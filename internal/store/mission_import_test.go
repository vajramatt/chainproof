package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
)

func TestImportMissionRoundTripRebuildsResumableState(t *testing.T) {
	ctx := context.Background()
	source, err := Open(t.TempDir() + "/source.db")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	mission, err := source.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Move verified work"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := source.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": "portable"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "First state", NextActions: []string{"continue elsewhere"}, Evidence: []continuity.EvidenceRef{{EventID: first.ID}}}); err != nil {
		t.Fatal(err)
	}
	second, err := source.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"status": "passed"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Ready to transfer", NextActions: []string{"resume on destination"}, Evidence: []continuity.EvidenceRef{{EventID: second.ID}}}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.MissionBundle(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}

	destination, err := Open(t.TempDir() + "/destination.db")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	result, err := destination.ImportMission(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mission.ID != mission.ID || result.RunCount != 1 || result.EventCount != 2 || result.CheckpointCount != 2 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	resumed, err := destination.ResumeMission(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Verification.Valid || resumed.Checkpoint == nil || resumed.Checkpoint.Summary != "Ready to transfer" {
		t.Fatalf("imported mission is not resumable: %+v", resumed)
	}
	contextEnvelope, err := destination.BuildMissionContext(ctx, mission.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(contextEnvelope.Evidence) != 1 || contextEnvelope.Evidence[0].ID != second.ID || contextEnvelope.HasUncheckpointedWork {
		t.Fatalf("imported context was not rebuilt: %+v", contextEnvelope)
	}
	search, err := destination.Search(ctx, SearchQuery{Text: "passed", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if search.Total != 1 || search.Hits[0].EventID != second.ID {
		t.Fatalf("derived search index was not rebuilt: %+v", search)
	}
	newRun, err := destination.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = destination.Append(ctx, newRun.ID, proof.EventInput{Kind: "work.resumed", Source: proof.Source{Adapter: "test", Mode: "reported"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.CreateCheckpoint(ctx, mission.ID, newRun.ID, continuity.CheckpointInput{Summary: "Work resumed on destination"}); err != nil {
		t.Fatal(err)
	}
	if verification := destination.VerifyMission(ctx, mission.ID); !verification.Valid || verification.CheckpointCount != 3 {
		t.Fatalf("destination could not extend imported continuity: %+v", verification)
	}
}

func TestImportMissionRejectsInvalidBundleAtomically(t *testing.T) {
	ctx := context.Background()
	source, bundle := missionImportFixture(t)
	defer source.Close()
	bundle.Checkpoints[0].Summary = "tampered"
	destination, err := Open(t.TempDir() + "/destination.db")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if _, err = destination.ImportMission(ctx, bundle); !errors.Is(err, ErrInvalidMissionImport) {
		t.Fatalf("invalid bundle error = %v", err)
	}
	if _, err = destination.Mission(ctx, bundle.Mission.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid import left mission state: %v", err)
	}
	var runs int
	if err = destination.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("invalid import left run state: count=%d err=%v", runs, err)
	}
}

func TestImportMissionRejectsDuplicateCheckpointIDs(t *testing.T) {
	ctx := context.Background()
	source, bundle := missionImportFixture(t)
	defer source.Close()
	first := bundle.Checkpoints[0]
	second := first
	second.Sequence = 1
	second.PreviousHash = first.CheckpointHash
	second.Timestamp = first.Timestamp.Add(time.Second)
	second.CheckpointHash = ""
	second.CheckpointHash, _ = continuity.Hash(second)
	bundle.Checkpoints = append(bundle.Checkpoints, second)
	bundle.RunProofs = append(bundle.RunProofs, bundle.RunProofs[0])
	bundle.Mission.CheckpointCount = 2
	bundle.Mission.ChainHead = second.CheckpointHash
	destination, err := Open(t.TempDir() + "/destination.db")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if _, err = destination.ImportMission(ctx, bundle); !errors.Is(err, ErrInvalidMissionImport) {
		t.Fatalf("duplicate checkpoint ID error = %v", err)
	}
}

func TestImportMissionRejectsDuplicateEventIDs(t *testing.T) {
	ctx := context.Background()
	source, bundle := missionImportFixture(t)
	defer source.Close()
	runProof := &bundle.RunProofs[0]
	second := runProof.Events[0]
	second.Sequence = 1
	second.PreviousHash = runProof.Events[0].EventHash
	second.Timestamp = second.Timestamp.Add(time.Second)
	second.Kind = "decision.repeated"
	second.EventHash = ""
	second.EventHash, _ = proof.Hash(second)
	runProof.Events = append(runProof.Events, second)
	runProof.Run.EntryCount = 2
	runProof.Run.ChainHead = second.EventHash
	bundle.Checkpoints[0].Run.EntryCount = 2
	bundle.Checkpoints[0].Run.ChainHead = second.EventHash
	bundle.Checkpoints[0].CheckpointHash = ""
	bundle.Checkpoints[0].CheckpointHash, _ = continuity.Hash(bundle.Checkpoints[0])
	bundle.Mission.ChainHead = bundle.Checkpoints[0].CheckpointHash
	destination, err := Open(t.TempDir() + "/destination.db")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if _, err = destination.ImportMission(ctx, bundle); !errors.Is(err, ErrInvalidMissionImport) {
		t.Fatalf("duplicate event ID error = %v", err)
	}
}

func TestImportMissionRefusesEveryCollision(t *testing.T) {
	ctx := context.Background()
	source, bundle := missionImportFixture(t)
	defer source.Close()
	destination, err := Open(t.TempDir() + "/destination.db")
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if _, err = destination.ImportMission(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	if _, err = destination.ImportMission(ctx, bundle); !errors.Is(err, ErrMissionImportCollision) {
		t.Fatalf("duplicate mission import error = %v", err)
	}

	other, err := Open(t.TempDir() + "/other.db")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	run := bundle.RunProofs[0].Run
	metadata, err := proof.CanonicalJSON(run.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.db.ExecContext(ctx, `INSERT INTO runs VALUES(?,?,?,?,?,?,?,?,?,?)`, run.ID, run.Agent, run.Harness, run.Model, "idle", run.StartedAt.Format(time.RFC3339Nano), nil, 0, proof.GenesisHash, string(metadata)); err != nil {
		t.Fatal(err)
	}
	if _, err = other.ImportMission(ctx, bundle); !errors.Is(err, ErrMissionImportCollision) {
		t.Fatalf("run collision error = %v", err)
	}
	if _, err = other.Mission(ctx, bundle.Mission.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("collision import left mission state: %v", err)
	}
}

func TestConcurrentMissionImportHasOneAtomicWinner(t *testing.T) {
	ctx := context.Background()
	source, bundle := missionImportFixture(t)
	defer source.Close()
	database := t.TempDir() + "/destination.db"
	first, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stores := []*Store{first, second}
	start := make(chan struct{})
	errorsByWorker := make([]error, len(stores))
	var workers sync.WaitGroup
	for index := range stores {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, errorsByWorker[index] = stores[index].ImportMission(ctx, bundle)
		}(index)
	}
	close(start)
	workers.Wait()
	winners, collisions := 0, 0
	for _, importErr := range errorsByWorker {
		switch {
		case importErr == nil:
			winners++
		case errors.Is(importErr, ErrMissionImportCollision):
			collisions++
		default:
			t.Fatalf("unexpected concurrent import error: %v", importErr)
		}
	}
	if winners != 1 || collisions != 1 {
		t.Fatalf("winners=%d collisions=%d errors=%v", winners, collisions, errorsByWorker)
	}
	if verification := first.VerifyMission(ctx, bundle.Mission.ID); !verification.Valid {
		t.Fatalf("winning import is invalid: %+v", verification)
	}
}

func missionImportFixture(t *testing.T) (*Store, continuity.Bundle) {
	t.Helper()
	ctx := context.Background()
	source, err := Open(t.TempDir() + "/source.db")
	if err != nil {
		t.Fatal(err)
	}
	mission, err := source.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Import verified mission"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := source.Start(ctx, "builder", "test", "gpt-test", map[string]any{"mission_id": mission.ID})
	if err != nil {
		t.Fatal(err)
	}
	event, err := source.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": "import"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Portable", Evidence: []continuity.EvidenceRef{{EventID: event.ID}}}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.MissionBundle(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	return source, bundle
}

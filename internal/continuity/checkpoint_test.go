package continuity

import (
	"strings"
	"testing"
	"time"

	"github.com/vajramatt/chainproof/internal/proof"
)

func TestVerifyDetectsCheckpointPayloadTampering(t *testing.T) {
	mission := Mission{
		ID:        "mission-1",
		Agent:     "builder",
		Objective: "Ship durable continuity",
	}
	checkpoint, err := NewCheckpoint(mission, 0, GenesisHash, time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC), CheckpointInput{
		Run:         RunAnchor{RunID: "run-1", EntryCount: 3, ChainHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Summary:     "Store mission state",
		NextActions: []string{"add resume command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mission.CheckpointCount = 1
	mission.ChainHead = checkpoint.CheckpointHash
	if got := Verify(mission, []Checkpoint{checkpoint}); !got.Valid {
		t.Fatalf("valid checkpoint rejected: %+v", got)
	}
	checkpoint.Summary = "Pretend everything shipped"
	if got := Verify(mission, []Checkpoint{checkpoint}); got.Valid || got.Reason != "checkpoint_hash_mismatch" {
		t.Fatalf("tampering was not detected: %+v", got)
	}
}

func TestVerifyDetectsCheckpointTruncation(t *testing.T) {
	mission := Mission{
		ID:              "mission-1",
		Agent:           "builder",
		Objective:       "Ship durable continuity",
		CheckpointCount: 1,
		ChainHead:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if got := Verify(mission, nil); got.Valid || got.Reason != "checkpoint_count_mismatch" {
		t.Fatalf("truncation was not detected: %+v", got)
	}
}

func TestNewCheckpointRequiresResumableSummary(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship durable continuity"}
	_, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
		Run: RunAnchor{RunID: "run-1", ChainHead: GenesisHash},
	})
	if err == nil || err.Error() != "checkpoint summary is required" {
		t.Fatalf("empty summary was accepted: %v", err)
	}
}

func TestVerifyBundleDetectsTamperedAnchoredRun(t *testing.T) {
	at := time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC)
	event := proof.Event{
		SchemaVersion: "1", ID: "event-1", RunID: "run-1", PreviousHash: proof.GenesisHash,
		Timestamp: at, Kind: "decision", Actor: proof.Actor{Type: "agent", Name: "builder"},
		Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": "continue"},
		Artifacts: []any{}, Extensions: map[string]any{},
	}
	eventHash, err := proof.Hash(event)
	if err != nil {
		t.Fatal(err)
	}
	event.EventHash = eventHash
	run := proof.Run{ID: "run-1", Agent: "builder", Status: "idle", StartedAt: at, EntryCount: 1, ChainHead: eventHash, Metadata: map[string]any{}}
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity", Status: "active", CreatedAt: at, UpdatedAt: at, ChainHead: GenesisHash, Metadata: map[string]any{}}
	checkpoint, err := NewCheckpoint(mission, 0, GenesisHash, at, CheckpointInput{Run: RunAnchor{RunID: run.ID, EntryCount: 1, ChainHead: eventHash}, Summary: "Run complete"})
	if err != nil {
		t.Fatal(err)
	}
	mission.CheckpointCount = 1
	mission.ChainHead = checkpoint.CheckpointHash
	bundle := Bundle{Format: "chainproof.continuity.bundle.v1", Mission: mission, Checkpoints: []Checkpoint{checkpoint}, RunProofs: []proof.Bundle{{Format: "chainproof.bundle.v1", Run: run, Events: []proof.Event{event}}}}
	if got := VerifyBundle(bundle); !got.Valid {
		t.Fatalf("valid continuity bundle rejected: %+v", got)
	}
	bundle.RunProofs[0].Events[0].Payload = map[string]any{"choice": "stop"}
	if got := VerifyBundle(bundle); got.Valid || got.Reason != "run_anchor_invalid" {
		t.Fatalf("tampered run proof accepted: %+v", got)
	}
}

func TestVerifyBundleRejectsForkedProofsForSameRun(t *testing.T) {
	at := time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC)
	makeEvent := func(id, choice string) proof.Event {
		event := proof.Event{
			SchemaVersion: "1", ID: id, RunID: "run-1", PreviousHash: proof.GenesisHash,
			Timestamp: at, Kind: "decision", Actor: proof.Actor{Type: "agent", Name: "builder"},
			Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": choice},
			Artifacts: []any{}, Extensions: map[string]any{},
		}
		hash, err := proof.Hash(event)
		if err != nil {
			t.Fatal(err)
		}
		event.EventHash = hash
		return event
	}
	first := makeEvent("event-1", "left")
	fork := makeEvent("event-2", "right")
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Reject run forks", Status: "active", CreatedAt: at, UpdatedAt: at, ChainHead: GenesisHash, Metadata: map[string]any{}}
	checkpointOne, err := NewCheckpoint(mission, 0, GenesisHash, at, CheckpointInput{Run: RunAnchor{RunID: "run-1", EntryCount: 1, ChainHead: first.EventHash}, Summary: "First fork"})
	if err != nil {
		t.Fatal(err)
	}
	checkpointTwo, err := NewCheckpoint(mission, 1, checkpointOne.CheckpointHash, at.Add(time.Second), CheckpointInput{Run: RunAnchor{RunID: "run-1", EntryCount: 1, ChainHead: fork.EventHash}, Summary: "Conflicting fork"})
	if err != nil {
		t.Fatal(err)
	}
	mission.CheckpointCount = 2
	mission.ChainHead = checkpointTwo.CheckpointHash
	bundle := Bundle{
		Format: "chainproof.continuity.bundle.v1", Mission: mission,
		Checkpoints: []Checkpoint{checkpointOne, checkpointTwo},
		RunProofs: []proof.Bundle{
			{Format: "chainproof.bundle.v1", Run: proof.Run{ID: "run-1", Agent: "builder", Status: "idle", StartedAt: at, EntryCount: 1, ChainHead: first.EventHash, Metadata: map[string]any{}}, Events: []proof.Event{first}},
			{Format: "chainproof.bundle.v1", Run: proof.Run{ID: "run-1", Agent: "builder", Status: "idle", StartedAt: at, EntryCount: 1, ChainHead: fork.EventHash, Metadata: map[string]any{}}, Events: []proof.Event{fork}},
		},
	}
	if got := VerifyBundle(bundle); got.Valid || got.Reason != "run_proof_fork" {
		t.Fatalf("forked proofs for one run were accepted: %+v", got)
	}
}

func TestVerifyBundleRejectsEvidenceOutsideAnchoredRun(t *testing.T) {
	at := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	event := proof.Event{
		SchemaVersion: "1", ID: "event-1", RunID: "run-1", PreviousHash: proof.GenesisHash,
		Timestamp: at, Kind: "decision", Actor: proof.Actor{Type: "agent", Name: "builder"},
		Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": "continue"},
		Artifacts: []any{}, Extensions: map[string]any{},
	}
	event.EventHash, _ = proof.Hash(event)
	run := proof.Run{ID: "run-1", Agent: "builder", Status: "idle", StartedAt: at, EntryCount: 1, ChainHead: event.EventHash, Metadata: map[string]any{}}
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Bind evidence", Status: "active", CreatedAt: at, UpdatedAt: at, ChainHead: GenesisHash, Metadata: map[string]any{}}
	checkpoint, err := NewCheckpoint(mission, 0, GenesisHash, at, CheckpointInput{
		Run:      RunAnchor{RunID: run.ID, EntryCount: 1, ChainHead: event.EventHash},
		Summary:  "Claims missing evidence",
		Evidence: []EvidenceRef{{EventID: "event-outside-prefix"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mission.CheckpointCount = 1
	mission.ChainHead = checkpoint.CheckpointHash
	bundle := Bundle{Format: "chainproof.continuity.bundle.v1", Mission: mission, Checkpoints: []Checkpoint{checkpoint}, RunProofs: []proof.Bundle{{Format: "chainproof.bundle.v1", Run: run, Events: []proof.Event{event}}}}
	if got := VerifyBundle(bundle); got.Valid || got.Reason != "evidence_anchor_invalid" {
		t.Fatalf("evidence outside anchored run was accepted: %+v", got)
	}
}

func TestNewCheckpointMarksResumableStateAsReported(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity"}
	checkpoint, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
		Run:     RunAnchor{RunID: "run-1", ChainHead: GenesisHash},
		Summary: "Continue from here",
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Source.Mode != "reported" || checkpoint.Source.Adapter != "continuity" {
		t.Fatalf("checkpoint provenance not explicit: %+v", checkpoint.Source)
	}
}

func TestNewCheckpointRejectsUnknownProvenanceMode(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity"}
	_, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
		Run:     RunAnchor{RunID: "run-1", ChainHead: GenesisHash},
		Source:  proof.Source{Adapter: "custom", Mode: "invented"},
		Summary: "Continue from here",
	})
	if err == nil || err.Error() != "invalid checkpoint collection mode" {
		t.Fatalf("unknown checkpoint mode was accepted: %v", err)
	}
}

func TestVerifyDetectsCommitmentStateTampering(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity"}
	checkpoint, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
		Run:     RunAnchor{RunID: "run-1", ChainHead: GenesisHash},
		Summary: "Implementation started",
		Commitments: []Commitment{{
			ID: "context", Description: "Build context compiler", Status: "pending",
			AcceptanceCriteria: []string{"bounded output", "verified evidence"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mission.CheckpointCount = 1
	mission.ChainHead = checkpoint.CheckpointHash
	checkpoint.Commitments[0].Status = "completed"
	if got := Verify(mission, []Checkpoint{checkpoint}); got.Valid || got.Reason != "checkpoint_hash_mismatch" {
		t.Fatalf("commitment tampering was not detected: %+v", got)
	}
}

func TestNewCheckpointRejectsInvalidCommitments(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity"}
	tests := []struct {
		name        string
		commitments []Commitment
		want        string
	}{
		{name: "missing id", commitments: []Commitment{{Description: "Build context", Status: "pending"}}, want: "commitment id is required"},
		{name: "missing description", commitments: []Commitment{{ID: "context", Status: "pending"}}, want: "description is required"},
		{name: "invalid status", commitments: []Commitment{{ID: "context", Description: "Build context", Status: "started"}}, want: "invalid commitment status"},
		{name: "duplicate id", commitments: []Commitment{{ID: "context", Description: "Build context", Status: "pending"}, {ID: "context", Description: "Build it", Status: "blocked"}}, want: "duplicate commitment id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
				Run: RunAnchor{RunID: "run-1", ChainHead: GenesisHash}, Summary: "Work recorded", Commitments: tt.commitments,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestVerifyRejectsCommitmentDefinitionChange(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity"}
	first, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
		Run: RunAnchor{RunID: "run-1", ChainHead: GenesisHash}, Summary: "Committed",
		Commitments: []Commitment{{ID: "context", Description: "Build context", Status: "pending", AcceptanceCriteria: []string{"bounded"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCheckpoint(mission, 1, first.CheckpointHash, time.Now(), CheckpointInput{
		Run: RunAnchor{RunID: "run-1", ChainHead: GenesisHash}, Summary: "Changed",
		Commitments: []Commitment{{ID: "context", Description: "Build unrelated feature", Status: "pending", AcceptanceCriteria: []string{"bounded"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mission.CheckpointCount = 2
	mission.ChainHead = second.CheckpointHash
	if got := Verify(mission, []Checkpoint{first, second}); got.Valid || got.Reason != "commitment_chain_mismatch" {
		t.Fatalf("commitment definition mutation verified: %+v", got)
	}
}

func TestEmptyCommitmentsPreserveLegacyCheckpointEncoding(t *testing.T) {
	mission := Mission{ID: "mission-1", Agent: "builder", Objective: "Ship continuity"}
	checkpoint, err := NewCheckpoint(mission, 0, GenesisHash, time.Now(), CheckpointInput{
		Run: RunAnchor{RunID: "run-1", ChainHead: GenesisHash}, Summary: "Legacy-compatible",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.CheckpointHash = ""
	raw, err := proof.CanonicalJSON(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"commitments"`) {
		t.Fatalf("empty commitments changed v1 canonical bytes: %s", raw)
	}
}

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
)

func TestMissionCheckpointRemainsVerifiableAfterRunContinues(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, err := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Ship durable continuity"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"status": "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{
		Summary:     "Storage layer works",
		NextActions: []string{"add resume command"},
		Evidence:    []continuity.EvidenceRef{{EventID: first.ID, Note: "passing test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Run.EntryCount != 1 || checkpoint.Run.ChainHead != first.EventHash {
		t.Fatalf("checkpoint did not anchor run prefix: %+v", checkpoint.Run)
	}
	if _, err = s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"next": "CLI"}}); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeMission(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint == nil || resumed.Checkpoint.Summary != "Storage layer works" || resumed.Mission.CheckpointCount != 1 {
		t.Fatalf("unexpected resume state: %+v", resumed)
	}
	if !resumed.Verification.Valid {
		t.Fatalf("checkpoint prefix did not verify after later append: %+v", resumed.Verification)
	}
}

func TestCreateCheckpointRejectsEvidenceOutsideAnchoredRun(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Keep evidence honest"})
	anchoredRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	otherRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	foreignEvent, _ := s.Append(ctx, otherRun.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	_, err = s.CreateCheckpoint(ctx, mission.ID, anchoredRun.ID, continuity.CheckpointInput{
		Summary:  "Claim unsupported success",
		Evidence: []continuity.EvidenceRef{{EventID: foreignEvent.ID}},
	})
	if err == nil || !strings.Contains(err.Error(), "outside anchored run prefix") {
		t.Fatalf("foreign evidence was accepted: %v", err)
	}
}

func TestCreateCheckpointRejectsCommitmentEvidenceOutsideAnchoredRun(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Keep commitments honest"})
	anchoredRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	otherRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	foreignEvent, _ := s.Append(ctx, otherRun.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	_, err = s.CreateCheckpoint(ctx, mission.ID, anchoredRun.ID, continuity.CheckpointInput{
		Summary: "Claim unsupported completion",
		Commitments: []continuity.Commitment{{
			ID: "context", Description: "Build context", Status: "completed", Evidence: []continuity.EvidenceRef{{EventID: foreignEvent.ID}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "outside anchored run prefix") {
		t.Fatalf("foreign commitment evidence was accepted: %v", err)
	}
}

func TestCreateCheckpointRejectsCommitmentMutationAndRegression(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Keep commitments stable"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	first := continuity.Commitment{ID: "context", Description: "Build context", Status: "completed", AcceptanceCriteria: []string{"bounded output"}}
	if _, err = s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Context built", Commitments: []continuity.Commitment{first}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		commitment continuity.Commitment
		want       string
	}{
		{name: "description", commitment: continuity.Commitment{ID: "context", Description: "Build different context", Status: "completed", AcceptanceCriteria: []string{"bounded output"}}, want: "definition changed"},
		{name: "criteria", commitment: continuity.Commitment{ID: "context", Description: "Build context", Status: "completed", AcceptanceCriteria: []string{"different"}}, want: "definition changed"},
		{name: "regression", commitment: continuity.Commitment{ID: "context", Description: "Build context", Status: "pending", AcceptanceCriteria: []string{"bounded output"}}, want: "cannot regress"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, checkpointErr := s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Next checkpoint", Commitments: []continuity.Commitment{tt.commitment}})
			if checkpointErr == nil || !strings.Contains(checkpointErr.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", checkpointErr, tt.want)
			}
		})
	}
}

func TestCreateCheckpointRequiresPriorCommitmentsInNextSnapshot(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Keep commitments visible"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	commitment := continuity.Commitment{ID: "context", Description: "Build context", Status: "pending"}
	if _, err = s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Committed", Commitments: []continuity.Commitment{commitment}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Commitment omitted"}); err == nil || !strings.Contains(err.Error(), "missing from checkpoint") {
		t.Fatalf("prior commitment disappeared: %v", err)
	}
}

func TestCompleteMissionRequiresValidCheckpointChain(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Finish work"})
	if _, err = s.CompleteMission(ctx, mission.ID); err == nil || err.Error() != "mission requires at least one valid checkpoint" {
		t.Fatalf("mission completed without checkpoint: %v", err)
	}
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	s.Append(ctx, run.ID, proof.EventInput{Kind: "run.completed", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Objective complete"})
	completed, err := s.CompleteMission(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" {
		t.Fatalf("mission not completed: %+v", completed)
	}
}

func TestCompleteMissionRequiresRecoveryReconciliation(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Finish without losing interrupted work"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Trusted state"})
	s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})

	if _, err = s.CompleteMission(ctx, mission.ID); err == nil || !strings.Contains(err.Error(), "uncheckpointed work") {
		t.Fatalf("mission completed with unreconciled tail: %v", err)
	}
	active, loadErr := s.Mission(ctx, mission.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if active.Status != "active" {
		t.Fatalf("blocked completion changed mission status: %+v", active)
	}
	if _, err = s.RejectRecovery(ctx, mission.ID, run.ID, "tail not needed"); err != nil {
		t.Fatal(err)
	}
	completed, err := s.CompleteMission(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" {
		t.Fatalf("reconciled mission did not complete: %+v", completed)
	}
	if _, err = s.Append(ctx, run.ID, proof.EventInput{Kind: "late.event", Source: proof.Source{Adapter: "test", Mode: "reported"}}); err == nil || !strings.Contains(err.Error(), "mission is completed") {
		t.Fatalf("completed mission accepted new work: %v", err)
	}
}

func TestMissionBundleContainsPortableAnchoredRunProof(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Export session proof"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "First session complete"})
	s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	bundle, err := s.MissionBundle(ctx, mission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.RunProofs) != 1 || len(bundle.RunProofs[0].Events) != 1 {
		t.Fatalf("bundle did not preserve anchored prefix: %+v", bundle.RunProofs)
	}
	if verification := continuity.VerifyBundle(bundle); !verification.Valid {
		t.Fatalf("portable mission bundle failed verification: %+v", verification)
	}
}

func TestBuildMissionContextReturnsVerifiedBoundedEvidence(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Compile trustworthy context"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	first, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": "local-first"}})
	second, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"status": "passed"}})
	checkpoint, err := s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{
		Summary:  "Architecture selected",
		Evidence: []continuity.EvidenceRef{{EventID: first.ID}},
		Commitments: []continuity.Commitment{{
			ID: "compiler", Description: "Build context compiler", Status: "completed",
			Evidence: []continuity.EvidenceRef{{EventID: first.ID}, {EventID: second.ID}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := s.BuildMissionContext(ctx, mission.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.SchemaVersion != "1" || compiled.Source.Mode != "derived" || compiled.Source.Adapter != "context-compiler" {
		t.Fatalf("context provenance missing: %+v", compiled)
	}
	if !compiled.Verification.Valid || compiled.Checkpoint == nil || compiled.Checkpoint.ID != checkpoint.ID {
		t.Fatalf("context not bound to verified checkpoint: %+v", compiled)
	}
	if len(compiled.Evidence) != 1 || compiled.Evidence[0].ID != first.ID || !compiled.EvidenceTruncated {
		t.Fatalf("evidence was not ordered, deduplicated, and bounded: %+v", compiled.Evidence)
	}
}

func TestBuildMissionContextRefusesInvalidContinuity(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Reject corrupt context"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Checkpoint created"})
	if _, err = s.db.ExecContext(ctx, `UPDATE checkpoints SET checkpoint_hash=? WHERE mission_id=?`, strings.Repeat("f", 64), mission.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BuildMissionContext(ctx, mission.ID, 10); err == nil || !strings.Contains(err.Error(), "continuity verification failed") {
		t.Fatalf("invalid continuity produced context: %v", err)
	}
}

func TestMissionsListsMostRecentAndFiltersStatus(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	first, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "First"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	s.CreateCheckpoint(ctx, first.ID, run.ID, continuity.CheckpointInput{Summary: "Done"})
	s.CompleteMission(ctx, first.ID)
	latest, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Latest"})

	active, err := s.Missions(ctx, "active", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != latest.ID {
		t.Fatalf("unexpected active missions: %+v", active)
	}
	all, err := s.Missions(ctx, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != latest.ID {
		t.Fatalf("missions not bounded and recent-first: %+v", all)
	}
	if _, err = s.Missions(ctx, "paused", 10); err == nil || !strings.Contains(err.Error(), "invalid mission status") {
		t.Fatalf("invalid status accepted: %v", err)
	}
}

func TestBuildMissionContextSurfacesUncheckpointedWorkWithoutPromotingIt(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Recover after interruption"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	anchored, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"choice": "safe"}})
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Known safe state", Evidence: []continuity.EvidenceRef{{EventID: anchored.ID}}})
	uncheckpointed, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"status": "unfinished"}})

	compiled, err := s.BuildMissionContext(ctx, mission.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.UncheckpointedWork) != 1 {
		t.Fatalf("uncheckpointed tail not surfaced: %+v", compiled.UncheckpointedWork)
	}
	if !compiled.HasUncheckpointedWork {
		t.Fatal("uncheckpointed work gate remained false")
	}
	tail := compiled.UncheckpointedWork[0]
	if tail.RunID != run.ID || tail.AnchoredEntryCount != 1 || tail.CurrentEntryCount != 2 || tail.EventCount != 1 || !tail.Verification.Valid {
		t.Fatalf("wrong uncheckpointed boundary: %+v", tail)
	}
	if len(compiled.Evidence) != 1 || compiled.Evidence[0].ID != anchored.ID || compiled.Evidence[0].ID == uncheckpointed.ID {
		t.Fatalf("uncheckpointed event was promoted into trusted context: %+v", compiled.Evidence)
	}
}

func TestBuildMissionContextFindsWorkBeforeFirstCheckpoint(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Recover initial work"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})

	compiled, err := s.BuildMissionContext(ctx, mission.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Checkpoint != nil || len(compiled.Evidence) != 0 {
		t.Fatalf("uncheckpointed initial work became trusted context: %+v", compiled)
	}
	if len(compiled.UncheckpointedWork) != 1 || compiled.UncheckpointedWork[0].AnchoredEntryCount != 0 || compiled.UncheckpointedWork[0].AnchoredChainHead != continuity.GenesisHash || compiled.UncheckpointedWork[0].EventCount != 1 {
		t.Fatalf("initial work not recoverable: %+v", compiled.UncheckpointedWork)
	}
}

func TestInspectRecoveryReturnsOnlyVerifiedUncheckpointedTail(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Recover reviewed work"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	anchored, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Trusted state", Evidence: []continuity.EvidenceRef{{EventID: anchored.ID}}})
	tail, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"status": "passed"}})

	inspection, err := s.InspectRecovery(ctx, mission.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SchemaVersion != "1" || inspection.Source.Mode != "derived" || inspection.Source.Adapter != "recovery-inspector" {
		t.Fatalf("inspection provenance missing: %+v", inspection)
	}
	if inspection.AnchoredEntryCount != 1 || inspection.AnchoredChainHead != anchored.EventHash || inspection.CurrentEntryCount != 2 || inspection.CurrentChainHead != tail.EventHash {
		t.Fatalf("wrong recovery boundary: %+v", inspection)
	}
	if !inspection.Verification.Valid || len(inspection.Events) != 1 || inspection.Events[0].ID != tail.ID {
		t.Fatalf("wrong recovery evidence: %+v", inspection)
	}
}

func TestAcceptRecoveryCreatesProofBoundCheckpointAndClearsWarning(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Accept recovered work"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	event, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"status": "passed"}})

	checkpoint, err := s.AcceptRecovery(ctx, mission.ID, run.ID, "reviewed output", continuity.CheckpointInput{
		Summary: "Recovered work passed review", Evidence: []continuity.EvidenceRef{{EventID: event.ID}}, NextActions: []string{"continue"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := checkpoint.Extensions["chainproof.recovery.v1"].(map[string]any)
	if !ok || recovery["decision"] != "accepted" || recovery["reason"] != "reviewed output" || recovery["from_entry_count"] != 0 || recovery["to_entry_count"] != 1 {
		t.Fatalf("recovery decision not proof-bound: %#v", checkpoint.Extensions)
	}
	compiled, err := s.BuildMissionContext(ctx, mission.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.HasUncheckpointedWork || len(compiled.UncheckpointedWork) != 0 || compiled.Checkpoint == nil || compiled.Checkpoint.ID != checkpoint.ID {
		t.Fatalf("accepted tail remains uncheckpointed: %+v", compiled)
	}
	s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	if _, err = s.AcceptRecovery(ctx, mission.ID, run.ID, "reviewed", continuity.CheckpointInput{Summary: "bad extension", Extensions: map[string]any{"chainproof.recovery.v1": map[string]any{"decision": "accepted"}}}); err == nil || !strings.Contains(err.Error(), "extension is reserved") {
		t.Fatalf("caller forged recovery extension: %v", err)
	}
}

func TestRejectRecoveryPreservesPriorStateWithoutPromotingTail(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Reject unsafe work"})
	trustedRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	trustedEvent, _ := s.Append(ctx, trustedRun.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	commitment := continuity.Commitment{ID: "safe", Description: "Keep trusted state", Status: "pending", Evidence: []continuity.EvidenceRef{{EventID: trustedEvent.ID}}}
	s.CreateCheckpoint(ctx, mission.ID, trustedRun.ID, continuity.CheckpointInput{Summary: "Known safe state", Commitments: []continuity.Commitment{commitment}, NextActions: []string{"resume safely"}, Evidence: []continuity.EvidenceRef{{EventID: trustedEvent.ID}}})
	interruptedRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	rejectedEvent, _ := s.Append(ctx, interruptedRun.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "imported"}, Payload: map[string]any{"claim": "unreviewed"}})

	checkpoint, err := s.RejectRecovery(ctx, mission.ID, interruptedRun.ID, "output contradicted tests")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Summary != "Known safe state" || len(checkpoint.NextActions) != 1 || checkpoint.NextActions[0] != "resume safely" {
		t.Fatalf("prior trusted state not carried forward: %+v", checkpoint)
	}
	if len(checkpoint.Commitments) != 1 || checkpoint.Commitments[0].ID != "safe" || len(checkpoint.Commitments[0].Evidence) != 0 || len(checkpoint.Evidence) != 0 {
		t.Fatalf("cross-run evidence was promoted: %+v", checkpoint)
	}
	recovery, ok := checkpoint.Extensions["chainproof.recovery.v1"].(map[string]any)
	if !ok || recovery["decision"] != "rejected" || recovery["reason"] != "output contradicted tests" || recovery["from_entry_count"] != 0 || recovery["to_entry_count"] != 1 {
		t.Fatalf("rejection not proof-bound: %#v", checkpoint.Extensions)
	}
	if checkpoint.Run.RunID != interruptedRun.ID || checkpoint.Run.ChainHead != rejectedEvent.EventHash {
		t.Fatalf("rejected tail boundary not anchored: %+v", checkpoint.Run)
	}
	compiled, err := s.BuildMissionContext(ctx, mission.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.HasUncheckpointedWork || len(compiled.Evidence) != 0 {
		t.Fatalf("rejected events became trusted or warning remained: %+v", compiled)
	}
}

func TestRejectRecoveryKeepsPriorEvidenceWhenRunIsUnchanged(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Keep safe evidence"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	trusted, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	commitment := continuity.Commitment{ID: "safe", Description: "Keep trusted state", Status: "pending", Evidence: []continuity.EvidenceRef{{EventID: trusted.ID}}}
	s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{Summary: "Known safe state", Commitments: []continuity.Commitment{commitment}, Evidence: []continuity.EvidenceRef{{EventID: trusted.ID}}})
	s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "reported"}})

	checkpoint, err := s.RejectRecovery(ctx, mission.ID, run.ID, "tail was wrong")
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Evidence) != 1 || checkpoint.Evidence[0].EventID != trusted.ID || len(checkpoint.Commitments[0].Evidence) != 1 || checkpoint.Commitments[0].Evidence[0].EventID != trusted.ID {
		t.Fatalf("safe same-run evidence was discarded: %+v", checkpoint)
	}
}

func TestRecoveryRejectsUnrelatedRunAndEmptyTail(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Recover exact mission work"})
	unrelated, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	s.Append(ctx, unrelated.ID, proof.EventInput{Kind: "event", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	if _, err = s.InspectRecovery(ctx, mission.ID, unrelated.ID); err == nil || !strings.Contains(err.Error(), "not associated") {
		t.Fatalf("unrelated run inspected: %v", err)
	}
	bound, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	if _, err = s.AcceptRecovery(ctx, mission.ID, bound.ID, "reviewed", continuity.CheckpointInput{Summary: "nothing"}); err == nil || !strings.Contains(err.Error(), "no uncheckpointed work") {
		t.Fatalf("empty tail accepted: %v", err)
	}
	if _, err = s.RejectRecovery(ctx, mission.ID, bound.ID, ""); err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("empty rejection reason accepted: %v", err)
	}
	s.Append(ctx, bound.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	checkpoint, err := s.RejectRecovery(ctx, mission.ID, bound.ID, "discard initial attempt")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Summary != "Uncheckpointed work rejected; no prior trusted checkpoint exists." || len(checkpoint.Commitments) != 0 || len(checkpoint.Evidence) != 0 {
		t.Fatalf("initial rejection did not establish empty trusted state: %+v", checkpoint)
	}
}

func TestRecoveryRefusesCorruptMissionContinuity(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Do not extend corrupt continuity"})
	trustedRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	s.Append(ctx, trustedRun.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	s.CreateCheckpoint(ctx, mission.ID, trustedRun.ID, continuity.CheckpointInput{Summary: "Trusted state"})
	interruptedRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", map[string]any{"mission_id": mission.ID})
	s.Append(ctx, interruptedRun.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	if _, err = s.db.ExecContext(ctx, `UPDATE checkpoints SET checkpoint_hash=? WHERE mission_id=?`, strings.Repeat("f", 64), mission.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RejectRecovery(ctx, mission.ID, interruptedRun.ID, "unsafe"); err == nil || !strings.Contains(err.Error(), "continuity verification failed") {
		t.Fatalf("corrupt continuity was extended: %v", err)
	}
	loaded, loadErr := s.Mission(ctx, mission.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.CheckpointCount != 1 {
		t.Fatalf("corrupt continuity gained checkpoint: %+v", loaded)
	}
}

func TestBuildMissionContextValidatesEvidenceBeyondOutputLimit(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mission, _ := s.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Validate every citation"})
	run, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	first, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	second, _ := s.Append(ctx, run.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	otherRun, _ := s.Start(ctx, "builder", "codex", "gpt-test", nil)
	foreign, _ := s.Append(ctx, otherRun.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "test", Mode: "observed"}})
	checkpoint, err := s.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{
		Summary: "Two citations", Evidence: []continuity.EvidenceRef{{EventID: first.ID}, {EventID: second.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Evidence[1].EventID = foreign.ID
	checkpoint.CheckpointHash = ""
	hash, err := continuity.Hash(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proof.CanonicalJSON(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE checkpoints SET checkpoint_json=?,checkpoint_hash=? WHERE checkpoint_id=?`, string(raw), hash, checkpoint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE missions SET chain_head=? WHERE mission_id=?`, hash, mission.ID); err != nil {
		t.Fatal(err)
	}

	if _, err = s.BuildMissionContext(ctx, mission.ID, 1); err == nil || !strings.Contains(err.Error(), "outside anchored run prefix") {
		t.Fatalf("invalid capped citation escaped validation: %v", err)
	}
}

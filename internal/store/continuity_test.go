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

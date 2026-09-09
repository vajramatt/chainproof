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

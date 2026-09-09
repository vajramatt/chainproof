package continuity

import (
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

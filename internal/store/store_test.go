package store

import (
	"context"
	"github.com/ChainProofAI/chainproof/internal/proof"
	"testing"
)

func TestLifecycleAndVerification(t *testing.T) {
	s, e := Open(t.TempDir() + "/test.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	r, e := s.Start(ctx, "qwen-agent", "opencode", "qwen3", nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"run.started", "tool.call", "tool.result"} {
		if _, e = s.Append(ctx, r.ID, proof.EventInput{Kind: kind, Source: proof.Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{"ok": true}}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.Complete(ctx, r.ID, "completed"); e != nil {
		t.Fatal(e)
	}
	v := s.Verify(ctx, r.ID)
	if !v.Valid || v.EntryCount != 3 {
		t.Fatalf("unexpected verification: %+v", v)
	}
}
func TestTamperingIsDetected(t *testing.T) {
	s, e := Open(t.TempDir() + "/test.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	r, _ := s.Start(ctx, "agent", "harness", "model", nil)
	s.Append(ctx, r.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "observed"}, Payload: map[string]any{"choice": "safe"}})
	s.db.Exec(`UPDATE events SET event_json=replace(event_json,'safe','unsafe') WHERE run_id=?`, r.ID)
	v := s.Verify(ctx, r.ID)
	if v.Valid || v.Reason != "event_hash_mismatch" {
		t.Fatalf("tamper was not detected: %+v", v)
	}
}
func TestTruncationIsDetected(t *testing.T) {
	s, e := Open(t.TempDir() + "/test.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	r, _ := s.Start(ctx, "agent", "harness", "model", nil)
	s.Append(ctx, r.ID, proof.EventInput{Kind: "one", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	s.Append(ctx, r.ID, proof.EventInput{Kind: "two", Source: proof.Source{Adapter: "test", Mode: "reported"}})
	s.db.Exec(`DELETE FROM events WHERE run_id=? AND sequence=1`, r.ID)
	v := s.Verify(ctx, r.ID)
	if v.Valid || v.Reason != "entry_count_mismatch" {
		t.Fatalf("truncation was not detected: %+v", v)
	}
}
func TestArtifactsAreByteCorrectAndContentAddressed(t *testing.T) {
	s, e := Open(t.TempDir() + "/test.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	body := []byte{0xff, 0x00, 0x80, 0x42}
	hash, e := s.PutArtifact(ctx, "", "application/octet-stream", body)
	if e != nil {
		t.Fatal(e)
	}
	loaded, media, e := s.Artifact(ctx, hash)
	if e != nil {
		t.Fatal(e)
	}
	if string(loaded) != string(body) || media != "application/octet-stream" {
		t.Fatal("artifact bytes or media type changed")
	}
	if _, e = s.PutArtifact(ctx, "wrong", "", body); e == nil {
		t.Fatal("expected content hash mismatch")
	}
}

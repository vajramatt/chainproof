package store

import (
	"context"
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/proof"
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

func TestSearchStructuredProvenance(t *testing.T) {
	s, e := Open(t.TempDir() + "/test.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	r, _ := s.Start(ctx, "repo-agent", "codex", "gpt-test", nil)
	s.Append(ctx, r.ID, proof.EventInput{Kind: "tool.result", Source: proof.Source{Adapter: "codex", Mode: "imported"}, Payload: map[string]any{"tool": "shell", "status": "completed", "command": map[string]any{"sha256": "abc123", "bytes": 42}}})
	s.Append(ctx, r.ID, proof.EventInput{Kind: "artifact.changed", Source: proof.Source{Adapter: "codex", Mode: "imported"}, Payload: map[string]any{"path": "internal/store/search.go", "status": "completed"}})
	result, e := s.Search(ctx, SearchQuery{Text: "abc123", Tool: "shell"})
	if e != nil {
		t.Fatal(e)
	}
	if result.Total != 1 || len(result.Hits) != 1 || result.Hits[0].Tool != "shell" {
		t.Fatalf("unexpected search: %+v", result)
	}
	if !strings.Contains(result.Hits[0].Summary, "completed") {
		t.Fatalf("missing evidence summary: %s", result.Hits[0].Summary)
	}
	result, e = s.Search(ctx, SearchQuery{Text: "search.go", Kind: "artifact.changed"})
	if e != nil || result.Total != 1 {
		t.Fatalf("path search failed: %+v %v", result, e)
	}
}

func TestOpenBackfillsProvenanceIndex(t *testing.T) {
	path := t.TempDir() + "/test.db"
	s, _ := Open(path)
	r, _ := s.Start(context.Background(), "agent", "harness", "model", nil)
	s.Append(context.Background(), r.ID, proof.EventInput{Kind: "decision", Source: proof.Source{Mode: "observed"}, Payload: map[string]any{"choice": "local-first"}})
	s.db.Exec(`DELETE FROM provenance_index`)
	s.Close()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	result, e := s.Search(context.Background(), SearchQuery{Text: "local-first"})
	if e != nil || result.Total != 1 {
		t.Fatalf("backfill failed: %+v %v", result, e)
	}
}

func TestRunLineageFromPortableMetadata(t *testing.T) {
	s, e := Open(t.TempDir() + "/test.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	parent, _ := s.Start(ctx, "orchestrator", "generic", "local", nil)
	child, _ := s.Start(ctx, "worker", "generic", "local", map[string]any{"parent_run_id": parent.ID})
	lineage, e := s.Lineage(ctx, child.ID)
	if e != nil || lineage.Parent == nil || lineage.Parent.ID != parent.ID {
		t.Fatalf("child lineage: %+v %v", lineage, e)
	}
	lineage, e = s.Lineage(ctx, parent.ID)
	if e != nil || len(lineage.Children) != 1 || lineage.Children[0].ID != child.ID {
		t.Fatalf("parent lineage: %+v %v", lineage, e)
	}
}

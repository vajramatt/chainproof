package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/proof"
)

func TestBackupCreatesConsistentReadOnlySnapshotWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	storePath := filepath.Join(root, "live.db")
	s, err := Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	run, err := s.Start(ctx, "backup-agent", "test", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Append(ctx, run.ID, proof.EventInput{Kind: "before-backup"}); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(root, "snapshot.db")
	if err = s.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err = CheckIntegrity(backupPath); err != nil {
		t.Fatalf("backup failed integrity check: %v", err)
	}
	backupStore, err := Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	backupRun, err := backupStore.Run(ctx, run.ID)
	closeErr := backupStore.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read backup: run_err=%v close_err=%v", err, closeErr)
	}
	if backupRun.EntryCount != 1 {
		t.Fatalf("backup entry count = %d, want 1", backupRun.EntryCount)
	}

	if err = os.WriteFile(filepath.Join(root, "existing.db"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	existingPath := filepath.Join(root, "existing.db")
	if err = s.Backup(ctx, existingPath); err == nil {
		t.Fatal("backup overwrote existing destination")
	}
	raw, err := os.ReadFile(existingPath)
	if err != nil || string(raw) != "preserve" {
		t.Fatalf("failed backup changed destination: data=%q err=%v", raw, err)
	}
}

func TestBackupRemainsVerifiableDuringIndependentWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ledgerPath := filepath.Join(root, "live.db")
	reader, err := Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	run, err := writer.Start(ctx, "writer", "test", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for index := 0; index < 50; index++ {
			if _, appendErr := writer.Append(ctx, run.ID, proof.EventInput{Kind: "concurrent-backup"}); appendErr != nil {
				done <- appendErr
				return
			}
			if index == 0 {
				close(started)
			}
		}
		done <- nil
	}()
	<-started
	backupPath := filepath.Join(root, "snapshot.db")
	if err = reader.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	snapshot, err := Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if verification := snapshot.Verify(ctx, run.ID); !verification.Valid {
		t.Fatalf("concurrent backup is not verifiable: %+v", verification)
	}
}

func TestOpenRepairsLedgerFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	path := t.TempDir() + "/test.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("ledger mode = %o, want 600", info.Mode().Perm())
	}
}

func TestCheckIntegrityAcceptsHealthyLedger(t *testing.T) {
	path := t.TempDir() + "/healthy.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = CheckIntegrity(path); err != nil {
		t.Fatalf("healthy ledger rejected: %v", err)
	}
}

func TestCheckIntegrityRejectsCorruptionWithoutModifyingFile(t *testing.T) {
	path := t.TempDir() + "/corrupt.db"
	want := "not a sqlite database"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckIntegrity(path); err == nil {
		t.Fatal("corrupt ledger passed integrity check")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("integrity check modified corrupt ledger: %q", raw)
	}
}

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

func TestAppendBindsRunAgentIdentityIntoEventChain(t *testing.T) {
	s, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	profile, err := identity.Ensure(t.TempDir(), "builder", "Builder", "codex")
	if err != nil {
		t.Fatal(err)
	}
	attribution := identity.Extension(profile, "worker:"+strings.Repeat("a", 32), "")
	run, err := s.Start(ctx, "Builder", "codex", "", map[string]any{identity.ExtensionKey: attribution})
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.Append(ctx, run.ID, proof.EventInput{
		Kind: "decision", Source: proof.Source{Adapter: "test", Mode: "reported"},
		Extensions: map[string]any{identity.ExtensionKey: map[string]any{"agent_id": "spoofed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event.Extensions[identity.ExtensionKey], attribution) {
		t.Fatalf("event did not inherit run attribution: %+v", event.Extensions)
	}
	if verification := s.Verify(ctx, run.ID); !verification.Valid {
		t.Fatalf("identity-bound run failed verification: %+v", verification)
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

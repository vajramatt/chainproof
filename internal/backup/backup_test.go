package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestCreateAndRestoreVerifiedInstance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ledgerPath := filepath.Join(root, "live", "chainproof.db")
	agentHome := filepath.Join(root, "live", "agents")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"builder", "reviewer"} {
		if _, err = identity.Ensure(agentHome, name, strings.ToUpper(name), "test"); err != nil {
			t.Fatal(err)
		}
	}
	run, err := s.Start(ctx, "builder", "test", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Append(ctx, run.ID, proof.EventInput{Kind: "backup.test"}); err != nil {
		t.Fatal(err)
	}
	artifactBody := []byte("artifact survives backup")
	artifactHash, err := s.PutArtifact(ctx, "", "text/plain", artifactBody)
	if err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(root, "backups", "snapshot")
	manifest, err := Create(ctx, s, agentHome, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != Format || len(manifest.Profiles) != 2 || len(manifest.Files) != 5 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	restorePath := filepath.Join(root, "restored", "instance")
	restored, err := Restore(backupPath, restorePath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Format != Format {
		t.Fatalf("restored format = %q", restored.Format)
	}
	if err = store.CheckIntegrity(filepath.Join(restorePath, "chainproof.db")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"builder", "reviewer"} {
		if _, err = identity.Verify(filepath.Join(restorePath, "agents"), name); err != nil {
			t.Fatalf("restored profile %q: %v", name, err)
		}
	}
	restoredStore, err := store.Open(filepath.Join(restorePath, "chainproof.db"))
	if err != nil {
		t.Fatal(err)
	}
	if verification := restoredStore.Verify(ctx, run.ID); !verification.Valid {
		t.Fatalf("restored run failed verification: %+v", verification)
	}
	gotArtifact, mediaType, artifactErr := restoredStore.Artifact(ctx, artifactHash)
	closeErr := restoredStore.Close()
	if artifactErr != nil || closeErr != nil || mediaType != "text/plain" || string(gotArtifact) != string(artifactBody) {
		t.Fatalf("restored artifact: body=%q media=%q artifact_err=%v close_err=%v", gotArtifact, mediaType, artifactErr, closeErr)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{restorePath, filepath.Join(restorePath, "chainproof.db"), filepath.Join(restorePath, "agents", "builder", "identity.key")} {
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			want := os.FileMode(0600)
			if info.IsDir() {
				want = 0700
			}
			if info.Mode().Perm() != want {
				t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want)
			}
		}
	}
}

func TestRestoreRejectsTamperWithoutPublishingDestination(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agentHome := filepath.Join(root, "agents")
	if _, err = identity.Ensure(agentHome, "default", "Agent", "test"); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "backup")
	if _, err = Create(ctx, s, agentHome, backupPath); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(backupPath, "agents", "default", "profile.json")
	if err = os.WriteFile(profilePath, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "restored")
	if _, err = Restore(backupPath, destination); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered backup accepted: %v", err)
	}
	if _, err = os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed restore published destination: %v", err)
	}
}

func TestRestoreRejectsUnsafeManifestProfileName(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	backupPath := filepath.Join(root, "backup")
	if _, err = Create(ctx, s, filepath.Join(root, "missing-agents"), backupPath); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(backupPath, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Profiles = []string{"../outside"}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(backupPath, filepath.Join(root, "restore")); err == nil || !strings.Contains(err.Error(), "invalid agent profile name") {
		t.Fatalf("unsafe profile name not rejected: %v", err)
	}
}

func TestRestoreRejectsLedgerWithInvalidProofChain(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(ctx, "agent", "test", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Append(ctx, run.ID, proof.EventInput{Kind: "proof"}); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "backup")
	if _, err = Create(ctx, s, filepath.Join(root, "missing-agents"), backupPath); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}

	backupDB := filepath.Join(backupPath, "chainproof.db")
	db, err := sql.Open("sqlite", backupDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE runs SET chain_head=? WHERE run_id=?`, proof.GenesisHash, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(backupPath, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for index := range manifest.Files {
		if manifest.Files[index].Path == "chainproof.db" {
			manifest.Files[index].SHA256, manifest.Files[index].Size, err = hashRegularFile(backupDB)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(backupPath, filepath.Join(root, "restore")); err == nil || !strings.Contains(err.Error(), "proof") {
		t.Fatalf("invalid proof chain accepted: %v", err)
	}
}

func TestBackupOperationsNeverOverwriteDestinations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agentHome := filepath.Join(root, "agents")
	if _, err = identity.Ensure(agentHome, "default", "Agent", "test"); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "backup")
	if _, err = Create(ctx, s, agentHome, backupPath); err != nil {
		t.Fatal(err)
	}
	if _, err = Create(ctx, s, agentHome, backupPath); err == nil {
		t.Fatal("backup overwrote existing destination")
	}
	restorePath := filepath.Join(root, "restore")
	if err = os.MkdirAll(restorePath, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(restorePath, "keep")
	if err = os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(backupPath, restorePath); err == nil {
		t.Fatal("restore overwrote existing destination")
	}
	if raw, readErr := os.ReadFile(marker); readErr != nil || string(raw) != "keep" {
		t.Fatalf("restore changed existing destination: data=%q err=%v", raw, readErr)
	}
}

func TestCreateRejectsIncompleteIdentityWithoutPublishingBackup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(filepath.Join(root, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agentHome := filepath.Join(root, "agents")
	if _, err = identity.Ensure(agentHome, "default", "Agent", "test"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(agentHome, "default", "identity.key")); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "backup")
	if _, err = Create(ctx, s, agentHome, destination); !errors.Is(err, identity.ErrIncomplete) {
		t.Fatalf("incomplete identity backup error = %v", err)
	}
	if _, err = os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed backup published destination: %v", err)
	}
}

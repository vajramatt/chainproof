package missionworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

func TestWorkspaceRoundTripCarriesProofViewsAndArtifacts(t *testing.T) {
	ctx := context.Background()
	source, mission, artifactHash, artifactBody := workspaceFixture(t)
	defer source.Close()
	root := filepath.Join(t.TempDir(), "mission-workspace")
	manifest, err := Create(ctx, source, mission.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != Format || manifest.MissionID != mission.ID || len(manifest.Files) != 4 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	for _, name := range []string{"manifest.json", "continuity.json", "events.jsonl", "README.md", filepath.Join("artifacts", artifactHash)} {
		info, statErr := os.Lstat(filepath.Join(root, name))
		if statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("workspace file %s: info=%v err=%v", name, info, statErr)
		}
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil || !strings.Contains(string(readme), mission.Objective) || !strings.Contains(string(readme), "Integrity does not prove truth") {
		t.Fatalf("generated README: %q err=%v", readme, err)
	}
	events, err := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if err != nil || strings.Count(string(events), "\n") != 1 || !strings.Contains(string(events), artifactHash) {
		t.Fatalf("event projection: %q err=%v", events, err)
	}
	if _, err = Verify(root); err != nil {
		t.Fatal(err)
	}

	destination, err := store.Open(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	result, err := Import(ctx, destination, root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mission.ID != mission.ID || result.EventCount != 1 || result.CheckpointCount != 1 || result.ArtifactCount != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	resumed, err := destination.ResumeMission(ctx, mission.ID)
	if err != nil || !resumed.Verification.Valid || resumed.Checkpoint == nil {
		t.Fatalf("imported mission did not resume: %+v err=%v", resumed, err)
	}
	body, mediaType, err := destination.Artifact(ctx, artifactHash)
	if err != nil || string(body) != string(artifactBody) || mediaType != "text/plain; charset=utf-8" {
		t.Fatalf("imported artifact: body=%q media=%q err=%v", body, mediaType, err)
	}
}

func TestWorkspaceTamperingFailsBeforeDatabaseMutation(t *testing.T) {
	ctx := context.Background()
	source, mission, artifactHash, _ := workspaceFixture(t)
	defer source.Close()
	root := filepath.Join(t.TempDir(), "mission-workspace")
	if _, err := Create(ctx, source, mission.ID, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", artifactHash), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	destination, err := store.Open(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if _, err = Import(ctx, destination, root); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered workspace error = %v", err)
	}
	if _, err = destination.Mission(ctx, mission.ID); err == nil {
		t.Fatal("tampered workspace imported mission")
	}
	if _, _, err = destination.Artifact(ctx, artifactHash); err == nil {
		t.Fatal("tampered workspace imported artifact")
	}
}

func TestWorkspaceRejectsRechecksummedDerivedView(t *testing.T) {
	ctx := context.Background()
	source, mission, _, _ := workspaceFixture(t)
	defer source.Close()
	root := filepath.Join(t.TempDir(), "mission-workspace")
	if _, err := Create(ctx, source, mission.ID, root); err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(root, "README.md")
	tampered := []byte("# Fake mission summary\n")
	if err := os.WriteFile(readmePath, tampered, 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(tampered)
	for index := range manifest.Files {
		if manifest.Files[index].Path == "README.md" {
			manifest.Files[index].SHA256 = hex.EncodeToString(sum[:])
			manifest.Files[index].Size = int64(len(tampered))
		}
	}
	raw, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(root); err == nil || !strings.Contains(err.Error(), "README projection") {
		t.Fatalf("rechecksummed false README accepted: %v", err)
	}
}

func TestWorkspaceCreateRefusesExistingDestination(t *testing.T) {
	ctx := context.Background()
	source, mission, _, _ := workspaceFixture(t)
	defer source.Close()
	root := filepath.Join(t.TempDir(), "existing")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "keep")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, source, mission.ID, root); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("existing destination error = %v", err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "preserve" {
		t.Fatalf("existing destination changed: %q err=%v", body, err)
	}
}

func workspaceFixture(t *testing.T) (*store.Store, continuity.Mission, string, []byte) {
	t.Helper()
	ctx := context.Background()
	source, err := store.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	mission, err := source.StartMission(ctx, continuity.MissionInput{Agent: "builder", Objective: "Carry verified work between machines"})
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	run, err := source.Start(ctx, "builder", "test", "gpt-test", map[string]any{"mission_id": mission.ID})
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	body := []byte("portable artifact body\n")
	hash, err := source.PutArtifact(ctx, "", "text/plain; charset=utf-8", body)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	event, err := source.Append(ctx, run.ID, proof.EventInput{
		Kind: "artifact.created", Source: proof.Source{Adapter: "test", Mode: "observed"},
		Payload:   map[string]any{"path": "result.txt"},
		Artifacts: []any{map[string]any{"hash": hash, "media_type": "text/plain; charset=utf-8"}},
	})
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	if _, err = source.CreateCheckpoint(ctx, mission.ID, run.ID, continuity.CheckpointInput{
		Summary: "Artifact ready for transfer", NextActions: []string{"resume on another instance"},
		Evidence: []continuity.EvidenceRef{{EventID: event.ID}},
	}); err != nil {
		source.Close()
		t.Fatal(err)
	}
	return source, mission, hash, body
}

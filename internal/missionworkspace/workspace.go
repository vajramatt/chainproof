// Package missionworkspace creates verified, portable filesystem projections
// of one ChainProof mission without copying local identity or lease state.
package missionworkspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vajramatt/chainproof/internal/continuity"
	"github.com/vajramatt/chainproof/internal/proof"
	"github.com/vajramatt/chainproof/internal/store"
)

const Format = "chainproof.mission-workspace.v1"

var (
	ErrInvalid           = errors.New("mission workspace is invalid")
	ErrDestinationExists = errors.New("mission workspace destination already exists")
)

type File struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type,omitempty"`
}

type Manifest struct {
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	MissionID string    `json:"mission_id"`
	Files     []File    `json:"files"`
}

type ImportResult struct {
	store.MissionImportResult
	ArtifactCount int `json:"artifacts_imported"`
}

func Create(ctx context.Context, ledger *store.Store, missionID, destination string) (manifest Manifest, err error) {
	bundle, err := ledger.MissionBundle(ctx, missionID)
	if err != nil {
		return Manifest{}, err
	}
	continuityJSON, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	continuityJSON = append(continuityJSON, '\n')
	eventsJSONL, err := eventProjection(bundle)
	if err != nil {
		return Manifest{}, err
	}
	readme := readmeProjection(bundle)
	refs, err := referencedArtifacts(bundle)
	if err != nil {
		return Manifest{}, err
	}

	absDestination, err := prepareDestination(destination)
	if err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(absDestination), ".chainproof-mission-workspace-*")
	if err != nil {
		return Manifest{}, err
	}
	if err = os.Chmod(stage, 0700); err != nil {
		_ = os.RemoveAll(stage)
		return Manifest{}, err
	}
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()

	files := map[string]workspaceContent{
		"README.md":       {body: readme, mediaType: "text/markdown; charset=utf-8"},
		"continuity.json": {body: continuityJSON, mediaType: "application/json"},
		"events.jsonl":    {body: eventsJSONL, mediaType: "application/x-ndjson"},
	}
	hashes := make([]string, 0, len(refs))
	for hash := range refs {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		body, mediaType, loadErr := ledger.Artifact(ctx, hash)
		if loadErr != nil {
			return Manifest{}, fmt.Errorf("load referenced artifact %s: %w", hash, loadErr)
		}
		if refs[hash] != "" && refs[hash] != mediaType {
			return Manifest{}, fmt.Errorf("artifact %s media type does not match event reference", hash)
		}
		files[path.Join("artifacts", hash)] = workspaceContent{body: body, mediaType: mediaType}
	}

	paths := make([]string, 0, len(files))
	for relative := range files {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	manifest = Manifest{Format: Format, CreatedAt: time.Now().UTC(), MissionID: bundle.Mission.ID, Files: make([]File, 0, len(paths))}
	for _, relative := range paths {
		content := files[relative]
		if err = writeExclusive(filepath.Join(stage, filepath.FromSlash(relative)), content.body); err != nil {
			return Manifest{}, err
		}
		sum := sha256.Sum256(content.body)
		manifest.Files = append(manifest.Files, File{Path: relative, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content.body)), MediaType: content.mediaType})
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err = writeExclusive(filepath.Join(stage, "manifest.json"), append(manifestJSON, '\n')); err != nil {
		return Manifest{}, err
	}
	if _, _, _, err = verify(stage); err != nil {
		return Manifest{}, err
	}
	if _, err = os.Lstat(absDestination); err == nil {
		return Manifest{}, fmt.Errorf("%w: %s", ErrDestinationExists, absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err = os.Rename(stage, absDestination); err != nil {
		return Manifest{}, fmt.Errorf("publish mission workspace: %w", err)
	}
	stage = ""
	return manifest, nil
}

func Verify(root string) (Manifest, error) {
	manifest, _, _, err := verify(root)
	return manifest, err
}

func Import(ctx context.Context, ledger *store.Store, root string) (ImportResult, error) {
	_, bundle, artifacts, err := verify(root)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	result, err := ledger.ImportMissionWithArtifacts(ctx, bundle, artifacts)
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{MissionImportResult: result, ArtifactCount: len(artifacts)}, nil
}

type workspaceContent struct {
	body      []byte
	mediaType string
}

func verify(root string) (Manifest, continuity.Bundle, []store.MissionImportArtifact, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Manifest{}, continuity.Bundle{}, nil, err
	}
	info, err := os.Lstat(absRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Manifest{}, continuity.Bundle{}, nil, errors.New("workspace root is not a regular directory")
	}
	manifestRaw, err := readRegularFile(filepath.Join(absRoot, "manifest.json"))
	if err != nil {
		return Manifest{}, continuity.Bundle{}, nil, err
	}
	var manifest Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.Format != Format || manifest.CreatedAt.IsZero() || strings.TrimSpace(manifest.MissionID) == "" {
		return Manifest{}, continuity.Bundle{}, nil, errors.New("invalid mission workspace manifest")
	}
	declared := make(map[string]File, len(manifest.Files))
	content := make(map[string][]byte, len(manifest.Files))
	for index, file := range manifest.Files {
		if !validWorkspacePath(file.Path) || file.Size < 0 || !validSHA256(file.SHA256) {
			return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("invalid workspace file record %q", file.Path)
		}
		if _, exists := declared[file.Path]; exists || (index > 0 && manifest.Files[index-1].Path >= file.Path) {
			return Manifest{}, continuity.Bundle{}, nil, errors.New("workspace file records must be unique and sorted")
		}
		body, readErr := readRegularFile(filepath.Join(absRoot, filepath.FromSlash(file.Path)))
		if readErr != nil {
			return Manifest{}, continuity.Bundle{}, nil, readErr
		}
		sum := sha256.Sum256(body)
		if int64(len(body)) != file.Size || hex.EncodeToString(sum[:]) != file.SHA256 {
			return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("workspace checksum mismatch for %s", file.Path)
		}
		declared[file.Path] = file
		content[file.Path] = body
	}
	for _, required := range []string{"README.md", "continuity.json", "events.jsonl"} {
		if _, exists := declared[required]; !exists {
			return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("workspace manifest is missing %s", required)
		}
	}
	for relative, mediaType := range map[string]string{
		"README.md": "text/markdown; charset=utf-8", "continuity.json": "application/json", "events.jsonl": "application/x-ndjson",
	} {
		if declared[relative].MediaType != mediaType {
			return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("invalid media type for %s", relative)
		}
	}
	if err = verifyFileInventory(absRoot, declared); err != nil {
		return Manifest{}, continuity.Bundle{}, nil, err
	}

	var bundle continuity.Bundle
	if err = json.Unmarshal(content["continuity.json"], &bundle); err != nil {
		return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("decode continuity proof: %w", err)
	}
	if err = store.ValidateMissionImport(bundle); err != nil {
		return Manifest{}, continuity.Bundle{}, nil, err
	}
	if bundle.Mission.ID != manifest.MissionID {
		return Manifest{}, continuity.Bundle{}, nil, errors.New("workspace mission ID mismatch")
	}
	expectedEvents, err := eventProjection(bundle)
	if err != nil || !bytes.Equal(expectedEvents, content["events.jsonl"]) {
		return Manifest{}, continuity.Bundle{}, nil, errors.New("event projection does not match continuity proof")
	}
	if !bytes.Equal(readmeProjection(bundle), content["README.md"]) {
		return Manifest{}, continuity.Bundle{}, nil, errors.New("README projection does not match continuity proof")
	}
	refs, err := referencedArtifacts(bundle)
	if err != nil {
		return Manifest{}, continuity.Bundle{}, nil, err
	}
	artifacts := make([]store.MissionImportArtifact, 0, len(refs))
	for relative, file := range declared {
		if !strings.HasPrefix(relative, "artifacts/") {
			continue
		}
		hash := strings.TrimPrefix(relative, "artifacts/")
		refMediaType, exists := refs[hash]
		if !exists || (refMediaType != "" && refMediaType != file.MediaType) {
			return Manifest{}, continuity.Bundle{}, nil, fmt.Errorf("unexpected artifact file %s", relative)
		}
		artifacts = append(artifacts, store.MissionImportArtifact{Hash: hash, MediaType: file.MediaType, Body: content[relative]})
	}
	if len(artifacts) != len(refs) {
		return Manifest{}, continuity.Bundle{}, nil, errors.New("workspace is missing a referenced artifact")
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Hash < artifacts[j].Hash })
	return manifest, bundle, artifacts, nil
}

func eventProjection(bundle continuity.Bundle) ([]byte, error) {
	byRun := make(map[string]proof.Bundle)
	order := make([]string, 0, len(bundle.RunProofs))
	for _, runProof := range bundle.RunProofs {
		prior, exists := byRun[runProof.Run.ID]
		if !exists {
			order = append(order, runProof.Run.ID)
		}
		if !exists || len(runProof.Events) > len(prior.Events) {
			byRun[runProof.Run.ID] = runProof
		}
	}
	var out bytes.Buffer
	for _, runID := range order {
		for _, event := range byRun[runID].Events {
			raw, err := json.Marshal(event)
			if err != nil {
				return nil, err
			}
			out.Write(raw)
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

func readmeProjection(bundle continuity.Bundle) []byte {
	mission := bundle.Mission
	var out strings.Builder
	fmt.Fprintln(&out, "# ChainProof Mission")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "- Mission ID: `%s`\n", markdownCode(mission.ID))
	fmt.Fprintf(&out, "- Agent: %s\n", oneLine(mission.Agent))
	fmt.Fprintf(&out, "- Status: `%s`\n", markdownCode(mission.Status))
	fmt.Fprintf(&out, "- Checkpoints: %d\n", mission.CheckpointCount)
	fmt.Fprintf(&out, "- Chain head: `%s`\n", markdownCode(mission.ChainHead))
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Objective")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "> %s\n", oneLine(mission.Objective))
	if len(bundle.Checkpoints) > 0 {
		checkpoint := bundle.Checkpoints[len(bundle.Checkpoints)-1]
		fmt.Fprintln(&out)
		fmt.Fprintln(&out, "## Latest Checkpoint")
		fmt.Fprintln(&out)
		fmt.Fprintf(&out, "%s\n", oneLine(checkpoint.Summary))
		if len(checkpoint.NextActions) > 0 {
			fmt.Fprintln(&out)
			fmt.Fprintln(&out, "### Next Actions")
			fmt.Fprintln(&out)
			for index, action := range checkpoint.NextActions {
				fmt.Fprintf(&out, "%d. %s\n", index+1, oneLine(action))
			}
		}
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Proof Boundary")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "Integrity does not prove truth. Verify `continuity.json` and `manifest.json`; provenance modes describe collection, not trust.")
	return []byte(out.String())
}

func referencedArtifacts(bundle continuity.Bundle) (map[string]string, error) {
	refs := map[string]string{}
	for _, runProof := range bundle.RunProofs {
		for _, event := range runProof.Events {
			for _, raw := range event.Artifacts {
				artifact, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				hash, hasHash := artifact["hash"].(string)
				if !hasHash {
					continue
				}
				if !validSHA256(hash) {
					return nil, fmt.Errorf("invalid referenced artifact hash %q", hash)
				}
				mediaType, _ := artifact["media_type"].(string)
				if prior, exists := refs[hash]; exists && prior != "" && mediaType != "" && prior != mediaType {
					return nil, fmt.Errorf("conflicting media types for artifact %s", hash)
				}
				if refs[hash] == "" {
					refs[hash] = mediaType
				}
			}
		}
	}
	return refs, nil
}

func prepareDestination(destination string) (string, error) {
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(absDestination); err == nil {
		return "", fmt.Errorf("%w: %s", ErrDestinationExists, absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(absDestination), 0700); err != nil {
		return "", err
	}
	return absDestination, nil
}

func validWorkspacePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == "manifest.json" {
		return false
	}
	if value == "README.md" || value == "continuity.json" || value == "events.jsonl" {
		return true
	}
	hash := strings.TrimPrefix(value, "artifacts/")
	return hash != value && !strings.Contains(hash, "/") && validSHA256(hash)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func verifyFileInventory(root string, declared map[string]File) error {
	want := map[string]struct{}{"manifest.json": {}}
	for relative := range declared {
		want[relative] = struct{}{}
	}
	seen := map[string]struct{}{}
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace contains symlink %s", relative)
		}
		if entry.IsDir() {
			if relative != "artifacts" {
				return fmt.Errorf("workspace contains unexpected directory %s", relative)
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("workspace contains non-regular file %s", relative)
		}
		if _, exists := want[relative]; !exists {
			return fmt.Errorf("workspace contains undeclared file %s", relative)
		}
		seen[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(want) {
		return errors.New("workspace is missing declared files")
	}
	return nil
}

func readRegularFile(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace file is not regular: %s", filename)
	}
	return os.ReadFile(filename)
}

func writeExclusive(filename string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func markdownCode(value string) string {
	return strings.ReplaceAll(oneLine(value), "`", "\\`")
}

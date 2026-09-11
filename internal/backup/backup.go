// Package backup creates and restores verified, non-destructive snapshots of a
// complete local ChainProof instance.
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vajramatt/chainproof/internal/identity"
	"github.com/vajramatt/chainproof/internal/store"
)

const Format = "chainproof.backup.v1"

var (
	ErrInvalid           = errors.New("backup is invalid")
	ErrDestinationExists = errors.New("destination already exists")
)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	Profiles  []string  `json:"profiles"`
	Files     []File    `json:"files"`
}

// Create writes a verified instance backup to a destination that must not
// already exist. The finished directory appears only after all files verify.
func Create(ctx context.Context, ledger *store.Store, agentHome, destination string) (manifest Manifest, err error) {
	absDestination, err := prepareDestination(destination)
	if err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(absDestination), ".chainproof-backup-*")
	if err != nil {
		return Manifest{}, err
	}
	if err = os.Chmod(stage, 0700); err != nil {
		os.RemoveAll(stage)
		return Manifest{}, err
	}
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()

	if err = ledger.Backup(ctx, filepath.Join(stage, "chainproof.db")); err != nil {
		return Manifest{}, err
	}
	profiles, err := copyProfiles(agentHome, filepath.Join(stage, "agents"))
	if err != nil {
		return Manifest{}, err
	}
	manifest = Manifest{Format: Format, CreatedAt: time.Now().UTC(), Profiles: profiles}
	manifest.Files, err = collectFiles(stage, profiles)
	if err != nil {
		return Manifest{}, err
	}
	if err = writeManifest(stage, manifest); err != nil {
		return Manifest{}, err
	}
	if _, err = verify(stage); err != nil {
		return Manifest{}, err
	}
	if _, err = os.Lstat(absDestination); err == nil {
		return Manifest{}, fmt.Errorf("%w: %s", ErrDestinationExists, absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err = os.Rename(stage, absDestination); err != nil {
		return Manifest{}, fmt.Errorf("publish backup: %w", err)
	}
	stage = ""
	return manifest, nil
}

// Restore verifies a backup, copies only manifest-declared files, verifies the
// copy again, and publishes it at a destination that must not already exist.
func Restore(source, destination string) (manifest Manifest, err error) {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err = verify(absSource)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	absDestination, err := prepareDestination(destination)
	if err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(absDestination), ".chainproof-restore-*")
	if err != nil {
		return Manifest{}, err
	}
	if err = os.Chmod(stage, 0700); err != nil {
		os.RemoveAll(stage)
		return Manifest{}, err
	}
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()

	for _, file := range manifest.Files {
		if err = copyRegularFile(filepath.Join(absSource, filepath.FromSlash(file.Path)), filepath.Join(stage, filepath.FromSlash(file.Path))); err != nil {
			return Manifest{}, err
		}
	}
	if err = copyRegularFile(filepath.Join(absSource, "manifest.json"), filepath.Join(stage, "manifest.json")); err != nil {
		return Manifest{}, err
	}
	if _, err = verify(stage); err != nil {
		return Manifest{}, fmt.Errorf("verify restored instance: %w", err)
	}
	if _, err = os.Lstat(absDestination); err == nil {
		return Manifest{}, fmt.Errorf("%w: %s", ErrDestinationExists, absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err = os.Rename(stage, absDestination); err != nil {
		return Manifest{}, fmt.Errorf("publish restored instance: %w", err)
	}
	stage = ""
	return manifest, nil
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

func copyProfiles(sourceRoot, destinationRoot string) ([]string, error) {
	if err := os.MkdirAll(destinationRoot, 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(sourceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	profiles := make([]string, 0)
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("agent home contains symlink %q", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err = identity.Verify(sourceRoot, name); err != nil {
			return nil, fmt.Errorf("verify agent profile %q: %w", name, err)
		}
		destination := filepath.Join(destinationRoot, name)
		if err = os.MkdirAll(destination, 0700); err != nil {
			return nil, err
		}
		for _, filename := range []string{"profile.json", "identity.key"} {
			if err = copyRegularFile(filepath.Join(sourceRoot, name, filename), filepath.Join(destination, filename)); err != nil {
				return nil, err
			}
		}
		if _, err = identity.Verify(destinationRoot, name); err != nil {
			return nil, fmt.Errorf("verify copied agent profile %q: %w", name, err)
		}
		profiles = append(profiles, name)
	}
	sort.Strings(profiles)
	return profiles, nil
}

func collectFiles(root string, profiles []string) ([]File, error) {
	paths := []string{"chainproof.db"}
	for _, profile := range profiles {
		paths = append(paths, filepath.ToSlash(filepath.Join("agents", profile, "identity.key")), filepath.ToSlash(filepath.Join("agents", profile, "profile.json")))
	}
	sort.Strings(paths)
	files := make([]File, 0, len(paths))
	for _, relative := range paths {
		sum, size, err := hashRegularFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: relative, SHA256: sum, Size: size})
	}
	return files, nil
}

func writeManifest(root string, manifest Manifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, "manifest.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(raw, '\n')); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func verify(root string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	if manifest.Format != Format || manifest.CreatedAt.IsZero() {
		return Manifest{}, errors.New("invalid backup manifest")
	}
	profileSet := map[string]bool{}
	for index, profile := range manifest.Profiles {
		if err = identity.ValidateProfileName(profile); err != nil {
			return Manifest{}, err
		}
		if profileSet[profile] || (index > 0 && manifest.Profiles[index-1] > profile) {
			return Manifest{}, errors.New("invalid backup profile list")
		}
		profileSet[profile] = true
	}
	wantPaths := map[string]bool{"chainproof.db": true}
	for _, profile := range manifest.Profiles {
		wantPaths[filepath.ToSlash(filepath.Join("agents", profile, "identity.key"))] = true
		wantPaths[filepath.ToSlash(filepath.Join("agents", profile, "profile.json"))] = true
	}
	seen := map[string]bool{}
	for _, file := range manifest.Files {
		if !wantPaths[file.Path] || seen[file.Path] || len(file.SHA256) != 64 || file.Size < 0 {
			return Manifest{}, fmt.Errorf("invalid backup file record %q", file.Path)
		}
		seen[file.Path] = true
		sum, size, hashErr := hashRegularFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if hashErr != nil {
			return Manifest{}, hashErr
		}
		if sum != file.SHA256 || size != file.Size {
			return Manifest{}, fmt.Errorf("backup checksum mismatch for %s", file.Path)
		}
	}
	if len(seen) != len(wantPaths) {
		return Manifest{}, errors.New("backup manifest is missing required files")
	}
	if err = store.CheckIntegrity(filepath.Join(root, "chainproof.db")); err != nil {
		return Manifest{}, fmt.Errorf("verify backup ledger: %w", err)
	}
	if err = store.CheckProofIntegrity(filepath.Join(root, "chainproof.db")); err != nil {
		return Manifest{}, fmt.Errorf("verify backup proofs: %w", err)
	}
	for _, profile := range manifest.Profiles {
		if _, err = identity.Verify(filepath.Join(root, "agents"), profile); err != nil {
			return Manifest{}, fmt.Errorf("verify backup agent profile %q: %w", profile, err)
		}
	}
	return manifest, nil
}

func copyRegularFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source is not a regular file: %s", source)
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

func hashRegularFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("backup file is not regular: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

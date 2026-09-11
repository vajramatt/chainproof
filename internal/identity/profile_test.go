package identity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestEnsureCreatesStablePrivateAgentProfile(t *testing.T) {
	root := t.TempDir()
	first, err := Ensure(root, "codex-main", "Forge", "codex")
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(root, "codex-main", "profile.json")
	if err = os.Chmod(profilePath, 0644); err != nil {
		t.Fatal(err)
	}
	second, err := Ensure(root, "codex-main", "Ignored rename", "other")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("agent identity changed across ensure calls:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if !strings.HasPrefix(first.AgentID, "agent:ed25519:") || first.DisplayName != "Forge" || first.Profile != "codex-main" || first.PublicKey == "" {
		t.Fatalf("unexpected profile: %+v", first)
	}

	keyPath := filepath.Join(root, "codex-main", "identity.key")
	if runtime.GOOS != "windows" {
		for _, path := range []string{profilePath, keyPath} {
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
			}
		}
	}
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "seed") {
		t.Fatalf("public profile leaked private material: %s", raw)
	}
	var stored Profile
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored != first {
		t.Fatalf("stored profile mismatch: %+v", stored)
	}
}

func TestConcurrentEnsureConvergesOnOneIdentity(t *testing.T) {
	root := t.TempDir()
	const workers = 24
	start := make(chan struct{})
	profiles := make(chan Profile, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			profile, err := Ensure(root, "shared", "Shared", "codex")
			if err != nil {
				errors <- err
				return
			}
			profiles <- profile
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	close(profiles)
	for err := range errors {
		t.Errorf("concurrent ensure failed: %v", err)
	}
	var expected Profile
	for profile := range profiles {
		if expected.AgentID == "" {
			expected = profile
		} else if profile != expected {
			t.Errorf("concurrent ensure returned different profile:\nwant: %+v\ngot:  %+v", expected, profile)
		}
	}
}

func TestEnsureRejectsUnsafeProfileNames(t *testing.T) {
	for _, name := range []string{"", "../escape", "nested/profile", ".", strings.Repeat("x", 65)} {
		if _, err := Ensure(t.TempDir(), name, "", ""); err == nil {
			t.Fatalf("unsafe profile name %q accepted", name)
		}
	}
}

func TestLoadRejectsTamperedPublicProfile(t *testing.T) {
	root := t.TempDir()
	profile, err := Ensure(root, "default", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	profile.AgentID = "agent:ed25519:" + strings.Repeat("0", 64)
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "default", "profile.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(root, "default"); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("tampered profile accepted: %v", err)
	}
}

func TestVerifyRequiresMatchingPrivateKey(t *testing.T) {
	root := t.TempDir()
	profile, err := Ensure(root, "default", "Primary", "codex")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(root, "default")
	if err != nil || verified != profile {
		t.Fatalf("valid identity did not verify: profile=%+v err=%v", verified, err)
	}
	otherRoot := t.TempDir()
	if _, err = Ensure(otherRoot, "other", "Other", "codex"); err != nil {
		t.Fatal(err)
	}
	otherKey, err := os.ReadFile(filepath.Join(otherRoot, "other", "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "default", "identity.key"), otherKey, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(root, "default"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched private key verified: %v", err)
	}
}

func TestEnsureDoesNotReplaceMissingPrivateKey(t *testing.T) {
	root := t.TempDir()
	before, err := Ensure(root, "default", "Primary", "codex")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "default", "identity.key")
	if err = os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	if _, err = Ensure(root, "default", "Replacement", "codex"); !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), "private key is missing") {
		t.Fatalf("missing private key was not rejected as incomplete identity: %v", err)
	}
	if _, err = os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ensure replaced missing private key: %v", err)
	}
	after, err := Load(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("ensure changed profile after key loss:\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func TestVerifyDistinguishesIncompleteIdentityFromUninitialized(t *testing.T) {
	root := t.TempDir()
	if _, err := Verify(root, "default"); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrIncomplete) {
		t.Fatalf("absent identity status = %v, want not-exist only", err)
	}
	if _, err := Ensure(root, "default", "Primary", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "default", "profile.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(root, "default"); !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), "profile is missing") {
		t.Fatalf("missing profile was not diagnosed as incomplete identity: %v", err)
	}
}

func TestEnsureDoesNotRecreateMissingProfile(t *testing.T) {
	root := t.TempDir()
	if _, err := Ensure(root, "default", "Primary", "codex"); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "default", "identity.key")
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(root, "default", "profile.json")
	if err = os.Remove(profilePath); err != nil {
		t.Fatal(err)
	}

	if _, err = Ensure(root, "default", "Replacement", "codex"); !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), "profile is missing") {
		t.Fatalf("missing profile was not rejected as incomplete identity: %v", err)
	}
	if _, err = os.Stat(profilePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ensure recreated missing profile: %v", err)
	}
	keyAfter, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(keyAfter) != string(keyBefore) {
		t.Fatal("ensure changed private key after profile loss")
	}
}

func TestEnsureDoesNotModifyCorruptIdentityMaterial(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root, profilePath, keyPath string)
	}{
		{
			name: "corrupt profile",
			mutate: func(t *testing.T, _, profilePath, _ string) {
				t.Helper()
				if err := os.WriteFile(profilePath, []byte("{\n"), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt private key",
			mutate: func(t *testing.T, _, _, keyPath string) {
				t.Helper()
				if err := os.WriteFile(keyPath, []byte("{\n"), 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mismatched private key",
			mutate: func(t *testing.T, root, _, keyPath string) {
				t.Helper()
				otherRoot := filepath.Join(root, "other")
				if _, err := Ensure(otherRoot, "other", "Other", "codex"); err != nil {
					t.Fatal(err)
				}
				otherKey, err := os.ReadFile(filepath.Join(otherRoot, "other", "identity.key"))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(keyPath, otherKey, 0600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Ensure(root, "default", "Primary", "codex"); err != nil {
				t.Fatal(err)
			}
			profilePath := filepath.Join(root, "default", "profile.json")
			keyPath := filepath.Join(root, "default", "identity.key")
			tt.mutate(t, root, profilePath, keyPath)
			profileBefore, err := os.ReadFile(profilePath)
			if err != nil {
				t.Fatal(err)
			}
			keyBefore, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = Ensure(root, "default", "Replacement", "codex"); err == nil {
				t.Fatal("corrupt identity was accepted")
			}
			profileAfter, profileErr := os.ReadFile(profilePath)
			keyAfter, keyErr := os.ReadFile(keyPath)
			if profileErr != nil || keyErr != nil || string(profileAfter) != string(profileBefore) || string(keyAfter) != string(keyBefore) {
				t.Fatalf("ensure modified corrupt identity: profile_err=%v key_err=%v", profileErr, keyErr)
			}
		})
	}
}

func TestRenameChangesDisplayNameWithoutChangingIdentity(t *testing.T) {
	root := t.TempDir()
	before, err := Ensure(root, "default", "First name", "codex")
	if err != nil {
		t.Fatal(err)
	}
	after, err := Rename(root, "default", "Second name")
	if err != nil {
		t.Fatal(err)
	}
	if after.DisplayName != "Second name" || after.AgentID != before.AgentID || after.PublicKey != before.PublicKey || after.CreatedAt != before.CreatedAt {
		t.Fatalf("rename changed durable identity: before=%+v after=%+v", before, after)
	}
	loaded, err := Load(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != after {
		t.Fatalf("renamed profile was not persisted: %+v", loaded)
	}
	for _, invalid := range []string{"", "\n\t", string([]byte{0x01})} {
		if _, err = Rename(root, "default", invalid); err == nil {
			t.Fatalf("invalid display name %q accepted", invalid)
		}
	}
}

func TestValidateExtensionChecksKeyFingerprintAndWorkerShape(t *testing.T) {
	profile, err := Ensure(t.TempDir(), "default", "Builder", "codex")
	if err != nil {
		t.Fatal(err)
	}
	valid := Extension(profile, "worker:"+strings.Repeat("a", 32), "reviewer")
	if err = ValidateExtension(valid); err != nil {
		t.Fatalf("valid extension rejected: %v", err)
	}
	valid["agent_id"] = "agent:ed25519:" + strings.Repeat("0", 64)
	if err = ValidateExtension(valid); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("invalid fingerprint accepted: %v", err)
	}
	valid = Extension(profile, "worker:not-hex", "reviewer")
	if err = ValidateExtension(valid); err == nil || !strings.Contains(err.Error(), "worker_id") {
		t.Fatalf("invalid worker ID accepted: %v", err)
	}
}

func TestValidateRoleAllowsEmptyAndReadableLabels(t *testing.T) {
	for _, role := range []string{"", "reviewer", "release manager"} {
		if err := ValidateRole(role); err != nil {
			t.Fatalf("valid role %q rejected: %v", role, err)
		}
	}
	for _, role := range []string{"\x01", strings.Repeat("x", 129)} {
		if err := ValidateRole(role); err == nil {
			t.Fatalf("invalid role %q accepted", role)
		}
	}
}

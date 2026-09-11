// Package identity manages private, local agent profiles.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ExtensionKey  = "chainproof.agent.v1"
	SchemaVersion = "1"
)

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var workerIDPattern = regexp.MustCompile(`^worker:[0-9a-f]{32}$`)

// ErrIncomplete means only part of an established identity remains. Callers
// must not repair this state by silently generating replacement material.
var ErrIncomplete = errors.New("agent identity is incomplete")

// Profile contains public attribution material. Private key material never
// appears in this type or command output.
type Profile struct {
	SchemaVersion string    `json:"schema_version"`
	Profile       string    `json:"profile"`
	AgentID       string    `json:"agent_id"`
	DisplayName   string    `json:"display_name"`
	Harness       string    `json:"harness,omitempty"`
	PublicKey     string    `json:"public_key"`
	CreatedAt     time.Time `json:"created_at"`
}

type privateKeyFile struct {
	SchemaVersion string `json:"schema_version"`
	Algorithm     string `json:"algorithm"`
	Seed          string `json:"seed"`
}

// Ensure loads a named profile or creates it once. Later display-name and
// harness arguments do not mutate an established identity.
func Ensure(root, profileName, displayName, harness string) (Profile, error) {
	if err := validateProfileName(profileName); err != nil {
		return Profile{}, err
	}
	dir := filepath.Join(root, profileName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Profile{}, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return Profile{}, err
	}

	profilePath := filepath.Join(dir, "profile.json")
	keyPath := filepath.Join(dir, "identity.key")
	profile, err := Load(root, profileName)
	if err == nil {
		privateKey, keyErr := loadPrivateKey(keyPath)
		if errors.Is(keyErr, os.ErrNotExist) {
			return Profile{}, fmt.Errorf("%w: private key is missing for existing profile %q", ErrIncomplete, profileName)
		}
		if keyErr != nil {
			return Profile{}, keyErr
		}
		if !publicKeysEqual(profile.PublicKey, privateKey.Public().(ed25519.PublicKey)) {
			return Profile{}, errors.New("agent profile public key does not match private key")
		}
		if err = os.Chmod(keyPath, 0600); err != nil {
			return Profile{}, err
		}
		if err = os.Chmod(profilePath, 0600); err != nil {
			return Profile{}, err
		}
		return profile, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Profile{}, err
	}
	if _, keyErr := os.Stat(keyPath); keyErr == nil {
		// Another initializer can briefly publish the key before the profile.
		// Give that bounded race time to converge, but never reconstruct a
		// persistently missing profile without an explicit recovery workflow.
		for range 50 {
			time.Sleep(10 * time.Millisecond)
			if profile, err = Load(root, profileName); err == nil {
				return Ensure(root, profileName, displayName, harness)
			} else if !errors.Is(err, os.ErrNotExist) {
				return Profile{}, err
			}
		}
		return Profile{}, fmt.Errorf("%w: profile is missing for existing private key %q", ErrIncomplete, profileName)
	} else if !errors.Is(keyErr, os.ErrNotExist) {
		return Profile{}, keyErr
	}

	privateKey, err := loadOrCreatePrivateKey(keyPath)
	if err != nil {
		return Profile{}, err
	}

	publicKey := privateKey.Public().(ed25519.PublicKey)
	agentID := fingerprint(publicKey)
	displayName = strings.TrimSpace(displayName)
	harness = strings.TrimSpace(harness)
	if harness != "" {
		if err = validateOptionalLabel(harness); err != nil {
			return Profile{}, fmt.Errorf("invalid agent harness: %w", err)
		}
	}
	if displayName == "" {
		prefix := harness
		if prefix == "" {
			prefix = "agent"
		}
		displayName = prefix + "-" + agentID[len(agentID)-8:]
	}
	if err = validateDisplayName(displayName); err != nil {
		return Profile{}, err
	}
	profile = Profile{
		SchemaVersion: SchemaVersion,
		Profile:       profileName,
		AgentID:       agentID,
		DisplayName:   displayName,
		Harness:       harness,
		PublicKey:     base64.RawURLEncoding.EncodeToString(publicKey),
		CreatedAt:     time.Now().UTC(),
	}
	if err = writeJSONOnce(profilePath, profile); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Profile{}, err
		}
		profile, err = Load(root, profileName)
		if err != nil {
			return Profile{}, err
		}
		if !publicKeysEqual(profile.PublicKey, publicKey) {
			return Profile{}, errors.New("agent profile public key does not match private key")
		}
	}
	return profile, nil
}

// Rename changes public presentation without rotating key-derived identity.
func Rename(root, profileName, displayName string) (Profile, error) {
	if err := validateProfileName(profileName); err != nil {
		return Profile{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if err := validateDisplayName(displayName); err != nil {
		return Profile{}, err
	}
	profile, err := Load(root, profileName)
	if err != nil {
		return Profile{}, err
	}
	privateKey, err := loadPrivateKey(filepath.Join(root, profileName, "identity.key"))
	if err != nil {
		return Profile{}, err
	}
	if !publicKeysEqual(profile.PublicKey, privateKey.Public().(ed25519.PublicKey)) {
		return Profile{}, errors.New("agent profile public key does not match private key")
	}
	profile.DisplayName = displayName
	if err = writeJSONReplacing(filepath.Join(root, profileName, "profile.json"), profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

// Load reads and validates public profile material.
func Load(root, profileName string) (Profile, error) {
	if err := validateProfileName(profileName); err != nil {
		return Profile{}, err
	}
	path := filepath.Join(root, profileName, "profile.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	if err = json.Unmarshal(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("read agent profile: %w", err)
	}
	if profile.SchemaVersion != SchemaVersion || profile.Profile != profileName || profile.CreatedAt.IsZero() {
		return Profile{}, errors.New("invalid agent profile")
	}
	if err = validateDisplayName(profile.DisplayName); err != nil {
		return Profile{}, err
	}
	if profile.Harness != "" {
		if err = validateOptionalLabel(profile.Harness); err != nil {
			return Profile{}, errors.New("invalid agent profile harness")
		}
	}
	publicKey, err := decodePublicKey(profile.PublicKey)
	if err != nil {
		return Profile{}, err
	}
	if profile.AgentID != fingerprint(publicKey) {
		return Profile{}, errors.New("agent profile fingerprint does not match public key")
	}
	return profile, nil
}

// Verify checks public profile integrity and possession of its matching local
// private key without creating or changing identity files.
func Verify(root, profileName string) (Profile, error) {
	profile, err := Load(root, profileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			keyPath := filepath.Join(root, profileName, "identity.key")
			if _, keyErr := os.Stat(keyPath); keyErr == nil {
				return Profile{}, fmt.Errorf("%w: profile is missing for existing private key %q", ErrIncomplete, profileName)
			} else if !errors.Is(keyErr, os.ErrNotExist) {
				return Profile{}, keyErr
			}
		}
		return Profile{}, err
	}
	privateKey, err := loadPrivateKey(filepath.Join(root, profileName, "identity.key"))
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, fmt.Errorf("%w: private key is missing for existing profile %q", ErrIncomplete, profileName)
	}
	if err != nil {
		return Profile{}, err
	}
	if !publicKeysEqual(profile.PublicKey, privateKey.Public().(ed25519.PublicKey)) {
		return Profile{}, errors.New("agent profile public key does not match private key")
	}
	return profile, nil
}

// Extension returns versioned public attribution suitable for run metadata,
// mission metadata, checkpoint extensions, and event extensions.
func Extension(profile Profile, workerID, role string) map[string]any {
	extension := map[string]any{
		"schema_version": profile.SchemaVersion,
		"profile":        profile.Profile,
		"agent_id":       profile.AgentID,
		"display_name":   profile.DisplayName,
		"public_key":     profile.PublicKey,
	}
	if profile.Harness != "" {
		extension["harness"] = profile.Harness
	}
	if workerID != "" {
		extension["worker_id"] = workerID
	}
	if role = strings.TrimSpace(role); role != "" {
		extension["role"] = role
	}
	return extension
}

// ValidateExtension checks public attribution structure and key-derived ID.
// It does not prove private-key possession.
func ValidateExtension(value any) error {
	extension, ok := value.(map[string]any)
	if !ok {
		return errors.New("agent attribution must be an object")
	}
	stringField := func(name string) (string, error) {
		value, ok := extension[name].(string)
		if !ok || value == "" {
			return "", fmt.Errorf("agent attribution requires %s", name)
		}
		return value, nil
	}
	schemaVersion, err := stringField("schema_version")
	if err != nil || schemaVersion != SchemaVersion {
		return errors.New("unsupported agent attribution schema version")
	}
	profileName, err := stringField("profile")
	if err != nil {
		return err
	}
	if err = validateProfileName(profileName); err != nil {
		return err
	}
	displayName, err := stringField("display_name")
	if err != nil {
		return err
	}
	if err = validateDisplayName(displayName); err != nil {
		return err
	}
	publicKeyText, err := stringField("public_key")
	if err != nil {
		return err
	}
	publicKey, err := decodePublicKey(publicKeyText)
	if err != nil {
		return err
	}
	agentID, err := stringField("agent_id")
	if err != nil {
		return err
	}
	if agentID != fingerprint(publicKey) {
		return errors.New("agent attribution fingerprint does not match public key")
	}
	if workerID, exists := extension["worker_id"]; exists {
		workerText, ok := workerID.(string)
		if !ok || !workerIDPattern.MatchString(workerText) {
			return errors.New("invalid agent attribution worker_id")
		}
	}
	for _, field := range []string{"harness", "role"} {
		if value, exists := extension[field]; exists {
			text, ok := value.(string)
			if !ok || validateOptionalLabel(text) != nil {
				return fmt.Errorf("invalid agent attribution %s", field)
			}
		}
	}
	return nil
}

// ValidateRole checks optional mission-specific role text.
func ValidateRole(role string) error {
	if strings.TrimSpace(role) == "" {
		return nil
	}
	if err := validateOptionalLabel(role); err != nil {
		return fmt.Errorf("invalid agent role: %w", err)
	}
	return nil
}

// NewWorkerID returns an ephemeral execution identity. It is intentionally not
// reused across run sessions.
func NewWorkerID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "worker:" + hex.EncodeToString(raw), nil
}

func validateProfileName(name string) error {
	if !profileNamePattern.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid agent profile name %q", name)
	}
	return nil
}

func validateDisplayName(name string) error {
	if name == "" || len([]rune(name)) > 128 {
		return errors.New("agent display name must contain 1 to 128 characters")
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return errors.New("agent display name cannot contain control characters")
		}
	}
	return nil
}

func validateOptionalLabel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 128 {
		return errors.New("label must contain 1 to 128 characters")
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return errors.New("label cannot contain control characters")
		}
	}
	return nil
}

func loadOrCreatePrivateKey(path string) (ed25519.PrivateKey, error) {
	if key, err := loadPrivateKey(path); err == nil {
		if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			return nil, chmodErr
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(privateKeyFile{
		SchemaVersion: SchemaVersion,
		Algorithm:     "ed25519",
		Seed:          base64.RawURLEncoding.EncodeToString(privateKey.Seed()),
	})
	if err != nil {
		return nil, err
	}
	if err = writeBytesOnce(path, append(encoded, '\n')); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadPrivateKey(path)
		}
		return nil, err
	}
	return privateKey, nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var stored privateKeyFile
	if err = json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("read agent private key: %w", err)
	}
	if stored.SchemaVersion != SchemaVersion || stored.Algorithm != "ed25519" {
		return nil, errors.New("invalid agent private key")
	}
	seed, err := base64.RawURLEncoding.DecodeString(stored.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid agent private key seed")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func decodePublicKey(encoded string) (ed25519.PublicKey, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid agent profile public key")
	}
	return ed25519.PublicKey(publicKey), nil
}

func publicKeysEqual(encoded string, expected ed25519.PublicKey) bool {
	publicKey, err := decodePublicKey(encoded)
	return err == nil && publicKey.Equal(expected)
}

func fingerprint(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return "agent:ed25519:" + hex.EncodeToString(sum[:])
}

func writeJSONOnce(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytesOnce(path, append(raw, '\n'))
}

func writeBytesOnce(path string, raw []byte) error {
	return writeBytes(path, raw, false)
}

func writeJSONReplacing(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(path, append(raw, '\n'), true)
}

func writeBytes(path string, raw []byte, replace bool) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".profile-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if replace {
		return os.Rename(tempPath, path)
	}
	return os.Link(tempPath, path)
}

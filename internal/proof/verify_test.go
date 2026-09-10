package proof

import (
	"strings"
	"testing"

	"github.com/vajramatt/chainproof/internal/identity"
)

func TestBundleRejectsWrongGenesis(t *testing.T) {
	event := Event{SchemaVersion: "1", ID: "e", RunID: "r", Sequence: 0, PreviousHash: "bad", Kind: "event", Actor: Actor{}, Source: Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{}, Artifacts: []any{}, Extensions: map[string]any{}}
	hash, _ := Hash(event)
	event.EventHash = hash
	bundle := Bundle{Format: "chainproof.bundle.v1", Run: Run{ID: "r", EntryCount: 1, ChainHead: hash}, Events: []Event{event}}
	if VerifyBundle(bundle).Valid {
		t.Fatal("wrong genesis accepted")
	}
}

func TestBundleRejectsAgentIdentityMetadataTampering(t *testing.T) {
	profile, err := identity.Ensure(t.TempDir(), "builder", "Builder", "codex")
	if err != nil {
		t.Fatal(err)
	}
	attribution := identity.Extension(profile, "worker:"+strings.Repeat("a", 32), "")
	event := Event{
		SchemaVersion: "1", ID: "e", RunID: "r", Sequence: 0, PreviousHash: GenesisHash,
		Kind: "event", Actor: Actor{}, Source: Source{Adapter: "test", Mode: "reported"},
		Payload: map[string]any{}, Artifacts: []any{}, Extensions: map[string]any{identity.ExtensionKey: attribution},
	}
	hash, err := Hash(event)
	if err != nil {
		t.Fatal(err)
	}
	event.EventHash = hash
	bundle := Bundle{
		Format: "chainproof.bundle.v1",
		Run:    Run{ID: "r", EntryCount: 1, ChainHead: hash, Metadata: map[string]any{identity.ExtensionKey: attribution}},
		Events: []Event{event},
	}
	if verification := VerifyBundle(bundle); !verification.Valid {
		t.Fatalf("valid identity binding rejected: %+v", verification)
	}
	tampered := identity.Extension(profile, "worker:"+strings.Repeat("a", 32), "")
	tampered["display_name"] = "Impostor"
	bundle.Run.Metadata[identity.ExtensionKey] = tampered
	if verification := VerifyBundle(bundle); verification.Valid || verification.Reason != "agent_identity_mismatch" {
		t.Fatalf("identity metadata tampering was not detected: %+v", verification)
	}
	invalid := identity.Extension(profile, "worker:"+strings.Repeat("a", 32), "")
	invalid["agent_id"] = "agent:ed25519:" + strings.Repeat("0", 64)
	bundle.Run.Metadata[identity.ExtensionKey] = invalid
	bundle.Events[0].Extensions[identity.ExtensionKey] = invalid
	bundle.Events[0].EventHash = ""
	invalidHash, err := Hash(bundle.Events[0])
	if err != nil {
		t.Fatal(err)
	}
	bundle.Events[0].EventHash = invalidHash
	bundle.Run.ChainHead = invalidHash
	if verification := VerifyBundle(bundle); verification.Valid || verification.Reason != "invalid_agent_identity" {
		t.Fatalf("invalid key fingerprint was accepted: %+v", verification)
	}
}

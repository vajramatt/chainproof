package proof

import "testing"

func TestBundleRejectsWrongGenesis(t *testing.T) {
	event := Event{SchemaVersion: "1", ID: "e", RunID: "r", Sequence: 0, PreviousHash: "bad", Kind: "event", Actor: Actor{}, Source: Source{Adapter: "test", Mode: "reported"}, Payload: map[string]any{}, Artifacts: []any{}, Extensions: map[string]any{}}
	hash, _ := Hash(event)
	event.EventHash = hash
	bundle := Bundle{Format: "chainproof.bundle.v1", Run: Run{ID: "r", EntryCount: 1, ChainHead: hash}, Events: []Event{event}}
	if VerifyBundle(bundle).Valid {
		t.Fatal("wrong genesis accepted")
	}
}

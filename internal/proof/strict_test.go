package proof

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func strictFixture(t *testing.T, payload any) []byte {
	t.Helper()
	event := Event{SchemaVersion: "1", ID: "e", RunID: "r", Sequence: 0, PreviousHash: GenesisHash, Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Kind: "event", Actor: Actor{Type: "agent"}, Source: Source{Adapter: "test", Mode: "reported"}, Payload: payload, Artifacts: []any{}, Extensions: map[string]any{}}
	hash, err := Hash(event)
	if err != nil {
		t.Fatal(err)
	}
	event.EventHash = hash
	raw, err := json.MarshalIndent(Bundle{Format: "chainproof.bundle.v1", Run: Run{ID: "r", Agent: "a", Status: "completed", EntryCount: 1, ChainHead: hash, Metadata: map[string]any{}}, Events: []Event{event}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCanonicalNumbersMatchLegacyFloatHashing(t *testing.T) {
	values := []float64{0, math.Copysign(0, -1), 1, -42, 1.5, 0.1, 1e-7, 1e20, 1e21, 123456789012, 9007199254740993, 12345678901234567890, math.MaxFloat64, math.SmallestNonzeroFloat64}
	for _, value := range values {
		legacy, err := CanonicalJSON(map[string]any{"n": value})
		if err != nil {
			t.Fatal(err)
		}
		text, _ := json.Marshal(value)
		current, err := CanonicalJSON(map[string]any{"n": json.Number(text)})
		if err != nil {
			t.Fatal(err)
		}
		if string(legacy) != string(current) {
			t.Errorf("%v: legacy %s != json.Number %s", value, legacy, current)
		}
	}
}

func TestCanonicalNumbersPreserveLargeIntegers(t *testing.T) {
	raw, err := CanonicalJSON(map[string]any{"n": json.Number("12345678901234567891")})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"n":12345678901234567891}` {
		t.Fatalf("large integer was rounded: %s", raw)
	}
}

func TestDecodeStrictAcceptsExportedBundle(t *testing.T) {
	raw := strictFixture(t, map[string]any{"n": json.Number("12345678901234567891"), "f": 1.5, "nested": map[string]any{"ok": true}})
	var bundle Bundle
	if err := DecodeStrict(raw, &bundle); err != nil {
		t.Fatalf("exported bundle rejected: %v", err)
	}
	if verification := VerifyBundle(bundle); !verification.Valid {
		t.Fatalf("exported bundle does not verify: %+v", verification)
	}
	if got := bundle.Events[0].Payload.(map[string]any)["n"]; got != json.Number("12345678901234567891") {
		t.Fatalf("large integer not preserved: %#v", got)
	}
}

func TestDecodeStrictRejectsAmbiguousBundles(t *testing.T) {
	raw := string(strictFixture(t, map[string]any{"value": "real"}))
	mutations := map[string]string{
		"case-variant duplicate": strings.Replace(raw, `"payload": {`, `"PAYLOAD": {"value": "forged"}, "payload": {`, 1),
		"case-variant field":     strings.Replace(raw, `"payload": {`, `"Payload": {`, 1),
		"duplicate field":        strings.Replace(raw, `"kind": "event",`, `"kind": "event", "kind": "other",`, 1),
		"duplicate payload key":  strings.Replace(raw, `"value": "real"`, `"value": "forged", "value": "real"`, 1),
		"unknown field":          strings.Replace(raw, `"kind": "event",`, `"kind": "event", "note": "extra",`, 1),
		"unknown run field":      strings.Replace(raw, `"agent": "a",`, `"agent": "a", "trusted": true,`, 1),
		"non-canonical integer":  strings.Replace(raw, `"value": "real"`, `"value": 1.0`, 1),
		"exponent number":        strings.Replace(raw, `"value": "real"`, `"value": 1E2`, 1),
		"trailing document":      raw + `{}`,
	}
	for name, mutated := range mutations {
		if mutated == raw && name != "trailing document" {
			t.Fatalf("%s: mutation did not apply", name)
		}
		var bundle Bundle
		if err := DecodeStrict([]byte(mutated), &bundle); err == nil {
			t.Errorf("%s: ambiguous bundle accepted", name)
		}
	}
}

func TestStrictBundleDetectsLargeIntegerTampering(t *testing.T) {
	raw := string(strictFixture(t, map[string]any{"amount": json.Number("12345678901234567891")}))
	tampered := strings.Replace(raw, "12345678901234567891", "12345678901234567892", 1)
	var bundle Bundle
	if err := DecodeStrict([]byte(tampered), &bundle); err != nil {
		t.Fatal(err)
	}
	if verification := VerifyBundle(bundle); verification.Valid || verification.Reason != "event_hash_mismatch" {
		t.Fatalf("large integer tampering verified: %+v", verification)
	}
}

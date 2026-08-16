package proof

import (
	"math"
	"testing"
)

func TestCanonicalJSONIsKeyOrderIndependent(t *testing.T) {
	a := map[string]any{"z": 1, "a": map[string]any{"y": true, "b": "x"}}
	b := map[string]any{"a": map[string]any{"b": "x", "y": true}, "z": 1}
	ah, _ := Hash(a)
	bh, _ := Hash(b)
	if ah != bh {
		t.Fatalf("hashes differ: %s != %s", ah, bh)
	}
}
func TestCanonicalJSONRejectsUnsupportedNumber(t *testing.T) {
	if _, err := CanonicalJSON(map[string]any{"bad": math.Inf(1)}); err == nil {
		t.Fatal("expected non-finite number error")
	}
}

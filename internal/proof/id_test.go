package proof

import (
	"strings"
	"testing"
)

func TestValidIDAcceptsGeneratedFormats(t *testing.T) {
	for _, id := range []string{
		"6f9619ff-8b86-d011-b42d-00cf4fc964ff",
		"codex-" + strings.Repeat("a", 64),
		"mission-1", "run.two", "worker:abc", "e", strings.Repeat("x", MaxIDLength),
	} {
		if !ValidID(id) {
			t.Errorf("valid ID %q rejected", id)
		}
	}
}

func TestValidIDRejectsMarkupAndControlCharacters(t *testing.T) {
	for _, id := range []string{
		"", strings.Repeat("x", MaxIDLength+1), `x" onmouseover="alert(1)`, "<script>", "a b",
		"a\tb", "a\nb", "a\x00b", "a\x1bb", "a'b", "a/b", "a\u0085b", "é",
	} {
		if ValidID(id) {
			t.Errorf("unsafe ID %q accepted", id)
		}
	}
}

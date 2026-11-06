package proof

import "regexp"

// MaxIDLength bounds client-supplied and imported record identifiers.
const MaxIDLength = 128

var idPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// ValidID reports whether id is safe to accept from a client or imported
// bundle. Generated UUIDs and adapter IDs such as "codex-<sha256>" match.
// Quotes, angle brackets, whitespace, slashes, and control characters never do,
// so identifiers stay inert in HTML attributes, URLs, and terminal output.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

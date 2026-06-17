// Package dedup implements SPEC §8.3's semantic dedup signature
// and `// orion:ignore` annotation parser.
//
// The dedup signature is `sha256(pattern || normalized_call_site)`
// where `normalized_call_site` is a stable identifier for the
// affected callsite that survives file-rename and refactor. v1 keeps
// the canonicalization small: function name + receiver type. The
// file path is intentionally excluded so an issue's signature stays
// stable when the file is moved.
//
// The annotation parser walks a Go source file and extracts the
// three v1 forms (line-prior, file-head per-pattern, file-head all).
// Malformed annotations emit warnings and DO NOT suppress —
// "over-detect rather than silently honor a typo" per §8.3.
package dedup

import (
	"crypto/sha256"
	"encoding/hex"
)

// CanonicalCallSite is the file-path-independent identifier of a
// callsite. v1 captures function + receiver type. Future extensions
// could add a typed-AST hash if/when Orion has access to a parsed
// program.
type CanonicalCallSite struct {
	// Function is the bare function name (no package, no receiver).
	Function string

	// Receiver is the receiver type for methods, empty for free
	// functions. Distinguishes Server.Run() from a free Run().
	Receiver string
}

// Canonicalize builds a CanonicalCallSite from the raw inputs. The
// filePath is accepted for future expansion (e.g. package detection)
// but is NOT included in the canonical form.
func Canonicalize(filePath, function, receiver string) CanonicalCallSite {
	_ = filePath // intentionally dropped from canonical form (rename-invariance)
	return CanonicalCallSite{Function: function, Receiver: receiver}
}

// Signature returns the hex-encoded sha256 of pattern || canonical
// callsite. Returns an empty string when either input is empty —
// callers treat empty as "not determinable" and skip stamping.
func Signature(pattern string, cs CanonicalCallSite) string {
	if pattern == "" || cs.Function == "" {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(pattern))
	h.Write([]byte{0})
	h.Write([]byte(cs.Receiver))
	h.Write([]byte{0})
	h.Write([]byte(cs.Function))
	return hex.EncodeToString(h.Sum(nil))
}

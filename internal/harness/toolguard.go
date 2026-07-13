package harness

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Tool-output boundary guard (or-mvr.7, OWASP ASI01 / the inc-u12 parser-crash
// class): everything a tool returns passes through sanitizeToolResult before
// it can enter the conversation. One bad payload — a binary blob, a
// multi-megabyte dump, an invalid-UTF-8 sequence — must never crash the loop,
// blow the context window in one turn, or ride into the model unannounced.
// The injection HALF of tool-output defense (threat patterns over well-formed
// text) is or-ykz.17; this is the well-formedness half.

const (
	// maxToolResultBytes bounds a single tool result (~12K tokens). Bigger
	// outputs are elided head+tail — errors live at the end, context at the
	// start — with an explicit marker; disk outputs stay re-fetchable.
	maxToolResultBytes = 48 * 1024
	// binaryNulThreshold: any NUL byte marks binary; beyond that, if more than
	// this fraction of the sample fails UTF-8 decoding the payload is treated
	// as binary rather than repairable text.
	binaryInvalidFraction = 0.10
)

// sanitizeToolResult returns loop-safe text for any tool output, and whether
// the payload was quarantined outright (binary).
func sanitizeToolResult(content string) (string, bool) {
	invCount, invFrac := invalidStats(content)
	// Binary iff a NUL is present, or the sample is BOTH substantially and
	// proportionally invalid — a couple of stray bytes in short text is a
	// repair case, not a quarantine case.
	if strings.IndexByte(content, 0) >= 0 || (invCount >= 16 && invFrac > binaryInvalidFraction) {
		return fmt.Sprintf("[binary/non-text output withheld (%d bytes) — it cannot be read as conversation text; if this was a file, read it with a suitable tool or narrow the request]", len(content)), true
	}
	if !utf8.ValidString(content) {
		// Texty with stray bad bytes (a chopped stream, a mixed encoding):
		// repair rather than drop — the inc-u12 class crashed a parser here.
		content = strings.ToValidUTF8(content, "�")
	}
	if len(content) > maxToolResultBytes {
		half := maxToolResultBytes / 2
		head := validPrefix(content[:half])
		tail := validSuffix(content[len(content)-half:])
		return fmt.Sprintf("%s\n\n[...output truncated: %d of %d bytes shown (head+tail) — narrow the query or read the source from disk...]\n\n%s",
			head, len(head)+len(tail), len(content), tail), false
	}
	return content, false
}

// invalidStats samples the payload's UTF-8 validity (capped scan), returning
// the invalid-byte count and fraction over the sample.
func invalidStats(s string) (int, float64) {
	sample := s
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if len(sample) == 0 {
		return 0, 0
	}
	invalid := 0
	for i := 0; i < len(sample); {
		r, size := utf8.DecodeRuneInString(sample[i:])
		if r == utf8.RuneError && size == 1 {
			invalid++
		}
		i += size
	}
	return invalid, float64(invalid) / float64(len(sample))
}

// validPrefix/validSuffix trim a byte-sliced boundary back to a rune boundary
// so truncation never fabricates invalid UTF-8.
func validPrefix(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func validSuffix(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}

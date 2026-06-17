package patches

import (
	"fmt"
	"regexp"
	"strings"
)

// Grammar names the per-Pattern allowed shape for a unified diff.
// v1 is intentionally permissive: enforce the structural shape (file
// header, hunk header, +/- lines) rather than parse the changes
// semantically. The verifier (E1-7) is the actual gate; the grammar
// rejects only obvious garbage so we don't waste verifier cycles.
type Grammar struct {
	Pattern Pattern

	// AllowedNewIdentifiers, if non-empty, restricts the patch to
	// introducing only these new symbols on the +-side. Disabled in
	// v1 (empty) but the hook is in place for stricter grammars.
	AllowedNewIdentifiers []string

	// MaxAddedLines caps the patch size to keep the LLM honest. 0 means
	// no cap.
	MaxAddedLines int

	// RequiredHints are substrings that MUST appear at least once on
	// the +-side. v1 enforces a per-pattern hint ("WithTimeout", etc.)
	// so the LLM can't return an unrelated diff and pass.
	RequiredHints []string
}

// GrammarFor returns the v1 grammar for a Pattern.
func GrammarFor(p Pattern) Grammar {
	switch p {
	case PatternTimeout:
		return Grammar{
			Pattern:       p,
			MaxAddedLines: 40,
			RequiredHints: []string{"context.WithTimeout", "context.WithDeadline"},
		}
	case PatternRetry:
		return Grammar{
			Pattern:       p,
			MaxAddedLines: 60,
			RequiredHints: []string{"backoff", "Backoff", "retry", "Retry"},
		}
	case PatternIdempotency:
		return Grammar{
			Pattern:       p,
			MaxAddedLines: 80,
			RequiredHints: []string{"Idempotency-Key", "idempotency_key", "idempotencyKey"},
		}
	}
	return Grammar{Pattern: p}
}

var (
	// minimalDiffRe matches the file header pair that every unified
	// diff begins with.
	minimalDiffRe = regexp.MustCompile(`(?m)^--- .+\n\+\+\+ .+`)

	// hunkHeaderRe matches "@@ -A,B +C,D @@" hunks.
	hunkHeaderRe = regexp.MustCompile(`(?m)^@@ -\d+(,\d+)? \+\d+(,\d+)? @@`)
)

// Validate checks a unified diff against the per-pattern grammar.
// Returns nil on success or ErrInvalidDiff (wrapped with the reason)
// on rejection.
func (g Grammar) Validate(diff string) error {
	if !minimalDiffRe.MatchString(diff) {
		return fmt.Errorf("%w: missing unified diff header", ErrInvalidDiff)
	}
	if !hunkHeaderRe.MatchString(diff) {
		return fmt.Errorf("%w: missing hunk header", ErrInvalidDiff)
	}
	added := 0
	hintHit := len(g.RequiredHints) == 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if !strings.HasPrefix(line, "+") {
			continue
		}
		added++
		if !hintHit {
			for _, h := range g.RequiredHints {
				if strings.Contains(line, h) {
					hintHit = true
					break
				}
			}
		}
	}
	if g.MaxAddedLines > 0 && added > g.MaxAddedLines {
		return fmt.Errorf("%w: %d added lines exceeds cap %d", ErrInvalidDiff, added, g.MaxAddedLines)
	}
	if !hintHit {
		return fmt.Errorf("%w: pattern %q requires one of %v on +-side", ErrInvalidDiff, g.Pattern, g.RequiredHints)
	}
	return nil
}

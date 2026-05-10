package patches

import (
	"fmt"
	"regexp"
	"strings"
)

// ExtractDiffOptions controls parsing.
type ExtractDiffOptions struct {
	// Pattern is used to look up the per-pattern grammar.
	Pattern Pattern

	// ExpectedTargetPath, if non-empty, requires the diff's `+++ b/<path>`
	// header to match this path. Empty means accept any target path the
	// diff names; the caller can still verify downstream.
	ExpectedTargetPath string
}

var (
	// codeFenceRe captures content inside the FIRST fenced block of an
	// LLM response. Most responses arrive as ```diff\n...\n```.
	codeFenceRe = regexp.MustCompile("(?s)```(?:diff|patch)?\\s*\\n(.*?)\\n```")

	// targetPathRe extracts the +++ side path from a unified diff.
	targetPathRe = regexp.MustCompile(`(?m)^\+\+\+ (?:b/)?(.+)$`)
)

// Parse extracts a unified diff from raw LLM output, validates it
// against the per-pattern grammar, and returns a CandidatePatch
// populated with the parsed fields. Caller is responsible for
// stamping LLMModel, LLMSeed, and GeneratedAt.
func Parse(raw string, opts ExtractDiffOptions) (CandidatePatch, error) {
	diff := extractDiff(raw)
	if strings.TrimSpace(diff) == "" {
		return CandidatePatch{}, fmt.Errorf("%w: no diff body found in LLM output", ErrInvalidDiff)
	}
	g := GrammarFor(opts.Pattern)
	if err := g.Validate(diff); err != nil {
		return CandidatePatch{}, err
	}
	target := extractTargetPath(diff)
	if target == "" {
		return CandidatePatch{}, fmt.Errorf("%w: no +++ target path", ErrInvalidDiff)
	}
	if opts.ExpectedTargetPath != "" && target != opts.ExpectedTargetPath {
		return CandidatePatch{}, fmt.Errorf("%w: target %q does not match expected %q", ErrInvalidDiff, target, opts.ExpectedTargetPath)
	}
	rng := extractTargetRange(diff)
	return CandidatePatch{
		TargetPath:  target,
		TargetRange: rng,
		UnifiedDiff: diff,
		Pattern:     opts.Pattern,
	}, nil
}

// extractDiff strips a code fence wrapper if present; otherwise returns
// raw verbatim.
func extractDiff(raw string) string {
	if m := codeFenceRe.FindStringSubmatch(raw); len(m) >= 2 {
		return m[1]
	}
	return strings.TrimSpace(raw)
}

// extractTargetPath returns the target path from `+++ b/<path>` (or
// `+++ <path>` when the LLM omits the b/ prefix).
func extractTargetPath(diff string) string {
	m := targetPathRe.FindStringSubmatch(diff)
	if len(m) < 2 {
		return ""
	}
	p := strings.TrimSpace(m[1])
	// strip "\t<timestamp>" if present after the path (some diff tools)
	if idx := strings.Index(p, "\t"); idx > 0 {
		p = p[:idx]
	}
	return p
}

// extractTargetRange returns the [start, end] range from the FIRST hunk
// header. If multiple hunks exist, returns the union of the first one
// only; v1 generates one hunk per gap so this is a non-issue.
func extractTargetRange(diff string) [2]int {
	hunkRe := regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	m := hunkRe.FindStringSubmatch(diff)
	if len(m) < 2 {
		return [2]int{0, 0}
	}
	start := atoi(m[3])
	count := 1
	if m[4] != "" {
		count = atoi(m[4])
	}
	if start <= 0 {
		return [2]int{0, 0}
	}
	end := start + count - 1
	if end < start {
		end = start
	}
	return [2]int{start, end}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

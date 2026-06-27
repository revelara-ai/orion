// Package promptguard is a versioned prompt-injection threat-pattern library (or-mkb, split
// from or-ykz.17). It actively detects and neutralizes known injection patterns in UNTRUSTED
// content (generation-tier memory, tool results) — defense-in-depth ON TOP of the context
// engine's passive quarantine framing: even inside the "data only" block, a recognized
// injected instruction is redacted so it cannot be read as an instruction at all.
package promptguard

import "regexp"

// Version is the threat-pattern library version. Bump it whenever the pattern set changes so
// a downstream consumer can tell which library a stored neutralization was produced by.
const Version = "1"

// Scope tunes how aggressively content is scanned.
type Scope int

const (
	// ScopeContext is the conservative default: instruction-injection patterns only.
	ScopeContext Scope = iota
	// ScopeAll adds role-spoofing + exfiltration patterns (used for untrusted context/memory).
	ScopeAll
	// ScopeStrict adds aggressive jailbreak/override heuristics (higher false-positive risk).
	ScopeStrict
)

// redaction replaces a detected injection span in neutralized output.
const redaction = "[redacted: prompt-injection]"

type pattern struct {
	id       string
	re       *regexp.Regexp
	minScope Scope
}

// reCI compiles a case-insensitive pattern.
func reCI(s string) *regexp.Regexp { return regexp.MustCompile(`(?i)` + s) }

// patterns is the versioned library. Each requires a verb AND an object so benign prose that
// merely mentions "instructions" or "previous" does not match.
var patterns = []pattern{
	{"ignore-prior", reCI(`\b(?:ignore|disregard|forget)\b[^.\n]{0,40}\b(?:all\s+)?(?:prior|previous|above|earlier|preceding)\b[^.\n]{0,25}\b(?:instructions?|prompts?|context|rules?|directions?)\b`), ScopeContext},
	{"new-instructions", reCI(`\bnew\s+(?:instructions?|system\s+prompt|rules?)\s*:`), ScopeContext},
	{"you-are-now", reCI(`\byou\s+are\s+now\b[^.\n]{0,40}`), ScopeContext},
	{"reveal-secrets", reCI(`\b(?:reveal|print|show|expose|leak|dump|repeat)\b[^.\n]{0,40}\b(?:system\s+prompt|your\s+instructions?|api[_\s-]?key|secret|password|token|credentials?)\b`), ScopeContext},
	{"role-spoof", regexp.MustCompile(`(?im)^\s*(?:system|assistant|developer)\s*:`), ScopeAll},
	{"exfil", reCI(`\b(?:send|post|exfiltrate|upload|curl|wget)\b[^.\n]{0,60}https?://`), ScopeAll},
	{"override", reCI(`\b(?:bypass|jailbreak|do\s+anything\s+now|developer\s+mode\s+enabled)\b`), ScopeStrict},
}

// Match is one detected threat span.
type Match struct {
	Pattern string // pattern id
	Span    string // the matched substring
}

func active(scope Scope) []pattern {
	out := patterns[:0:0]
	for _, p := range patterns {
		if p.minScope <= scope {
			out = append(out, p)
		}
	}
	return out
}

// Detect returns the threat matches in s for the given scope without modifying s.
func Detect(s string, scope Scope) []Match {
	var ms []Match
	for _, p := range active(scope) {
		for _, span := range p.re.FindAllString(s, -1) {
			ms = append(ms, Match{Pattern: p.id, Span: span})
		}
	}
	return ms
}

// Neutralize redacts every in-scope threat span in s, returning the sanitized text and the
// redacted matches. Benign text is returned unchanged with no matches.
func Neutralize(s string, scope Scope) (string, []Match) {
	var ms []Match
	out := s
	for _, p := range active(scope) {
		spans := p.re.FindAllString(out, -1)
		if len(spans) == 0 {
			continue
		}
		for _, span := range spans {
			ms = append(ms, Match{Pattern: p.id, Span: span})
		}
		out = p.re.ReplaceAllString(out, redaction)
	}
	return out, ms
}

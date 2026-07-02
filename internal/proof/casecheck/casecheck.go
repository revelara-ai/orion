// Package casecheck is the SINGLE SOURCE of exec-case assertion semantics
// (or-v9f.3, Orion-Obligation-Vocabulary-Design §4.1). It is compiled into the
// harness (the empirical prober calls it directly) AND embedded verbatim into
// the behavioral corpus (testsynth ships it beside the generated tests), so the
// two proof channels can never drift on what an assertion MEANS. One
// implementation, two compilation contexts.
//
// HARD CONSTRAINT: stdlib-only and self-contained — no Orion imports, ever.
// The embedded copy must compile inside the generated artifact's module. A
// standalone-compile test enforces this.
package casecheck

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// OrionCheckExit reports whether the observed exit code satisfies the expected
// one, with a human-readable detail on mismatch.
func OrionCheckExit(want, got int) (bool, string) {
	if want == got {
		return true, ""
	}
	return false, fmt.Sprintf("exit: want %d, got %d", want, got)
}

// OrionCheckStream reports whether an output stream satisfies one assertion.
// kind is the closed StreamKind vocabulary; unknown kinds FAIL (never a silent
// pass — the or-y9d invariant holds inside the oracle too).
func OrionCheckStream(kind, value, key, got string) (bool, string) {
	switch kind {
	case "exact":
		if got == value {
			return true, ""
		}
		return false, fmt.Sprintf("stream exact: want %q, got %q", value, truncate(got))
	case "contains":
		if strings.Contains(got, value) {
			return true, ""
		}
		return false, fmt.Sprintf("stream contains: %q not found in %q", value, truncate(got))
	case "regex":
		re, err := regexp.Compile(value)
		if err != nil {
			return false, fmt.Sprintf("stream regex %q does not compile: %v", value, err)
		}
		if re.MatchString(got) {
			return true, ""
		}
		return false, fmt.Sprintf("stream regex %q did not match %q", value, truncate(got))
	case "empty":
		if strings.TrimSpace(got) == "" {
			return true, ""
		}
		return false, fmt.Sprintf("stream empty: got %q", truncate(got))
	case "rfc3339_utc":
		s := strings.TrimSpace(got)
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return false, fmt.Sprintf("stream rfc3339_utc: %q does not parse as RFC3339: %v", truncate(s), err)
		}
		if _, offset := ts.Zone(); offset != 0 {
			return false, fmt.Sprintf("stream rfc3339_utc: %q carries a non-UTC offset", truncate(s))
		}
		return true, ""
	}
	_ = key // reserved for json_key_* kinds (phase 2)
	return false, fmt.Sprintf("unknown stream assertion kind %q (oracle refuses, never passes)", kind)
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

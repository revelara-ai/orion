package conductor

import (
	"regexp"
	"sort"
	"strings"
)

// envCandidateRe extracts package-level build/setup failures — the marker of
// an ENVIRONMENTAL wall (missing deps, toolchain, sandbox capability) rather
// than a behavioral regression (CAST R5 fold on or-kf7o).
var envCandidateRe = regexp.MustCompile(`FAIL\s+(\S+)\s+\[(?:build|setup) failed\]`)

// failureFingerprint canonicalizes a failed attempt's identity (or-kf7o):
// the named delta failures + environmental-candidate packages + the first
// line of the reason. Post-newline detail is EXCLUDED — per-strike evidence
// (or-nos3) varies across attempts and must not defeat the invariance match.
// Sorted and deduplicated: ordering never changes the fingerprint. Empty for
// a committed result.
func failureFingerprint(res ChangeResult) string {
	if res.Committed {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	for _, f := range res.Regression.NewFailures {
		add("fail:" + f)
	}
	sources := []string{res.Reason, res.FailureDigest(), res.Regression.Before.Output, res.Regression.After.Output}
	if res.NewBehavior != nil {
		sources = append(sources, res.NewBehavior.Output)
	}
	for _, src := range sources {
		for _, m := range envCandidateRe.FindAllStringSubmatch(src, -1) {
			add("env:" + m[1])
		}
	}
	sort.Strings(parts)
	reason, _, _ := strings.Cut(res.Reason, "\n")
	reason = strings.TrimSpace(reason)
	if reason == "" && len(parts) == 0 {
		return ""
	}
	return reason + "||" + strings.Join(parts, "|")
}

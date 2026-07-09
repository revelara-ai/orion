package brownfield

import "strings"

// failureDigestMaxChars hard-bounds a digest: it rides in tool results and
// escalation records read by MODELS (often small local ones) — enough signal
// to self-correct, never 8K of build chatter.
const failureDigestMaxChars = 3000

// FailureDigest distills a failed test run's output to the lines a model (or
// human) needs to act: compiler errors, per-test failure anchors, panics, and
// package FAIL lines. When nothing matches (an exotic failure shape) it falls
// back to the output's tail — the end of a go test run is where verdicts live.
// The result is bounded to maxLines lines and failureDigestMaxChars chars.
// Empty output digests to "".
func FailureDigest(output string, maxLines int) string {
	output = strings.TrimSpace(output)
	if output == "" || maxLines <= 0 {
		return ""
	}
	lines := strings.Split(output, "\n")
	var keep []string
	for _, l := range lines {
		if failureSignal(l) {
			keep = append(keep, l)
			if len(keep) >= maxLines {
				break
			}
		}
	}
	if len(keep) == 0 { // no recognized signal: tail is the best generic evidence
		start := len(lines) - maxLines
		if start < 0 {
			start = 0
		}
		keep = lines[start:]
	}
	d := strings.Join(keep, "\n")
	if len(d) > failureDigestMaxChars {
		d = d[:failureDigestMaxChars] + "…"
	}
	return d
}

// failureSignal reports whether a go test output line carries actionable
// failure evidence: test-failure anchors and their detail lines, compiler
// errors (file:line:col), panics, and package verdict FAIL lines.
func failureSignal(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "--- FAIL"):
		return true
	case strings.HasPrefix(line, "FAIL"): // "FAIL\tpkg ..." and bare terminal FAIL
		return true
	case strings.HasPrefix(t, "panic:"):
		return true
	case strings.Contains(t, "undefined:"),
		strings.Contains(t, "cannot "),
		strings.Contains(t, "redeclared"),
		strings.Contains(t, "Error:"),
		strings.Contains(t, "error:"):
		return true
	case looksLikeFileLineCol(t):
		return true
	}
	return false
}

// looksLikeFileLineCol matches compiler/test detail lines of the form
// "path/file.go:12:34: message" or "file_test.go:12: message".
func looksLikeFileLineCol(s string) bool {
	i := strings.Index(s, ".go:")
	if i < 0 {
		return false
	}
	rest := s[i+4:]
	j := strings.IndexByte(rest, ':')
	if j <= 0 {
		return false
	}
	for _, r := range rest[:j] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

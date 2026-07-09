package brownfield

import (
	"strings"
	"testing"
)

func TestFailureDigestExtractsCompileErrors(t *testing.T) {
	out := `# github.com/x/pkg [github.com/x/pkg.test]
pkg/config_test.go:90:14: undefined: filepath
pkg/config_test.go:91:12: undefined: os
pkg/config_test.go:96:18: undefined: llm.LoadFile
lots of unrelated build chatter
more chatter
FAIL	github.com/x/pkg [build failed]
FAIL`
	d := FailureDigest(out, 10)
	for _, want := range []string{"undefined: filepath", "undefined: llm.LoadFile", "FAIL\tgithub.com/x/pkg"} {
		if !strings.Contains(d, want) {
			t.Errorf("digest missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "unrelated build chatter") {
		t.Errorf("digest must drop low-signal lines:\n%s", d)
	}
}

func TestFailureDigestExtractsTestFailures(t *testing.T) {
	out := `=== RUN   TestA
some log line
--- FAIL: TestA (0.01s)
    a_test.go:12: wanted 5 got 6
=== RUN   TestB
--- PASS: TestB (0.00s)
FAIL
FAIL	example.com/t	0.02s`
	d := FailureDigest(out, 10)
	if !strings.Contains(d, "--- FAIL: TestA") || !strings.Contains(d, "a_test.go:12") {
		t.Errorf("digest missing failure anchor lines:\n%s", d)
	}
	if strings.Contains(d, "--- PASS: TestB") {
		t.Errorf("digest must not include passing tests:\n%s", d)
	}
}

func TestFailureDigestFallsBackToTail(t *testing.T) {
	out := "line1\nline2\nline3\nline4"
	d := FailureDigest(out, 2)
	if !strings.Contains(d, "line3") || !strings.Contains(d, "line4") || strings.Contains(d, "line1") {
		t.Errorf("no-signal output must fall back to the tail:\n%s", d)
	}
}

func TestFailureDigestBounds(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("pkg/f_test.go:1:1: undefined: sym\n")
	}
	d := FailureDigest(b.String(), 40)
	if lines := strings.Count(d, "\n"); lines > 41 {
		t.Errorf("digest exceeded maxLines: %d lines", lines)
	}
	if len(d) > 3200 {
		t.Errorf("digest exceeded char bound: %d", len(d))
	}
	if FailureDigest("", 10) != "" {
		t.Error("empty output must digest to empty")
	}
}

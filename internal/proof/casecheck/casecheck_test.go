package casecheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestStreamSemanticsVectors: the golden semantics both channels inherit.
func TestStreamSemanticsVectors(t *testing.T) {
	cases := []struct {
		kind, value, got string
		want             bool
	}{
		{"exact", "ok", "ok", true},
		{"exact", "ok", "ok\n", false},
		{"contains", "Bogus", "unknown zone Bogus\n", true},
		{"contains", "Bogus", "fine", false},
		{"regex", `(?m)^\S+\.go:\d+`, "src/a.go:3 hardcoded secret\n", true},
		{"regex", `(?m)^\S+\.go:\d+`, "clean\n", false},
		{"empty", "", "  \n", true},
		{"empty", "", "x", false},
		{"rfc3339_utc", "", "2026-07-02T10:00:00Z\n", true},
		{"rfc3339_utc", "", "2026-07-02T10:00:00+02:00", false},
		{"rfc3339_utc", "", "not a time", false},
		{"sniff", "x", "x", false}, // unknown kind refuses, never passes
	}
	for _, tc := range cases {
		ok, detail := OrionCheckStream(tc.kind, tc.value, "", tc.got)
		if ok != tc.want {
			t.Errorf("OrionCheckStream(%q,%q,_,%q) = %v (%s), want %v", tc.kind, tc.value, tc.got, ok, detail, tc.want)
		}
		if !ok && detail == "" {
			t.Errorf("a failing check must explain itself: %+v", tc)
		}
	}
	if ok, _ := OrionCheckExit(3, 3); !ok {
		t.Error("matching exit must pass")
	}
	if ok, detail := OrionCheckExit(0, 2); ok || !strings.Contains(detail, "want 0, got 2") {
		t.Errorf("mismatched exit must fail with detail, got %v %q", ok, detail)
	}
}

// TestEmbeddedSourceCompilesStandalone: the embedded copy must compile in a
// bare module with NOTHING but the stdlib — the corpus-side compilation context.
func TestEmbeddedSourceCompilesStandalone(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module; skipped in -short")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module standalone\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := Source("main")
	if !strings.HasPrefix(strings.TrimSpace(stripComments(src)), "package main") {
		t.Fatalf("Source must rewrite the package clause:\n%s", src[:120])
	}
	if err := os.WriteFile(filepath.Join(dir, "check.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { _, _ = OrionCheckExit(0, 0) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("embedded oracle must compile standalone (stdlib-only):\n%s", out)
	}
}

func stripComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// TestOracleCoversSpecStreamKinds: every StreamKind the spec validator anchors
// must be understood by the oracle — the closed sets may never drift apart.
func TestOracleCoversSpecStreamKinds(t *testing.T) {
	samples := map[spec.StreamKind]struct{ value, passing string }{
		spec.StreamExact:      {"ok", "ok"},
		spec.StreamContains:   {"x", "axb"},
		spec.StreamRegex:      {"a+", "aaa"},
		spec.StreamEmpty:      {"", ""},
		spec.StreamRFC3339UTC: {"", "2026-07-02T00:00:00Z"},
	}
	for _, k := range spec.KnownStreamKinds() {
		s, ok := samples[k]
		if !ok {
			t.Fatalf("no oracle coverage sample for spec kind %q — add semantics to casecheck AND a sample here", k)
		}
		if pass, detail := OrionCheckStream(string(k), s.value, "", s.passing); !pass {
			t.Errorf("oracle rejects spec kind %q's passing sample: %s", k, detail)
		}
	}
}

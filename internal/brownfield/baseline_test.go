package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeRepo writes a minimal Go module with the given test body into a temp dir.
func writeRepo(t *testing.T, testBody string) string {
	t.Helper()
	dir := t.TempDir()
	must(t, filepath.Join(dir, "go.mod"), "module example.com/target\n\ngo 1.25\n")
	must(t, filepath.Join(dir, "lib.go"), "package target\n\nfunc Add(a, b int) int { return a + b }\n")
	must(t, filepath.Join(dir, "lib_test.go"), testBody)
	return dir
}

func must(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBaselineGreenAndRed: the baseline reflects the repo's actual test state —
// green when the suite passes, red when it fails.
func TestBaselineGreenAndRed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test on a temp module")
	}
	green := writeRepo(t, "package target\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,2)!=4 { t.Fatal(\"math\") } }\n")
	if r, err := Baseline(context.Background(), green); err != nil || !r.Detected || !r.Passed {
		t.Fatalf("green repo should yield a passing baseline: res=%+v err=%v", r, err)
	}

	red := writeRepo(t, "package target\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,2)!=5 { t.Fatal(\"expected failure\") } }\n")
	if r, err := Baseline(context.Background(), red); err != nil || !r.Detected || r.Passed {
		t.Fatalf("red repo should yield a failing baseline: res=%+v err=%v", r, err)
	}
}

// TestBaselineNoToolchain: a dir with no recognized toolchain is reported as
// "no baseline", not an error (the caller decides what to do).
func TestBaselineNoToolchain(t *testing.T) {
	r, err := Baseline(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Detected || r.Skipped == "" {
		t.Fatalf("a dir with no go.mod should report not-detected with a reason: %+v", r)
	}
}

// TestBaselineDoesNotLeakHostSecrets: the SECURITY boundary. The target repo's test
// asserts ANTHROPIC_API_KEY is absent and panics if present. We set the key in the
// PARENT env, then run the baseline — it must still pass, proving safeenv scrubbed
// the host secret before the untrusted repo code ran. If safeenv regressed and the
// host env leaked, the repo test would panic and the baseline would be RED.
func TestBaselineDoesNotLeakHostSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test on a temp module")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-MUST-NOT-LEAK")
	repo := writeRepo(t, "package target\nimport (\n\t\"os\"\n\t\"testing\"\n)\nfunc TestNoSecret(t *testing.T){ if os.Getenv(\"ANTHROPIC_API_KEY\") != \"\" { panic(\"HOST SECRET LEAKED INTO UNTRUSTED REPO TESTS\") } }\n")

	r, err := Baseline(context.Background(), repo)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if !r.Passed {
		t.Fatalf("the host API key leaked into the target repo's test env (safeenv breach):\n%s", r.Output)
	}
}

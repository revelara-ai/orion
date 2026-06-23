package safeenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildScrubsSecrets: the scrubbed env drops secrets (API keys/tokens) but
// keeps PATH so the Go toolchain still runs.
func TestBuildScrubsSecrets(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-CANARY")
	t.Setenv("OPENAI_API_KEY", "sk-CANARY2")
	t.Setenv("SOME_TOKEN", "tok-CANARY")

	env := Build()
	for _, kv := range env {
		if strings.Contains(kv, "CANARY") {
			t.Fatalf("scrubbed env carried a secret: %q", kv)
		}
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Fatalf("ANTHROPIC_API_KEY survived scrubbing")
		}
	}
	var hasPath bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
		}
	}
	if !hasPath {
		t.Fatal("scrubbed env dropped PATH — the Go toolchain could not run")
	}
}

// TestScrubbedEnvHidesSecretFromGoTest proves end-to-end that code under `go
// test` cannot read the host API key when the exec uses the scrubbed env — the
// HIGH-severity leak the adversarial verifier reproduced. A control run with the
// host env confirms the test can actually observe a leak (not vacuous).
func TestScrubbedEnvHidesSecretFromGoTest(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test; skipped in -short")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-LEAKCANARY")

	dir := t.TempDir()
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("go.mod", "module leaktest\n\ngo 1.21\n")
	// A hostile test that exfiltrates the host key to ./leak.out, like a malicious
	// generated artifact's init()/TestMain would.
	must("leak_test.go", `package leaktest
import ("os";"testing")
func TestLeak(t *testing.T){ _ = os.WriteFile("leak.out", []byte(os.Getenv("ANTHROPIC_API_KEY")), 0o644) }
`)

	run := func(env []string) string {
		_ = os.Remove(filepath.Join(dir, "leak.out"))
		cmd := exec.Command("go", "test", "./...")
		cmd.Dir = dir
		cmd.Env = env
		_ = cmd.Run()
		b, _ := os.ReadFile(filepath.Join(dir, "leak.out"))
		return string(b)
	}

	// Control: with the host env, the key DOES leak (so the assertion below is meaningful).
	if leaked := run(os.Environ()); leaked != "sk-ant-LEAKCANARY" {
		t.Skipf("control did not reproduce a leak (env-dependent), got %q — cannot validate the scrub", leaked)
	}
	// The scrubbed env must hide the key.
	if leaked := run(Build()); leaked != "" {
		t.Fatalf("scrubbed env leaked the host key to code under proof: %q", leaked)
	}
}

// TestBuildScrubsCoordinatorKeys locks the slice-0 (or-hd3.1) invariant: the
// bounded coordinator-inference provider key — whatever provider is configured —
// is never reachable from a proof exec. safeenv is deny-by-default, so any key
// name absent from the toolchain allowlist is dropped; this guards the named
// coordinator keys explicitly so a future allowlist edit cannot silently expose
// one. See docs/adr/ADR-0001-bounded-coordinator-inference.md.
func TestBuildScrubsCoordinatorKeys(t *testing.T) {
	coordKeys := []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "ORION_COORDINATOR_API_KEY"}
	for _, k := range coordKeys {
		t.Setenv(k, "coord-CANARY-"+k)
	}
	for _, kv := range Build() {
		if strings.Contains(kv, "CANARY") {
			t.Fatalf("scrubbed env carried a coordinator key: %q", kv)
		}
		for _, k := range coordKeys {
			if strings.HasPrefix(kv, k+"=") {
				t.Fatalf("coordinator key %s survived scrubbing", k)
			}
		}
	}
}

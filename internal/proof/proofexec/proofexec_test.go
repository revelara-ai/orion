package proofexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/sandbox"
)

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// requireBwrap skips when no namespace sandbox is available, so the isolation
// assertions only run where they can actually hold (CI without bwrap falls back
// to the "none" backend, which by design provides no isolation).
func requireBwrap(t *testing.T) {
	t.Helper()
	if _, err := sandbox.New("bwrap"); err != nil {
		t.Skipf("bwrap backend unavailable: %v", err)
	}
}

// A no-dep module whose test passes must still run GREEN through the sandboxed
// toolchain (isolation must not break legitimate proof execs).
func TestGoToolchainRunsTestsGreen(t *testing.T) {
	requireBwrap(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module probe\ngo 1.25\n",
		"m.go":      "package probe\nfunc Add(a, b int) int { return a + b }\n",
		"m_test.go": "package probe\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,2)!=4 { t.Fatal(\"math\") } }\n",
	})
	out, code, err := GoToolchain(context.Background(), dir, "test", "./...")
	if err != nil {
		t.Fatalf("GoToolchain error: %v\n%s", err, out)
	}
	if code != 0 {
		t.Fatalf("expected pass (exit 0), got exit %d:\n%s", code, out)
	}
}

// The security property this issue exists for: untrusted code under proof CANNOT
// reach the network (no exfiltration of anything it reads during build/test).
func TestGoToolchainDeniesNetworkEgress(t *testing.T) {
	requireBwrap(t)
	dir := writeModule(t, map[string]string{
		"go.mod": "module probe\ngo 1.25\n",
		"m.go":   "package probe\n",
		"m_test.go": `package probe
import ("testing";"net";"time")
func TestNet(t *testing.T){
  c,e:=net.DialTimeout("tcp","1.1.1.1:53",1500*time.Millisecond)
  if e==nil{ c.Close(); t.Log("ORION_NET=OPEN") } else { t.Log("ORION_NET=DENIED") }
}
`,
	})
	out, _, err := GoToolchain(context.Background(), dir, "test", "-v", "./...")
	if err != nil {
		t.Fatalf("GoToolchain error: %v\n%s", err, out)
	}
	if strings.Contains(out, "ORION_NET=OPEN") || !strings.Contains(out, "ORION_NET=DENIED") {
		t.Fatalf("network egress was NOT denied to code under proof:\n%s", out)
	}
}

// Host secrets in Orion's own environment (notably the model API key) must never
// reach generated code during proof.
func TestGoToolchainScrubsHostSecretEnv(t *testing.T) {
	requireBwrap(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret-sentinel-xyz")
	dir := writeModule(t, map[string]string{
		"go.mod": "module probe\ngo 1.25\n",
		"m.go":   "package probe\n",
		"m_test.go": `package probe
import ("os";"testing")
func TestSecret(t *testing.T){
  if v:=os.Getenv("ANTHROPIC_API_KEY"); v!="" { t.Logf("ORION_SECRET=LEAKED:%s", v) } else { t.Log("ORION_SECRET=ABSENT") }
}
`,
	})
	out, _, err := GoToolchain(context.Background(), dir, "test", "-v", "./...")
	if err != nil {
		t.Fatalf("GoToolchain error: %v\n%s", err, out)
	}
	if strings.Contains(out, "ORION_SECRET=LEAKED") {
		t.Fatalf("host secret leaked into code under proof:\n%s", out)
	}
}

// TestGoArmFailsClosedWithoutSandbox (or-tf8 H1): generated code never runs
// without a namespace sandbox unless the operator EXPLICITLY accepts it.
func TestGoArmFailsClosedWithoutSandbox(t *testing.T) {
	t.Setenv("ORION_SANDBOX_ISOLATION", "none")
	t.Setenv("ORION_ALLOW_UNSAFE_GO_ARM", "")
	dir := t.TempDir()
	_, _, _, err := RunTool(context.Background(), dir, "go", "go", "version")
	if err == nil || !strings.Contains(err.Error(), "refusing to run generated code without a namespace sandbox") {
		t.Fatalf("the none backend must fail closed for the go arm: %v", err)
	}

	// The explicit operator override runs (with the warning, not silently).
	t.Setenv("ORION_ALLOW_UNSAFE_GO_ARM", "1")
	out, _, code, err := RunTool(context.Background(), dir, "go", "go", "version")
	if err != nil || code != 0 || !strings.Contains(out, "go version") {
		t.Fatalf("the explicit override must run: %v code=%d out=%q", err, code, out)
	}
}

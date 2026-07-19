package proofexec

import (
	"context"
	"strings"
	"testing"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := (pyToolchain{}).ResolveBin("", "python3"); err != nil {
		t.Skip("no python3 on host")
	}
}

// TestPyToolchainPolicy (or-4y7.9): the python proof toolchain's policy surface —
// only python3 runs, installer/env modules are denied, the env is hermetic, and
// there is NO unisolated-backend override (stricter than the Go arm).
func TestPyToolchainPolicy(t *testing.T) {
	tc := toolchainFor("python")
	if tc == nil || tc.Language() != "python" {
		t.Fatal("the python toolchain must be registered under 'python'")
	}
	for _, ok := range [][]string{
		{"-m", "unittest", "-v"},
		{"-m", "py_compile", "main.py"},
		{"-m", "pytest", "-q"},
		{"test_probe.py"},
	} {
		if err := tc.Allow("python3", ok); err != nil {
			t.Fatalf("`python3 %s` must be allowed: %v", strings.Join(ok, " "), err)
		}
	}
	if err := tc.Allow("bash", nil); err == nil {
		t.Fatal("a non-python tool must be refused")
	}
	for _, mod := range []string{"pip", "ensurepip", "venv"} {
		if err := tc.Allow("python3", []string{"-m", mod, "install", "x"}); err == nil {
			t.Errorf("`python3 -m %s` must be denied under proof", mod)
		}
	}
	if !tc.IsPrimary("python3") {
		t.Error("python3 is the primary toolchain binary")
	}
	if tc.UnsafeNoneOverride() {
		t.Error("python must have NO unisolated-backend override — it executes generated code on every invocation")
	}
	env := tc.Env("/tmp/wd")
	for k, want := range map[string]string{
		"PYTHONDONTWRITEBYTECODE": "1", "PYTHONNOUSERSITE": "1",
		"PYTHONHASHSEED": "0", "PIP_NO_INDEX": "1",
	} {
		if env[k] != want {
			t.Errorf("env[%q] = %q, want %q", k, env[k], want)
		}
	}
	if _, ok := env["PYTHONPATH"]; ok {
		t.Error("PYTHONPATH must never leak into proof")
	}
}

// TestPyToolchainRefusesNoneEvenWithGoOverride (or-4y7.9): under the unisolated
// none backend, python REFUSES even with the Go arm's operator override set —
// there is no unsandboxed Python proof, ever.
func TestPyToolchainRefusesNoneEvenWithGoOverride(t *testing.T) {
	requirePython(t)
	t.Setenv("ORION_SANDBOX_ISOLATION", "none")
	t.Setenv("ORION_ALLOW_UNSAFE_GO_ARM", "1")
	_, _, _, err := RunTool(context.Background(), t.TempDir(), "python", "python3", "-m", "unittest")
	if err == nil || !strings.Contains(err.Error(), "namespace sandbox") {
		t.Fatalf("python under 'none' must refuse even with the override, got %v", err)
	}
}

// TestPyToolchainUnittestSandboxed (or-4y7.9): a real stdlib-unittest run inside
// the namespace sandbox — proving the resolved interpreter, its usr-merge loader
// links, and the hermetic env actually work with ZERO third-party dependencies —
// and the obligation marker survives on stdout.
func TestPyToolchainUnittestSandboxed(t *testing.T) {
	requireBwrap(t)
	requirePython(t)
	dir := t.TempDir()
	mustWrite(t, dir+"/test_orion_probe.py", `
import unittest

class TestProbe(unittest.TestCase):
    def test_green(self):
        print("ORION_OBLIGATION_PASS:probe1")
        self.assertEqual(1 + 1, 2)

if __name__ == "__main__":
    unittest.main(verbosity=2)
`)
	stdout, stderr, exit, err := RunTool(context.Background(), dir, "python", "python3", "test_orion_probe.py")
	if err != nil {
		t.Fatalf("unittest under sandbox failed to launch: %v\n%s%s", err, stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("green unittest must exit 0, got %d\n%s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "ORION_OBLIGATION_PASS:probe1") {
		t.Fatalf("the obligation marker must survive on stdout:\n%s\n%s", stdout, stderr)
	}
}

// TestPyToolchainDeniesNetworkEgress (or-4y7.9): the sandboxed python cannot
// reach the network — a connect attempt RAISES inside the jail (the test passes
// exactly because egress is denied).
func TestPyToolchainDeniesNetworkEgress(t *testing.T) {
	requireBwrap(t)
	requirePython(t)
	dir := t.TempDir()
	mustWrite(t, dir+"/test_orion_net.py", `
import socket
import unittest

class TestNet(unittest.TestCase):
    def test_egress_denied(self):
        with self.assertRaises(OSError):
            socket.create_connection(("1.1.1.1", 443), timeout=3)

if __name__ == "__main__":
    unittest.main()
`)
	stdout, stderr, exit, err := RunTool(context.Background(), dir, "python", "python3", "test_orion_net.py")
	if err != nil {
		t.Fatalf("launch: %v\n%s%s", err, stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("egress must be denied inside the sandbox (the connect must raise):\n%s%s", stdout, stderr)
	}
}

// TestPyToolchainCannotReadHostHome (or-4y7.9): the jail sees no host filesystem
// beyond the workdir + toolchain roots — the user's real HOME (and any secrets in
// it) does not exist inside.
func TestPyToolchainCannotReadHostHome(t *testing.T) {
	requireBwrap(t)
	requirePython(t)
	dir := t.TempDir()
	mustWrite(t, dir+"/test_orion_fs.py", `
import os
import unittest

class TestFS(unittest.TestCase):
    def test_host_home_invisible(self):
        self.assertFalse(os.path.exists("/home/josebiro/.orion"))
        self.assertFalse(os.path.exists("/home/josebiro/.ssh"))

if __name__ == "__main__":
    unittest.main()
`)
	stdout, stderr, exit, err := RunTool(context.Background(), dir, "python", "python3", "test_orion_fs.py")
	if err != nil {
		t.Fatalf("launch: %v\n%s%s", err, stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("the host HOME must be invisible inside the jail:\n%s%s", stdout, stderr)
	}
}

package empirical

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestMain doubles as the probe-verb shim (or-6lm): when re-exec'd inside the
// sandbox as `<test-binary> __empirical-probe`, it IS the verb — no separate
// orion build needed to prove the co-located path.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__empirical-probe" {
		os.Exit(ProbeVerbMain(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

// The or-6lm done-when, both halves in one witness: the fixture service's
// handler ATTEMPTS egress to a live host-side listener and reports what
// happened — 200 "egress denied" only if the dial failed. A passing probe
// therefore proves loopback WORKS (the answer arrived) and egress is DENIED
// (the answer says so). The host listener stands in for "the internet": with
// --unshare-net both are equally unreachable, and unlike a real external
// endpoint it is guaranteed reachable from the host netns (asserted below),
// so a surviving AllowNet mutant cannot hide behind an offline machine.
func TestColocEgressDeniedLoopbackWorks(t *testing.T) {
	be, err := sandbox.New("auto")
	if err != nil || be.Name() != "bwrap" {
		t.Skip("bwrap unavailable — co-located sandbox path untestable here")
	}

	// The egress witness target: live in the HOST netns.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	target := ln.Addr().String()
	if c, derr := net.DialTimeout("tcp", target, time.Second); derr != nil {
		t.Fatalf("sanity: egress target must be reachable from the host netns: %v", derr)
	} else {
		c.Close()
	}

	binDir := t.TempDir()
	src := filepath.Join(binDir, "main.go")
	svc := fmt.Sprintf(`package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if c, err := net.DialTimeout("tcp", %q, 1500*time.Millisecond); err == nil {
			c.Close()
			w.WriteHeader(500)
			fmt.Fprint(w, "egress allowed")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "egress denied")
	})
	if err := http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`, target)
	if err := os.WriteFile(src, []byte(svc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "go.mod"), []byte("module egressfixture\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "svc")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = binDir
	build.Env = append(os.Environ(), "CGO_ENABLED=0") // static: the jail has no libc unless loader dirs exist
	if out, berr := build.CombinedOutput(); berr != nil {
		t.Fatalf("fixture build: %v\n%s", berr, out)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORION_EMPIRICAL_PROBE_BIN", self) // the TestMain shim above

	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	prs, ok := colocProbe(context.Background(), binDir, bin, port, 2, testsynth.Contract{Route: "/", Format: "text"})
	if !ok {
		t.Fatal("colocProbe fell back to the bare path — expected a sandboxed co-located run under bwrap")
	}
	if len(prs) != 2 {
		t.Fatalf("rounds: got %d results, want 2", len(prs))
	}
	for i, pr := range prs {
		if !pr.PortOpen {
			t.Fatalf("round %d: loopback probe never reached the service inside the netns: %s", i, pr.Detail)
		}
		if !pr.ResponseContractSatisfied {
			t.Fatalf("round %d: service reported egress was NOT denied (or contract mismatch): %s", i, pr.Detail)
		}
	}
}

// Bare fallback: with no orion-named executable and no override, the
// co-located path must decline (ok=false) so Prove keeps today's host run.
func TestColocFallsBackWithoutProbeBin(t *testing.T) {
	t.Setenv("ORION_EMPIRICAL_PROBE_BIN", "")
	if _, ok := colocProbe(context.Background(), t.TempDir(), "/nonexistent", 1, 1, testsynth.Contract{}); ok {
		t.Fatal("colocProbe claimed a sandboxed run with no probe binary available")
	}
}

// Kill switch: ORION_EMPIRICAL_SANDBOX=off forces the bare path even when a
// probe binary is available.
func TestColocKillSwitch(t *testing.T) {
	self, _ := os.Executable()
	t.Setenv("ORION_EMPIRICAL_PROBE_BIN", self)
	t.Setenv("ORION_EMPIRICAL_SANDBOX", "off")
	if _, ok := colocProbe(context.Background(), t.TempDir(), "/nonexistent", 1, 1, testsynth.Contract{}); ok {
		t.Fatal("ORION_EMPIRICAL_SANDBOX=off did not force the bare fallback")
	}
}

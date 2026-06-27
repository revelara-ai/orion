package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
)

// checkStatus is a doctor check outcome. Only fail flips the exit code; warn is advisory
// (a degraded-but-functional component, e.g. no sandbox backend → safeenv-only isolation).
type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

type doctorCheck struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
}

// cmdDoctor implements `orion doctor [--fix] [--json]`: report harness component health and,
// with --fix, repair known faults (currently a missing data directory). Exit is non-zero if
// any check FAILs (warn does not fail), so an operator or CI can gate on it — the 3 a.m. test
// applied to Orion itself (or-ykz.18).
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fix := fs.Bool("fix", false, "attempt to repair known faults (e.g. create a missing data dir)")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, _ := doctorDataDir() // "" if unresolved; the data-dir check reports that as a fail
	checks := doctorChecks(dir, exec.LookPath, os.Getenv("ORION_AGENT"), *fix)
	if *asJSON {
		return emitJSON(checks)
	}
	failed := 0
	for _, c := range checks {
		fmt.Printf("[%-4s] %-16s %s\n", c.Status, c.Name, c.Detail)
		if c.Status == statusFail {
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("doctor: %d check(s) FAILED\n", failed)
		return 1
	}
	fmt.Println("doctor: all checks passed")
	return 0
}

// doctorDataDir resolves the data dir WITHOUT creating it (resolveDataDir creates it as a
// side effect, which would mask the very fault --fix repairs).
func doctorDataDir() (string, error) {
	if d := os.Getenv("ORION_DATA_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".orion"), nil
}

// doctorChecks runs the component-health probes. lookPath + agentEnv are injected so the
// checks are testable without touching the real PATH or environment.
func doctorChecks(dir string, lookPath func(string) (string, error), agentEnv string, fix bool) []doctorCheck {
	var out []doctorCheck

	// 1. Data directory: present, or (with --fix) created. A missing data dir is the one
	//    known fault --fix repairs.
	switch {
	case dir == "":
		out = append(out, doctorCheck{"data-dir", statusFail, "could not resolve data dir (no $ORION_DATA_DIR and no home dir)"})
	case dirExists(dir):
		out = append(out, doctorCheck{"data-dir", statusOK, dir})
	case fix:
		if err := os.MkdirAll(dir, 0o700); err == nil {
			out = append(out, doctorCheck{"data-dir", statusOK, "created " + dir})
		} else {
			out = append(out, doctorCheck{"data-dir", statusFail, "missing and could not create: " + err.Error()})
		}
	default:
		out = append(out, doctorCheck{"data-dir", statusFail, "missing: " + dir + " (run `orion doctor --fix` to create)"})
	}

	// 2/3. Stores open only if the data dir now exists.
	if dirExists(dir) {
		if st, err := contextstore.Open(dir); err == nil {
			_ = st.Close()
			out = append(out, doctorCheck{"context-store", statusOK, "openable"})
		} else {
			out = append(out, doctorCheck{"context-store", statusFail, err.Error()})
		}
		memDir := filepath.Join(dir, "memory")
		if err := os.MkdirAll(memDir, 0o700); err != nil {
			out = append(out, doctorCheck{"memory-store", statusFail, "memory dir: " + err.Error()})
		} else if m, err := memory.Open(memDir); err == nil {
			_ = m.Close()
			out = append(out, doctorCheck{"memory-store", statusOK, "openable"})
		} else {
			out = append(out, doctorCheck{"memory-store", statusFail, err.Error()})
		}
	}

	// 4. Sandbox backend: proof execs isolate generated code in bwrap; absent → safeenv-only
	//    (env-scrubbed but not namespaced), so warn rather than fail.
	if _, err := lookPath("bwrap"); err == nil {
		out = append(out, doctorCheck{"sandbox-backend", statusOK, "bwrap available"})
	} else {
		out = append(out, doctorCheck{"sandbox-backend", statusWarn, "bwrap not found; proof execs fall back to safeenv-only isolation"})
	}

	// 5. Agent preset (generation backend).
	out = append(out, doctorAgentCheck(agentEnv, lookPath))
	return out
}

// doctorAgentCheck mirrors resolveAgent (run.go): unset/none/fixture is healthy (the
// deterministic generator); a set-but-unknown preset is a fault; a known-but-not-on-PATH
// preset is a warn (Orion silently falls back to the fixture).
func doctorAgentCheck(agentEnv string, lookPath func(string) (string, error)) doctorCheck {
	name := strings.TrimSpace(agentEnv)
	if name == "" || strings.EqualFold(name, "none") || strings.EqualFold(name, "fixture") {
		return doctorCheck{"agent-preset", statusOK, "fixture generator (no vendor agent configured)"}
	}
	p, ok := agentruntime.DefaultPresetRegistry().Get(name)
	if !ok {
		return doctorCheck{"agent-preset", statusFail, "ORION_AGENT=" + name + " is not a known preset"}
	}
	if _, err := lookPath(p.Command); err != nil {
		return doctorCheck{"agent-preset", statusWarn, "ORION_AGENT=" + name + " set but '" + p.Command + "' not on PATH (falls back to fixture)"}
	}
	return doctorCheck{"agent-preset", statusOK, name + " (" + p.Command + ")"}
}

func dirExists(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

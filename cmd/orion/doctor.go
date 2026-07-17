package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/term"

	"github.com/revelara-ai/orion/internal/harnessconfig"
	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/preflight"
)

// checkStatus mirrors health.Status for doctor's flat line output + JSON. Only fail flips the
// exit code; warn is advisory (a degraded-but-functional component).
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

// cmdDoctor implements `orion doctor [--fix] [--json]`: report harness component health (from
// the shared internal/health source) and, with --fix, repair known faults (currently a missing
// data directory). Exit is non-zero if any check FAILs (warn does not fail), so an operator or
// CI can gate on it — the 3 a.m. test applied to Orion itself (or-ykz.18, or-gik.1).
func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fix := fs.Bool("fix", false, "attempt to repair known faults (e.g. create a missing data dir, offer missing-tool installs)")
	yes := fs.Bool("yes", false, "with --fix: install missing tools without prompting (CI)")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, _ := doctorDataDir() // "" if unresolved; the data-dir check reports that as a fail
	if *fix {
		// or-f96q: --fix also offers the missing-tool installs the startup
		// preflight would (prompted on a TTY; --yes for headless CI), before the
		// probe below so a fresh install reports green.
		prefsPath := ""
		if dir != "" {
			prefsPath = filepath.Join(dir, "toolprefs.json")
		}
		preflight.Run(preflight.Options{
			IsTTY:     term.IsTerminal(int(os.Stdin.Fd())),
			In:        os.Stdin,
			Out:       os.Stderr,
			Runner:    preflight.ExecRunner,
			PrefsPath: prefsPath,
			AssumeYes: *yes,
		})
	}
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

// doctorChecks runs the shared health probes (internal/health) and flattens the grouped Report
// into doctor's flat list. With --fix it first repairs the one known fault — a missing data
// dir — so the subsequent read-only probe reports it healthy. lookPath + agentEnv are injected
// for testability.
func doctorChecks(dir string, lookPath func(string) (string, error), agentEnv string, fix bool) []doctorCheck {
	if fix && dir != "" && !health.DirExists(dir) {
		_ = os.MkdirAll(dir, 0o700)
	}
	rep := health.Probe(health.Options{
		DataDir:  dir,
		LookPath: lookPath,
		AgentEnv: agentEnv,
		Polaris:  cachedPolarisCheck,
	})
	out := make([]doctorCheck, 0, len(rep.Pipeline)+len(rep.Subsystems))
	for _, c := range rep.All() {
		out = append(out, doctorCheck{Name: c.Name, Status: checkStatus(c.Status), Detail: c.Detail})
	}
	// or-kzf.2: validate the externalized harness config (prompts/checklists).
	// Absent files are fine (compiled defaults); an INVALID file is a FAIL here
	// even though runtime degrades to defaults — the whole point is that a bad
	// edit is caught in review/doctor, not discovered as silent drift.
	if verrs := harnessconfig.Validate(); len(verrs) > 0 {
		for _, e := range verrs {
			out = append(out, doctorCheck{Name: "harness-config", Status: statusFail, Detail: e.Error()})
		}
	} else {
		out = append(out, doctorCheck{Name: "harness-config", Status: statusOK, Detail: "externalized config valid (or absent → compiled defaults)"})
	}
	// or-c6zf.5: semantic-recall provisioning state (opt-in feature).
	out = append(out, embedderCheck(dir))
	return out
}

// cachedPolarisCheck reports Polaris cached-credential presence WITHOUT a network call — the
// live reachability probe belongs to `orion status` (or-gik.4). Never panics.
func cachedPolarisCheck() health.Check {
	dir, err := credentialsDir()
	if err != nil {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: "no credentials dir: " + err.Error()}
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: err.Error()}
	}
	tok, ok, err := store.Load()
	if err != nil || !ok {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: "not logged in"}
	}
	return health.Check{Name: "revelara.ai", Status: health.OK, Detail: "cached credential for " + tok.BaseURL}
}

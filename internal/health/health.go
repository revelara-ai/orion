// Package health is the single source of truth for Orion's component readiness. Both `orion
// doctor` and the init status banner (or-gik) consume the same grouped Report, so there is no
// drift between two implementations of "is bwrap present".
//
// Probes are cheap, local, and never panic or make network calls — anything external (a live
// Polaris reachability check) is INJECTED by the caller via Options.Polaris, so the TUI launch
// path can stay network-free (cached-credential presence) while `orion status` does a live
// probe. A probe that errors yields a warn/fail row carrying the reason; it never panics.
package health

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
)

// Status is a readiness outcome. Only Fail is a hard failure; Warn is degraded-but-functional.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Check is one readiness probe result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Report groups checks the way the banner renders them: Pipeline (the generate→prove→deliver
// gates) and Subsystems (the operational dependencies).
type Report struct {
	Pipeline   []Check `json:"pipeline"`
	Subsystems []Check `json:"subsystems"`
}

// All returns the checks flattened, Pipeline first.
func (r Report) All() []Check {
	out := make([]Check, 0, len(r.Pipeline)+len(r.Subsystems))
	out = append(out, r.Pipeline...)
	out = append(out, r.Subsystems...)
	return out
}

// Summary counts ok/warn/fail across all checks (for the banner's "N/M ready" line).
func (r Report) Summary() (ok, warn, fail int) {
	for _, c := range r.All() {
		switch c.Status {
		case OK:
			ok++
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}
	return ok, warn, fail
}

// Options injects the externals so probes stay testable + side-effect-scoped.
type Options struct {
	DataDir  string                       // the resolved data dir (may be "" / missing)
	LookPath func(string) (string, error) // PATH lookup (defaults to exec.LookPath)
	AgentEnv string                       // $ORION_AGENT
	Polaris  func() Check                 // optional: caller-provided polaris probe (cached or live)
}

// Probe runs every readiness probe and returns the grouped Report. Read-only (it does not
// repair anything — `orion doctor --fix` handles repair before calling Probe).
func Probe(opts Options) Report {
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var r Report

	// Pipeline: the generate→prove→deliver gates. These are code-level capabilities — present
	// in a built conductor — except the lsp gate, which depends on gopls being installed.
	r.Pipeline = append(r.Pipeline,
		Check{"intent→spec", OK, "generate→spec flow wired"},
		Check{"completeness gate", OK, "clarify analyzer present"},
		Check{"proof harness", OK, "behavioral · empirical · hazard"},
		lspCheck(lookPath),
	)

	// Subsystems: the operational dependencies.
	r.Subsystems = append(r.Subsystems, dataDirCheck(opts.DataDir))
	if DirExists(opts.DataDir) {
		r.Subsystems = append(r.Subsystems, contextStoreCheck(opts.DataDir), memoryCheck(opts.DataDir))
	}
	r.Subsystems = append(r.Subsystems, sandboxCheck(lookPath), trackerCheck(lookPath))
	if opts.Polaris != nil {
		r.Subsystems = append(r.Subsystems, opts.Polaris())
	}
	r.Subsystems = append(r.Subsystems, agentCheck(opts.AgentEnv, lookPath))
	return r
}

func dataDirCheck(dir string) Check {
	switch {
	case dir == "":
		return Check{"data-dir", Fail, "could not resolve data dir (no $ORION_DATA_DIR and no home dir)"}
	case DirExists(dir):
		return Check{"data-dir", OK, dir}
	default:
		return Check{"data-dir", Fail, "missing: " + dir + " (run `orion doctor --fix` to create)"}
	}
}

func contextStoreCheck(dir string) Check {
	st, err := contextstore.Open(dir)
	if err != nil {
		return Check{"context-store", Fail, err.Error()}
	}
	_ = st.Close()
	return Check{"context-store", OK, "openable"}
}

func memoryCheck(dir string) Check {
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		return Check{"memory-store", Fail, "memory dir: " + err.Error()}
	}
	m, err := memory.Open(memDir)
	if err != nil {
		return Check{"memory-store", Fail, err.Error()}
	}
	_ = m.Close()
	return Check{"memory-store", OK, "openable"}
}

func sandboxCheck(lookPath func(string) (string, error)) Check {
	if _, err := lookPath("bwrap"); err == nil {
		return Check{"sandbox-backend", OK, "bwrap available"}
	}
	return Check{"sandbox-backend", Warn, "bwrap not found; proof execs fall back to safeenv-only isolation"}
}

func trackerCheck(lookPath func(string) (string, error)) Check {
	if _, err := lookPath("bd"); err == nil {
		return Check{"tracker", OK, "beads (bd) available"}
	}
	return Check{"tracker", Warn, "bd not found; beads tracking unavailable"}
}

func lspCheck(lookPath func(string) (string, error)) Check {
	if _, err := lookPath("gopls"); err == nil {
		return Check{"lsp gate", OK, "gopls available"}
	}
	return Check{"lsp gate", Warn, "gopls not found; pre-proof diagnostics skipped"}
}

func agentCheck(agentEnv string, lookPath func(string) (string, error)) Check {
	name := strings.TrimSpace(agentEnv)
	if name == "" || strings.EqualFold(name, "none") || strings.EqualFold(name, "fixture") {
		return Check{"agent-preset", OK, "fixture generator (no vendor agent configured)"}
	}
	p, ok := agentruntime.DefaultPresetRegistry().Get(name)
	if !ok {
		return Check{"agent-preset", Fail, "ORION_AGENT=" + name + " is not a known preset"}
	}
	if _, err := lookPath(p.Command); err != nil {
		return Check{"agent-preset", Warn, "ORION_AGENT=" + name + " set but '" + p.Command + "' not on PATH (falls back to fixture)"}
	}
	return Check{"agent-preset", OK, name + " (" + p.Command + ")"}
}

// DirExists reports whether dir is an existing directory.
func DirExists(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

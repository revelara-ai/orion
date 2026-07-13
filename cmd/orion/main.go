// Command orion is the single Orion binary: an interactive TUI (default) and
// non-interactive CLI subcommands for CI/agent/headless use (PRD: cmd/orion).
//
// V2.0 skeleton (or-0d2): `orion` (no args) launches the Conversation pane;
// `orion --version` reports the version. Later tasks register the rest of the
// loop-control surface the acceptance criteria exercise (submit, spec, plan,
// task, proof, deliver, deps, init, answer, run, login/status, …).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proc"
	"github.com/revelara-ai/orion/internal/tui"
	"github.com/revelara-ai/orion/internal/worktree"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-V", "version":
			fmt.Println("orion " + resolveVersion())
			return 0
		case "-h", "--help", "help":
			usage(os.Stdout)
			return 0
		case "init":
			return cmdInit(args[1:])
		case "submit":
			return cmdSubmit(args[1:])
		case "answer":
			return cmdAnswer(args[1:])
		case "spec":
			return cmdSpec(args[1:])
		case "plan":
			return cmdPlan(args[1:])
		case "run":
			return cmdRun(args[1:])
		case "resume":
			return cmdResume(args[1:])
		case "baseline":
			return cmdBaseline(args[1:])
		case "map":
			return cmdMap(args[1:])
		case "change":
			return cmdChange(args[1:])
		case "proof":
			return cmdProof(args[1:])
		case "deliver":
			return cmdDeliver(args[1:])
		case "conductor":
			return cmdConductor(args[1:])
		case "deps":
			return cmdDeps(args[1:])
		case "tracker":
			return cmdTracker(args[1:])
		case "queue":
			return cmdQueue(args[1:])
		case "escalations":
			return cmdEscalations(args[1:])
		case "redbutton":
			return cmdRedButton(args[1:])
		case "login":
			return cmdLogin(args[1:])
		case "logout":
			return cmdLogout(args[1:])
		case "status":
			return cmdStatus(args[1:])
		case "doctor":
			return cmdDoctor(args[1:])
		case "skills":
			return cmdSkills(args[1:])
		case "agents":
			return cmdAgents(args[1:])
		case "evolve":
			return cmdEvolve(args[1:])
		case "design":
			return cmdDesign(args[1:])
		case "harness":
			return cmdHarness(args[1:])
		case "trace":
			return cmdTrace(args[1:])
		case "service":
			return cmdService(args[1:])
		case "boot":
			return cmdBoot(args[1:])
		default:
			// Unknown subcommand. The non-interactive loop-control surface is
			// implemented by later tasks; until then an unknown command is a hard
			// error (non-zero) and prints a recognizable marker so the acceptance
			// harness never reads an unimplemented command as a pass.
			fmt.Fprintf(os.Stderr, "orion: not implemented: %s\n", args[0])
			usage(os.Stderr)
			return 2
		}
	}

	// Default: launch the interactive Conversation pane, backed by the durable
	// Context Store so submitted intent survives a restart.
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion: resolve data dir:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion: open context store:", err)
		return 1
	}
	defer store.Close()

	// Install SIGINT/SIGTERM cleanup: cancel in-flight work and reap sandbox
	// process groups before exit (no orphaned children).
	reaper := proc.NewReaper()
	ctx, stop := proc.Install(context.Background(), reaper)
	defer stop()

	// Startup worktree reconciliation (filesystem as source of truth): prune
	// deleted worktrees, reap orphans, repair Context Store drift. Best-effort —
	// only meaningful inside a git repo.
	if repoRoot, err := gitToplevel(); err == nil {
		if err := worktree.New(repoRoot, store).Reconcile(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "orion: worktree reconcile:", err)
		}
	}

	// or-gik.3: the TUI launch banner is network-free — a cached (no-Me()) Polaris probe.
	dataDir, _ := doctorDataDir()
	bannerReport := health.Probe(health.Options{
		DataDir:  dataDir,
		LookPath: exec.LookPath,
		AgentEnv: os.Getenv("ORION_AGENT"),
		Polaris:  cachedPolarisCheck,
	})
	if err := tui.Run(orchestrator.NewWithStore(store), bannerReport, bannerIdentity(), tuiCommands()); err != nil {
		fmt.Fprintln(os.Stderr, "orion:", err)
		return 1
	}
	return 0
}

// resolveDataDir returns the Orion data directory: $ORION_DATA_DIR if set,
// otherwise ~/.orion. The directory is created with 0700 (it holds project
// context and, later, tokens).
func resolveDataDir() (string, error) {
	if d := os.Getenv("ORION_DATA_DIR"); d != "" {
		return d, os.MkdirAll(d, 0o700)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".orion")
	return d, os.MkdirAll(d, 0o700)
}

func resolveVersion() string {
	if version != "0.0.0-dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				rev := s.Value
				if len(rev) > 12 {
					rev = rev[:12]
				}
				return version + "+" + rev
			}
		}
	}
	return version
}

// gitToplevel returns the root of the git repo containing the cwd, or an error
// if the cwd is not inside a git repo.
func gitToplevel() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func usage(w *os.File) {
	fmt.Fprintln(w, "orion — the reliability layer of the agentic SDLC")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  orion                 launch the interactive TUI (Conversation)")
	fmt.Fprintln(w, "  orion --version       print version")
	fmt.Fprintln(w, "  orion --help          show this help")
}

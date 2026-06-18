// Command orion is the single Orion binary: an interactive TUI (default) and
// non-interactive CLI subcommands for CI/agent/headless use (PRD: cmd/orion).
//
// V2.0 skeleton (or-0d2): `orion` (no args) launches the Conversation pane;
// `orion --version` reports the version. Later tasks register the rest of the
// loop-control surface the acceptance criteria exercise (submit, spec, plan,
// task, proof, deliver, deps, init, answer, run, login/status, …).
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tui"
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

	// Default: launch the interactive Conversation pane.
	if err := tui.Run(orchestrator.New()); err != nil {
		fmt.Fprintln(os.Stderr, "orion:", err)
		return 1
	}
	return 0
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

func usage(w *os.File) {
	fmt.Fprintln(w, "orion — the reliability layer of the agentic SDLC")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  orion                 launch the interactive TUI (Conversation)")
	fmt.Fprintln(w, "  orion --version       print version")
	fmt.Fprintln(w, "  orion --help          show this help")
}

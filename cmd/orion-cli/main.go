// Command orion-cli is the operator and dogfood CLI for Orion.
//
// Subcommands:
//   - detect    Run rvl-cli scanner against a repo and emit findings.
//   - --version Print version and exit (legacy top-level flag, preserved).
//
// More subcommands (run, roundtrip) land in subsequent epics.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const progName = "orion-cli"

// subcommand is the contract every orion-cli subcommand implements.
type subcommand interface {
	Name() string
	Synopsis() string
	Run(ctx context.Context, args []string) int
}

func registeredCommands() []subcommand {
	return []subcommand{
		newDetectCmd(os.Stdout, os.Stderr),
	}
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage(os.Stderr, registeredCommands())
		os.Exit(2)
	}

	// Preserve the legacy --version top-level flag.
	switch args[0] {
	case "-h", "--help", "help":
		printUsage(os.Stdout, registeredCommands())
		return
	case "-v", "--version", "version":
		printVersion(os.Stdout)
		return
	}

	// Dispatch to subcommand.
	for _, cmd := range registeredCommands() {
		if cmd.Name() == args[0] {
			ctx, cancel := signalContext()
			defer cancel()
			os.Exit(cmd.Run(ctx, args[1:]))
		}
	}

	// Use a sanitized rendering of the unknown command (G705): show only its
	// length and first 30 chars, with non-printable bytes replaced. The
	// command itself is operator-supplied input, so we treat it as untrusted
	// when rendering.
	unknown := args[0]
	if len(unknown) > 30 {
		unknown = unknown[:30] + "..."
	}
	safe := make([]rune, 0, len(unknown))
	for _, r := range unknown {
		if r < 0x20 || r == 0x7f {
			safe = append(safe, '?')
			continue
		}
		safe = append(safe, r)
	}
	//nolint:gosec // G705: `safe` is built from a non-printable-stripped, length-capped copy of args[0]; len(args[0]) is just an int.
	_, _ = fmt.Fprintf(os.Stderr, "%s: unknown command (%d chars): %s\n", progName, len(args[0]), string(safe))
	printUsage(os.Stderr, registeredCommands())
	os.Exit(2)
}

func printUsage(w *os.File, cmds []subcommand) {
	_, _ = fmt.Fprintf(w, "Usage: %s <command> [flags]\n\nCommands:\n", progName)
	for _, c := range cmds {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", c.Name(), c.Synopsis())
	}
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "version", "Print version and exit")
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "help", "Show this help")
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	return ctx, cancel
}

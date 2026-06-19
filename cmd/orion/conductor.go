package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/revelara-ai/orion/internal/conductor"
)

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

// cmdConductor implements `orion conductor {start|stop|status|restart|attach|acp}`
// — the thin lifecycle manager for the Conductor control process (or-2az).
func cmdConductor(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion conductor: usage: start|stop|status|restart|attach|acp")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion conductor:", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		self = "orion"
	}
	m := &conductor.LifecycleManager{Dir: dir, Command: []string{self, "conductor", "acp"}}

	switch args[0] {
	case "acp":
		// Self-hosted headless Conductor daemon: serve the ACP agent over stdio (a
		// connected client — the TUI — drives it), but stay alive until signalled so
		// the lifecycle manager can supervise it even before a client attaches.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ca := conductor.ConductorAgent{Role: conductor.RoleTemplate{Project: "orion"}}
		go func() { _ = ca.Serve(ctx, os.Stdin, os.Stdout) }()
		<-ctx.Done()
		return 0
	case "start":
		if err := m.Start(nowStamp()); err != nil {
			fmt.Fprintln(os.Stderr, "orion conductor start:", err)
			return 1
		}
		_, pid := m.Status()
		fmt.Printf("conductor started (pid %d)\n", pid)
		return 0
	case "status":
		if running, pid := m.Status(); running {
			fmt.Printf("conductor: running (pid %d)\n", pid)
		} else {
			fmt.Println("conductor: stopped")
		}
		return 0
	case "stop":
		if err := m.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "orion conductor stop:", err)
			return 1
		}
		fmt.Println("conductor: stopped")
		return 0
	case "restart":
		_ = m.Stop()
		if err := m.Start(nowStamp()); err != nil {
			fmt.Fprintln(os.Stderr, "orion conductor restart:", err)
			return 1
		}
		fmt.Println("conductor: restarted")
		return 0
	case "attach":
		if running, pid := m.Status(); running {
			fmt.Printf("conductor: running (pid %d) — attach/observe not yet implemented (V2.0)\n", pid)
			return 0
		}
		fmt.Fprintln(os.Stderr, "conductor: not running")
		return 1
	default:
		fmt.Fprintln(os.Stderr, "orion conductor: unknown subcommand:", args[0])
		return 2
	}
}

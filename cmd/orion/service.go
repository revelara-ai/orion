package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/notify"
	"github.com/revelara-ai/orion/internal/worktree"
)

// or-v9f.25b: reboot survival. `orion service install` ships a user-level
// unit (systemd on Linux, launchd on macOS) pointing at `orion boot` — the
// headless entry that clears a stale pid, reconciles worktrees, re-dispatches
// incomplete runs (run.resumed events), and leaves the conductor supervised
// by the init system (Restart=on-failure owns restarts there).

const systemdUnit = `[Unit]
Description=Orion conductor (proof-gated build harness)
After=network-online.target

[Service]
Type=simple
ExecStart=%s boot --stay
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>ai.revelara.orion</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>boot</string><string>--stay</string></array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
</dict>
</plist>
`

func serviceUnitPath() (path, content string, err error) {
	self, err := os.Executable()
	if err != nil {
		self = "orion"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "LaunchAgents", "ai.revelara.orion.plist"),
			fmt.Sprintf(launchdPlist, self), nil
	}
	return filepath.Join(home, ".config", "systemd", "user", "orion.service"),
		fmt.Sprintf(systemdUnit, self), nil
}

// cmdService implements `orion service install|uninstall|status [--dry-run]`.
func cmdService(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion service: usage: orion service install [--dry-run] | uninstall | status")
		return 2
	}
	path, content, err := serviceUnitPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion service:", err)
		return 1
	}
	switch args[0] {
	case "install":
		for _, a := range args[1:] {
			if a == "--dry-run" {
				fmt.Print(content)
				return 0
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "orion service install:", err)
			return 1
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "orion service install:", err)
			return 1
		}
		activate := "systemctl --user daemon-reload && systemctl --user enable --now orion"
		if runtime.GOOS == "darwin" {
			activate = "launchctl load " + path
		}
		fmt.Printf("service unit written: %s\nactivate it: %s\n", path, activate)
		return 0
	case "uninstall":
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "orion service uninstall:", err)
			return 1
		}
		fmt.Println("service unit removed:", path)
		return 0
	case "status":
		if _, err := os.Stat(path); err != nil {
			fmt.Println("service: not installed")
			return 0
		}
		fmt.Println("service: installed at", path)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "orion service: unknown subcommand:", args[0])
		return 2
	}
}

// cmdBoot is the headless boot entry (or-v9f.25b): stale-pid cleanup,
// worktree reconciliation, incomplete-run re-dispatch with run.resumed
// events. --stay keeps the process alive supervising the conductor (the
// init-system mode).
func cmdBoot(args []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion boot:", err)
		return 1
	}
	ctx := context.Background()

	// (1) Clear a stale conductor pid — a reboot leaves conductor.json behind.
	self, err := os.Executable()
	if err != nil {
		self = "orion"
	}
	m := &conductor.LifecycleManager{Dir: dir, Command: []string{self, "conductor", "acp"}}
	if running, _ := m.Status(); !running {
		_ = os.Remove(filepath.Join(dir, "conductor.json"))
	}

	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion boot:", err)
		return 1
	}

	// (2) Reconcile worktrees (filesystem is truth after a crash/reboot).
	if repoRoot, rerr := gitToplevel(); rerr == nil {
		if rerr := worktree.New(repoRoot, store).Reconcile(ctx); rerr != nil {
			fmt.Fprintln(os.Stderr, "orion boot: worktree reconcile:", rerr)
		}
	}

	// (3) Re-dispatch incomplete runs: a persisted run that never closed.
	resumed := false
	if proj, _, perr := store.CurrentProjectSpec(ctx); perr == nil {
		if runID, ok, _ := store.LatestRunID(ctx, proj.ID); ok {
			if incomplete, ierr := runIncomplete(ctx, store, runID); ierr == nil && incomplete {
				fmt.Printf("boot: run %s never closed — re-dispatching\n", runID)
				_ = notify.Notify(ctx, notify.Event{Kind: "run.resumed", Detail: "boot re-dispatch of incomplete run " + runID})
				_ = store.Close()
				if code := cmdResume(nil); code != 0 {
					return code
				}
				resumed = true
			}
		}
	}
	if !resumed {
		_ = store.Close()
		fmt.Println("boot: no incomplete runs")
	}

	// (4) --stay: supervise the conductor under the init system.
	for _, a := range args {
		if a == "--stay" {
			return cmdConductor([]string{"start", "--supervise"})
		}
	}
	return 0
}

// runIncomplete reports whether a persisted run has no terminal Run event.
func runIncomplete(ctx context.Context, store *contextstore.Store, runID string) (bool, error) {
	events, err := store.ListRunEventsAfter(ctx, runID, 0)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if e.Phase == "Run" && (e.Status == "done" || e.Status == "failed") {
			return false, nil
		}
	}
	return len(events) > 0, nil
}

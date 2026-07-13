package conductor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Supervision layer for weeks-long runs (or-v9f.25a): the conductor daemon
// gets a WATCHDOG — unexpected exit is detected, notified, and restarted
// with bounded backoff; exhaustion escalates instead of flapping forever.
// A heartbeat file distinguishes RUNNING from WEDGED (pid alive, loop dead).
// Under systemd, Restart=on-failure owns restarts and Orion's role reduces
// to crash-detection + notify + heartbeat.

const (
	heartbeatFile = "conductor.heartbeat"
	// WedgeThreshold: a live pid whose heartbeat is older than this is WEDGED.
	WedgeThreshold = 90 * time.Second
)

// HeartbeatPath is the file the conductor touches every tick.
func HeartbeatPath(dir string) string { return filepath.Join(dir, heartbeatFile) }

// TouchHeartbeat stamps liveness (best-effort).
func TouchHeartbeat(dir string) {
	now := time.Now()
	p := HeartbeatPath(dir)
	if err := os.Chtimes(p, now, now); err != nil {
		_ = os.WriteFile(p, []byte(now.UTC().Format(time.RFC3339)), 0o600)
	}
}

// HeartbeatAge returns how stale the heartbeat is (ok=false when absent).
func HeartbeatAge(dir string) (time.Duration, bool) {
	st, err := os.Stat(HeartbeatPath(dir))
	if err != nil {
		return 0, false
	}
	return time.Since(st.ModTime()), true
}

// StatusKind reports the conductor's three-state health: stopped (no live
// pid), wedged (live pid, stale/absent heartbeat), running.
func (m *LifecycleManager) StatusKind() string {
	running, _ := m.Status()
	if !running {
		return "stopped"
	}
	age, ok := HeartbeatAge(m.Dir)
	if !ok || age > WedgeThreshold {
		return "wedged"
	}
	return "running"
}

// SuperviseOpts bounds the watchdog.
type SuperviseOpts struct {
	MaxRestarts int           // restarts allowed within Window before escalating (<=0 → 5)
	Window      time.Duration // the restart-counting window (<=0 → 10min)
	Backoff     time.Duration // base backoff, doubled per consecutive restart (<=0 → 2s)
	Poll        time.Duration // liveness poll interval (<=0 → 1s)
}

func (o SuperviseOpts) withDefaults() SuperviseOpts {
	if o.MaxRestarts <= 0 {
		o.MaxRestarts = 5
	}
	if o.Window <= 0 {
		o.Window = 10 * time.Minute
	}
	if o.Backoff <= 0 {
		o.Backoff = 2 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	return o
}

// Supervise runs the watchdog loop until ctx ends or the restart budget is
// exhausted. onEvent receives conductor.crashed / conductor.restarted /
// conductor.supervision.exhausted (the caller wires it to notify). The
// conductor is started if not already running.
func (m *LifecycleManager) Supervise(ctx context.Context, opts SuperviseOpts, onEvent func(kind, detail string)) error {
	opts = opts.withDefaults()
	emit := func(kind, detail string) {
		if onEvent != nil {
			onEvent(kind, detail)
		}
	}
	if running, _ := m.Status(); !running {
		if err := m.Start(time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("supervise: initial start: %w", err)
		}
	}
	var restarts []time.Time
	backoff := opts.Backoff
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.Poll):
		}
		if running, _ := m.Status(); running {
			backoff = opts.Backoff // a healthy observation resets the backoff ramp
			continue
		}
		// Unexpected exit: the pid died without Stop removing the agent file.
		emit("conductor.crashed", "conductor process exited unexpectedly")
		now := time.Now()
		fresh := restarts[:0]
		for _, t := range restarts {
			if now.Sub(t) <= opts.Window {
				fresh = append(fresh, t)
			}
		}
		restarts = fresh
		if len(restarts) >= opts.MaxRestarts {
			detail := fmt.Sprintf("%d restarts within %s — supervision stopped; investigate before restarting", len(restarts), opts.Window)
			emit("conductor.supervision.exhausted", detail)
			return fmt.Errorf("supervise: %s", detail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		_ = os.Remove(m.agentFilePath()) // clear the stale pid before restarting
		if err := m.Start(time.Now().UTC().Format(time.RFC3339)); err != nil {
			emit("conductor.supervision.exhausted", "restart failed: "+err.Error())
			return fmt.Errorf("supervise: restart: %w", err)
		}
		restarts = append(restarts, now)
		emit("conductor.restarted", fmt.Sprintf("restart %d/%d within window", len(restarts), opts.MaxRestarts))
	}
}

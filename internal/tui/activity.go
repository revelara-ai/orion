package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
)

// render returns the activity pane (in-flight) or the collapsed summary (idle).
// It returns "" when idle with no summary, so the caller can skip inserting it.
func (a activityModel) render(width int, inFlight bool) string {
	if !inFlight {
		if a.summary == "" {
			return ""
		}
		return dimStyle.Render("  " + a.summary)
	}
	var b strings.Builder
	// actor stack as a tree
	for i, f := range a.stack {
		prefix := "  "
		if f.depth > 0 || i > 0 {
			prefix = "  " + strings.Repeat("  ", f.depth) + "↳ "
		}
		fmt.Fprintf(&b, "%s%s · %s\n", prefix, f.actor, f.activity)
	}
	// phase strip
	if len(a.phases) > 0 {
		var parts []string
		for _, p := range a.phases {
			parts = append(parts, p.name+" "+phaseGlyph(p.status))
		}
		fmt.Fprintf(&b, "  %s\n", strings.Join(parts, "  "))
	}
	// recent log
	for _, l := range a.log {
		fmt.Fprintf(&b, "  · %s\n", l)
	}
	body := strings.TrimRight(b.String(), "\n")
	if body == "" {
		body = "  …"
	}
	return activityPane.Width(width).Render(body)
}

func phaseGlyph(status string) string {
	switch status {
	case "done":
		return "✓"
	case "fail":
		return "✗"
	case "warn":
		return "⚠"
	default:
		return "⠋"
	}
}

type actorFrame struct {
	actor    string
	activity string
	depth    int
}

type phaseMark struct {
	name   string
	status string // running | done | fail
}

type activityModel struct {
	stack   []actorFrame // call stack; index 0 = conductor, deeper = subagent
	phases  []phaseMark  // build-pipeline strip, in first-seen order
	log     []string     // ring buffer of the last logCap lines
	summary string       // one-line idle summary
	bus     *ProgressBus
}

const logCap = 4

func newActivityModel() activityModel {
	return activityModel{bus: NewProgressBus(2 * time.Second)}
}

// apply folds one activity update into the model.
func (a *activityModel) apply(u acp.Update) {
	if u.Kind != acp.ActivityKind {
		return
	}
	a.bus.EmitActivity(u.Actor, u.Text, u.Depth, u.Status)
	if u.Text != "" {
		a.pushLog(fmt.Sprintf("%s · %s", u.Actor, u.Text))
	}

	// Build phases (depth 0, a known phase name) drive ONLY the phase strip.
	// They must NOT touch the actor-stack — the Depth:0 tool frame (e.g.
	// "build_service") must remain pinned while its phases stream to the strip.
	if u.Depth == 0 && isPhaseName(u.Text) {
		a.setPhase(u.Text, u.Status)
		return
	}

	switch u.Status {
	case "done", "fail":
		a.popTo(u.Depth) // resolve this frame and anything deeper
	default: // running
		a.pushOrReplace(actorFrame{actor: u.Actor, activity: u.Text, depth: u.Depth})
	}
}

func (a *activityModel) pushOrReplace(f actorFrame) {
	// Remove any existing frames at this depth or deeper, then push the new frame.
	n := 0
	for _, existing := range a.stack {
		if existing.depth < f.depth {
			a.stack[n] = existing
			n++
		}
	}
	a.stack = append(a.stack[:n], f)
}

// popTo removes all frames whose depth is >= depth (i.e. this frame and anything deeper).
func (a *activityModel) popTo(depth int) {
	n := 0
	for _, f := range a.stack {
		if f.depth < depth {
			a.stack[n] = f
			n++
		}
	}
	a.stack = a.stack[:n]
}

func (a *activityModel) setPhase(name, status string) {
	for i := range a.phases {
		if strings.EqualFold(a.phases[i].name, name) {
			a.phases[i].status = status
			return
		}
	}
	a.phases = append(a.phases, phaseMark{name: name, status: status})
}

func (a *activityModel) pushLog(line string) {
	a.log = append(a.log, line)
	if len(a.log) > logCap {
		a.log = a.log[len(a.log)-logCap:]
	}
}

// finish computes the one-line idle summary and clears the live stack.
// Only status "fail" is a hard failure; "warn" is advisory and does NOT produce a ✗ summary.
func (a *activityModel) finish() {
	var failed, done []string
	for _, p := range a.phases {
		switch p.status {
		case "fail":
			failed = append(failed, p.name)
		case "done", "warn":
			done = append(done, p.name)
		}
	}
	if len(failed) > 0 {
		a.summary = "✗ " + strings.Join(failed, "/")
	} else if len(done) > 0 {
		a.summary = "✓ " + strings.Join(done, "/")
	} else {
		a.summary = ""
	}
	a.stack = nil
}

func (a *activityModel) reset() {
	a.stack, a.phases, a.log, a.summary = nil, nil, nil, ""
	a.bus = NewProgressBus(2 * time.Second)
}

func isPhaseName(s string) bool {
	switch strings.ToLower(s) {
	case "decompose", "cluster", "reliabilitycontext", "generate", "align", "prove", "deliver", "queue",
		"diagnose", "escalate", "integrate", "systemvalidate":
		return true
	}
	return false
}

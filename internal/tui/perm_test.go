package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/revelara-ai/orion/internal/acp"
)

// A Kind:"tool" permission renders an approval card with the tool, a colorized preview,
// and the three single-key choices; 'a' resolves it as allow_always.
func TestToolPermissionCardAndAllowAlways(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 70, Height: 20})
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "edit_file", Preview: "x.go\n-old line\n+new line"}, reply: reply})

	card := ansi.Strip(m.renderTranscript())
	for _, want := range []string{"permission", "edit_file", "old line", "new line", "allow once", "allow always", "deny"} {
		if !strings.Contains(card, want) {
			t.Errorf("tool-permission card missing %q:\n%s", want, card)
		}
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if res := <-reply; res.Outcome != "allow_always" {
		t.Errorf("'a' should resolve allow_always, got %q", res.Outcome)
	}
	if m.hasPerm() {
		t.Error("the pending permission should be cleared after answering")
	}
}

func TestToolPermissionSingleKeys(t *testing.T) {
	cases := map[string]string{"y": "allow_once", "n": "deny"}
	for key, want := range cases {
		m := newTestConvo(t)
		reply := make(chan acp.PermissionResult, 1)
		m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ ls"}, reply: reply})
		_ = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if res := <-reply; res.Outcome != want {
			t.Errorf("key %q should resolve %q, got %q", key, want, res.Outcome)
		}
	}
}

// While a tool-permission card is up, ordinary keys don't leak into the input box, and
// Esc denies (safe default).
func TestToolPermissionCapturesInputAndEscDenies(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ ls"}, reply: reply})
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")}) // ignored, not typed
	if v := m.input.Value(); v != "" {
		t.Errorf("keys must not leak into the input while a tool card is up, got %q", v)
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyEsc})
	if res := <-reply; res.Outcome != "deny" {
		t.Errorf("Esc should deny, got %q", res.Outcome)
	}
}

// TestConcurrentPermissionsQueueFIFO (or-f06): two permission requests arriving
// before the first is answered must BOTH be honored, in order — a second request
// must never orphan the first gate goroutine. Answering the head surfaces the
// next; each reply channel receives exactly one decision, at the right time.
func TestConcurrentPermissionsQueueFIFO(t *testing.T) {
	m := newTestConvo(t)
	r1 := make(chan acp.PermissionResult, 1)
	r2 := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ one"}, reply: r1})
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ two"}, reply: r2})

	if !m.hasPerm() {
		t.Fatal("a permission should be pending after two requests")
	}
	// The SECOND reply must NOT be sent while its card is still queued.
	select {
	case <-r2:
		t.Fatal("the queued second permission was answered before it was surfaced")
	default:
	}

	// Answer the head ('y' → allow_once): r1 receives it, r2 still waits.
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	select {
	case res := <-r1:
		if res.Outcome != "allow_once" {
			t.Fatalf("first reply = %q, want allow_once", res.Outcome)
		}
	default:
		t.Fatal("first gate goroutine was orphaned — its reply never arrived (single-slot bug)")
	}
	if !m.hasPerm() {
		t.Fatal("the queued second permission must surface after the first is answered")
	}
	// The second card must be VISIBLE now (not merely queued) — otherwise the
	// developer is asked to answer a prompt they cannot see.
	if !strings.Contains(transcript(m), "$ two") {
		t.Fatalf("the second card must render once it becomes the head:\n%s", transcript(m))
	}
	select {
	case <-r2:
		t.Fatal("the second reply must not be sent until its card is answered")
	default:
	}

	// Answer the second ('n' → deny): r2 receives it, queue drains.
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	select {
	case res := <-r2:
		if res.Outcome != "deny" {
			t.Fatalf("second reply = %q, want deny", res.Outcome)
		}
	default:
		t.Fatal("second gate goroutine never got its reply")
	}
	if m.hasPerm() {
		t.Fatal("queue must be empty after both permissions are answered")
	}
}

// TestCancelDeniesAllQueuedPermissions (or-f06): cancel/quit must deny EVERY
// queued permission, not just the head — no gate goroutine is left blocked.
func TestCancelDeniesAllQueuedPermissions(t *testing.T) {
	m := newTestConvo(t)
	r1 := make(chan acp.PermissionResult, 1)
	r2 := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ one"}, reply: r1})
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ two"}, reply: r2})

	if !m.cancelInFlight() {
		t.Fatal("cancelInFlight should report it denied pending permissions")
	}
	for i, r := range []chan acp.PermissionResult{r1, r2} {
		select {
		case res := <-r:
			if res.Outcome != "denied" {
				t.Fatalf("perm %d: got %q, want denied", i+1, res.Outcome)
			}
		default:
			t.Fatalf("perm %d: gate goroutine left blocked on cancel (deny-all missed it)", i+1)
		}
	}
	if m.hasPerm() {
		t.Fatal("the queue must be cleared after deny-all")
	}
}

// 'e' toggles the preview expansion for a long diff (progressive disclosure).
func TestToolPermissionExpand(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("+added line placeholder\n")
	}
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "write_file", Preview: b.String()}, reply: reply})
	if !strings.Contains(m.renderTranscript(), "more · e expand") {
		t.Fatal("a long preview should truncate with an expand hint")
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if strings.Contains(m.renderTranscript(), "more · e expand") {
		t.Error("'e' should expand the preview (hint gone)")
	}
	if !m.hasPerm() {
		t.Error("expanding must not resolve the permission")
	}
	reply <- acp.PermissionResult{Outcome: "deny"} // drain
}

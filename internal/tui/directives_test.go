package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// or-ykz.6: @file inlines a readable file; an unresolvable @token is left
// verbatim (never silently dropped).
func TestExpandDirectivesFileRef(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(f, []byte("hello world\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := ExpandDirectives("summarize @"+f+" please", nil)
	if !res.Send {
		t.Fatalf("a file ref must still send: %+v", res)
	}
	if !strings.Contains(res.Text, "hello world") || !strings.Contains(res.Text, "notes.txt") {
		t.Fatalf("file contents must be inlined: %q", res.Text)
	}
	if strings.Contains(res.Text, "@"+f) {
		t.Fatalf("the resolved @token must be replaced, not left raw: %q", res.Text)
	}

	// A non-file @mention survives untouched.
	res = ExpandDirectives("ping @nobody-here about this", nil)
	if res.Text != "ping @nobody-here about this" {
		t.Fatalf("an unresolvable @token must be left verbatim: %q", res.Text)
	}
}

// !cmd runs and SENDS the output; !!cmd runs but does NOT send (local action).
func TestExpandDirectivesCommands(t *testing.T) {
	fakeRun := func(_ context.Context, cmd string) (string, error) {
		if strings.Contains(cmd, "boom") {
			return "partial", errors.New("exit 1")
		}
		return "ran: " + cmd + "\n", nil
	}

	send := ExpandDirectives("!git status", fakeRun)
	if !send.Send {
		t.Fatalf("!cmd must send: %+v", send)
	}
	if !strings.Contains(send.Text, "ran: git status") || !strings.Contains(send.Text, "$ git status") {
		t.Fatalf("!cmd output must be in the message: %q", send.Text)
	}

	local := ExpandDirectives("!!ls -la", fakeRun)
	if local.Send {
		t.Fatalf("!!cmd must NOT send (local action): %+v", local)
	}
	if !strings.Contains(local.Local, "ran: ls -la") {
		t.Fatalf("!!cmd output must still surface locally: %q", local.Local)
	}
	if local.Text != "" {
		t.Fatalf("!!cmd must produce no message text: %q", local.Text)
	}

	// A failing command carries its output + the error, and still sends (!).
	fail := ExpandDirectives("!boom", fakeRun)
	if !fail.Send || !strings.Contains(fail.Text, "partial") || !strings.Contains(fail.Text, "exit 1") {
		t.Fatalf("a failing !cmd must carry output + error: %+v", fail)
	}
}

// A plain line with no directive is unchanged.
func TestExpandDirectivesPassthrough(t *testing.T) {
	res := ExpandDirectives("just a normal message", nil)
	if !res.Send || res.Text != "just a normal message" {
		t.Fatalf("plain line must pass through: %+v", res)
	}
}

package orionsdk

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// The or-ykz.4 done-when: an SDK consumer runs the FULL loop in-process —
// submit → answer → requirement → ratify → build/prove/deliver — and asserts
// on the verdict, consuming the typed event stream along the way.
func TestSDKFullLoopInProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("full in-process loop (~1 min proof pipeline) — run without -short")
	}
	// Builds run in worktrees of the current repo: give the loop its own.
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	t.Chdir(dir)

	cl, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx := context.Background()

	conf, err := cl.Submit(ctx, "Build an HTTP service that returns the current time.")
	if err != nil {
		t.Fatal(err)
	}
	if len(conf.OpenDecisions) == 0 {
		t.Fatalf("a bare intent must open decisions: %+v", conf)
	}
	for _, kv := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
		if err := cl.Answer(ctx, kv[0], kv[1]); err != nil {
			t.Fatalf("answer %s: %v", kv[0], err)
		}
	}
	if err := cl.AddRequirement(ctx,
		`GET /time returns the current time as an RFC3339 "time" JSON key`,
		`[{"request":{"method":"GET","path":"/time"},"expect":{"status":200,"content_type":"application/json","assertions":[{"kind":"json_key_rfc3339","key":"time"}]}}]`,
	); err != nil {
		t.Fatalf("requirement: %v", err)
	}
	if _, err := cl.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("assumptions: %v", err)
	}
	hash, err := cl.Ratify(ctx)
	if err != nil {
		t.Fatalf("ratify: %v", err)
	}
	if hash == "" {
		t.Fatal("ratified spec must be hash-anchored")
	}

	var events []Event
	out, err := cl.BuildService(ctx, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if out.Verdict != "Accept" {
		t.Fatalf("fixture loop must prove Accept, got %+v", out)
	}
	if out.Delivery != "deliver" {
		t.Fatalf("bar must clear on the fixture: %+v", out)
	}
	if len(events) == 0 {
		t.Fatal("the typed event stream must carry the loop's phases")
	}
	seen := map[string]bool{}
	lastSeq := 0
	for _, e := range events {
		seen[e.Phase] = true
		if e.Seq <= lastSeq {
			t.Fatalf("event seq must be strictly increasing: %+v", events)
		}
		lastSeq = e.Seq
	}
	for _, phase := range []string{"Prove", "Deliver"} {
		if !seen[phase] {
			t.Fatalf("event stream missing phase %s; saw %v", phase, seen)
		}
	}
}

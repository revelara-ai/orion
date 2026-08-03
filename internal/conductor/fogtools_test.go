package conductor

import (
	"strings"
	"testing"
)

// TestFogToolsRoundTrip (or-kjcm.5): park → list → preview shows the parked
// area → graduate lands questions on the open-question ledger and clears the
// note. Uses the same registry fixture as the other spec tools.
func TestFogToolsRoundTrip(t *testing.T) {
	_, run := specRegistry(t)

	out, err := run("park_fog", `{"note":"something about persistence, once the traffic answer lands"}`)
	if err != nil {
		t.Fatalf("park_fog: %v", err)
	}
	if !strings.Contains(out, "parked (") {
		t.Fatalf("unexpected park output: %q", out)
	}
	id := out[strings.Index(out, "(")+1 : strings.Index(out, ")")]

	out, err = run("list_fog", `{}`)
	if err != nil || !strings.Contains(out, "persistence") {
		t.Fatalf("list_fog: %v %q", err, out)
	}

	// The preview carries the parked area as a signpost.
	preview, err := run("preview_spec", `{}`)
	if err != nil {
		t.Fatalf("preview_spec: %v", err)
	}
	if !strings.Contains(preview, "Not yet specified") || !strings.Contains(preview, "persistence") {
		t.Fatalf("preview must signpost parked areas:\n%s", preview)
	}

	out, err = run("graduate_fog", `{"id":"`+id+`","questions":[{"phase":"spec","question":"Which datastore — and does it need durability beyond a restart?","severity":"advisory"}]}`)
	if err != nil {
		t.Fatalf("graduate_fog: %v", err)
	}
	if !strings.Contains(out, "graduated into 1") {
		t.Fatalf("unexpected graduate output: %q", out)
	}

	if out, err = run("list_fog", `{}`); err != nil || !strings.Contains(out, "nothing parked") {
		t.Fatalf("graduated note must clear the list: %v %q", err, out)
	}
	if out, err = run("list_open_questions", `{}`); err != nil || !strings.Contains(out, "datastore") {
		t.Fatalf("graduated question must appear on the question ledger: %v %q", err, out)
	}
}

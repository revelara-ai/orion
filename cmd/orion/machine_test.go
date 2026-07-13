package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// or-ykz.4: the submit event stream is TYPED — a discriminated event per
// line: intent_submitted, open_decision per gap, gate summary last.
func TestSubmitEventsTyped(t *testing.T) {
	conf := orchestrator.Confirmation{
		Intent:   "build a time service",
		Accepted: true,
		OpenDecisions: []completeness.OpenDecision{
			{Key: "port", Question: "Which port?"},
			{Key: "route", Question: "Which route?"},
		},
	}
	events := submitEvents(conf)
	if len(events) != 4 {
		t.Fatalf("want intent+2 decisions+gate, got %d", len(events))
	}
	var kinds []string
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var probe struct {
			Event    string `json:"event"`
			Key      string `json:"key"`
			Accepted *bool  `json:"accepted"`
			Open     int    `json:"open"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			t.Fatal(err)
		}
		if probe.Event == "" {
			t.Fatalf("every event needs the discriminator: %s", b)
		}
		kinds = append(kinds, probe.Event)
		if probe.Event == "gate" && (probe.Accepted == nil || probe.Open != 2) {
			t.Fatalf("gate event must summarize: %s", b)
		}
	}
	want := "intent_submitted,open_decision,open_decision,gate"
	if got := strings.Join(kinds, ","); got != want {
		t.Fatalf("event order: got %s want %s", got, want)
	}
}

// The phase stream is JSONL with strictly-increasing seq and the "phase"
// discriminator on every line.
func TestJSONSinkStream(t *testing.T) {
	var buf strings.Builder
	sink := jsonSink(&buf)
	sink(conductor.PhaseEvent{Phase: "Prove", Status: conductor.PhaseDone, Detail: "green"})
	sink(conductor.PhaseEvent{Phase: "Deliver", Status: conductor.PhaseWarn, Detail: "escalate"})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d: %q", len(lines), buf.String())
	}
	for i, ln := range lines {
		var e struct {
			Event, Phase, Status string
			Seq                  int
		}
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Fatalf("line %d not JSON: %v", i, err)
		}
		if e.Event != "phase" || e.Seq != i+1 || e.Phase == "" || e.Status == "" {
			t.Fatalf("line %d malformed: %s", i, ln)
		}
	}
}

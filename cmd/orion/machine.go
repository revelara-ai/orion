package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// Machine mode (or-ykz.4, A3): --mode json turns the loop into a typed,
// line-delimited event stream on stdout so CI and other agents consume
// structure, not prose. Every line is one JSON object with an "event"
// discriminator.

// jsonSink adapts the conductor phase stream to JSONL phase events on w.
func jsonSink(w io.Writer) conductor.PhaseSink {
	enc := json.NewEncoder(w)
	seq := 0
	return func(e conductor.PhaseEvent) {
		seq++
		_ = enc.Encode(struct {
			Event  string `json:"event"`
			Seq    int    `json:"seq"`
			Phase  string `json:"phase"`
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
		}{"phase", seq, e.Phase, string(e.Status), e.Detail})
	}
}

// emitRunResult prints the terminal result event of a run.
func emitRunResult(res conductor.BuildResult) {
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Event    string `json:"event"`
		TaskID   string `json:"task_id"`
		Verdict  string `json:"verdict"`
		Closed   bool   `json:"closed"`
		Tier     string `json:"tier"`
		Delivery string `json:"delivery"`
	}{"result", res.TaskID, res.Verdict, res.Closed, res.Tier, res.Delivery})
}

// submitEvents renders a submit confirmation as the typed event stream:
// intent_submitted, one open_decision per gap, then the gate summary.
func submitEvents(conf orchestrator.Confirmation) []any {
	events := []any{struct {
		Event  string `json:"event"`
		Intent string `json:"intent"`
	}{"intent_submitted", conf.Intent}}
	for _, d := range conf.OpenDecisions {
		events = append(events, struct {
			Event    string `json:"event"`
			Key      string `json:"key"`
			Question string `json:"question"`
		}{"open_decision", d.Key, d.Question})
	}
	events = append(events, struct {
		Event    string `json:"event"`
		Accepted bool   `json:"accepted"`
		Open     int    `json:"open"`
	}{"gate", conf.Accepted, len(conf.OpenDecisions)})
	return events
}

// emitEvents writes one JSONL line per event.
func emitEvents(events []any) int {
	enc := json.NewEncoder(os.Stdout)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			fmt.Fprintln(os.Stderr, "orion:", err)
			return 1
		}
	}
	return 0
}

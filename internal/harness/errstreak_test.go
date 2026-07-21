package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// failingCaseTool registers add_case as always-erroring and returns the
// execution counter — the shape of the live failure (grounding rejections).
func failingCaseTool(reg *tools.Registry) *int {
	execs := new(int)
	reg.Register(tools.Tool{
		Name: "add_case", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) {
			*execs++
			return "", fmt.Errorf("ungrounded case")
		},
	})
	return execs
}

// streakNudged reports whether the conversation carries the streak guard's
// corrective result, and fails the test if that result is not an error.
func streakNudged(t *testing.T, convo []llm.Message) bool {
	t.Helper()
	nudged := false
	for _, m := range convo {
		for _, b := range m.Content {
			if b.ToolResult != nil && strings.Contains(b.ToolResult.Content, "strategy stall") {
				if !b.ToolResult.IsError {
					t.Error("the streak nudge must be an error result")
				}
				nudged = true
			}
		}
	}
	return nudged
}

// TestLoopErrorStreakGuard: a model looping on ONE tool with VARYING inputs,
// each returning an error, evades the identical-input stall detector and burns
// the whole iteration budget (observed live: add_case grounding failures). The
// harness must nudge at 4 consecutive errors (skip execution, return a
// corrective error result) and cleanly stop the turn at 6 with a NAMED error —
// not grind to a generic max-iterations.
func TestLoopErrorStreakGuard(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":2}`),
		stallToolUse("add_case", `{"x":3}`),
		stallToolUse("add_case", `{"x":4}`),
		stallToolUse("add_case", `{"x":5}`),
		stallToolUse("add_case", `{"x":6}`),
	}}
	reg := tools.NewRegistry()
	execs := failingCaseTool(reg)
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 20}}
	convo, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if !errors.Is(err, ErrErrorStreak) {
		t.Fatalf("turn must end with the named error-streak error, got: %v", err)
	}
	if *execs != 3 {
		t.Errorf("the 4th+ consecutive same-tool error must not execute; executions = %d, want 3", *execs)
	}
	if !streakNudged(t, convo) {
		t.Error("corrective streak nudge missing from the conversation")
	}
	if prov.calls >= 20 {
		t.Errorf("the streak must stop the turn well before MaxIterations, used %d", prov.calls)
	}
}

// TestLoopErrorStreakResetsOnDifferentTool: any call to a DIFFERENT tool —
// even one that itself errors — resets the streak. Error-then-investigate
// cycles are normal agent behavior: no nudge, everything executes.
func TestLoopErrorStreakResetsOnDifferentTool(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":2}`),
		stallToolUse("add_case", `{"x":3}`),
		stallToolUse("read_file", `{"p":"a"}`),
		stallToolUse("add_case", `{"x":4}`),
		stallToolUse("add_case", `{"x":5}`),
		stallToolUse("add_case", `{"x":6}`),
		{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}},
	}}
	reg := tools.NewRegistry()
	execs := failingCaseTool(reg)
	reg.Register(tools.Tool{
		Name: "read_file", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "", fmt.Errorf("no such file") },
	})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 12}}
	convo, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("an interleaved different tool must reset the streak: %v", err)
	}
	if *execs != 6 || resp == nil || resp.Text() != "done" {
		t.Errorf("all calls execute and the turn completes: execs=%d, want 6", *execs)
	}
	if streakNudged(t, convo) {
		t.Error("streak nudge fired despite an interleaved different tool")
	}
}

// TestLoopErrorStreakResetsOnSuccess: a success from the streaking tool proves
// the approach works — the streak resets and later errors start a fresh count.
func TestLoopErrorStreakResetsOnSuccess(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":2}`),
		stallToolUse("add_case", `{"x":3}`),
		stallToolUse("add_case", `{"x":4}`),
		stallToolUse("add_case", `{"x":5}`),
		stallToolUse("add_case", `{"x":6}`),
		{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}},
	}}
	execs := 0
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "add_case", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) {
			execs++
			if execs == 3 {
				return "ok", nil
			}
			return "", fmt.Errorf("ungrounded case")
		},
	})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 12}}
	convo, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("a success must reset the streak: %v", err)
	}
	if execs != 6 || resp == nil || resp.Text() != "done" {
		t.Errorf("all calls execute and the turn completes: execs=%d, want 6", execs)
	}
	if streakNudged(t, convo) {
		t.Error("streak nudge fired despite a resetting success")
	}
}

// TestLoopIdenticalInputErrorsStillStall: identical-input error loops belong
// to the stall detector — its nudge and named stop are unchanged, and the
// streak guard must not double-fire (accounting is skipped while the stall
// guard intercepts).
func TestLoopIdenticalInputErrorsStillStall(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{stallToolUse("add_case", `{"x":1}`)}}
	reg := tools.NewRegistry()
	failingCaseTool(reg)
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 20}}
	convo, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("identical-input loop must end with the stall error, got: %v", err)
	}
	if errors.Is(err, ErrErrorStreak) {
		t.Fatalf("the streak guard must not hijack an identical-input stall: %v", err)
	}
	if streakNudged(t, convo) {
		t.Error("streak nudge fired on an identical-input loop (the stall guard owns it)")
	}
}

// or-nos3: the abort error must CARRY the per-strike evidence (inputs + error
// text) — a bare "failed 6× consecutively" made live failures undiagnosable
// from the session log (dogfood 2026-07-21: diffgen read_file streak).
func TestLoopErrorStreakCarriesEvidence(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":2}`),
		stallToolUse("add_case", `{"x":3}`),
		stallToolUse("add_case", `{"x":4}`),
		stallToolUse("add_case", `{"x":5}`),
		stallToolUse("add_case", `{"x":6}`),
	}}
	reg := tools.NewRegistry()
	failingCaseTool(reg)
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 20}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if !errors.Is(err, ErrErrorStreak) {
		t.Fatalf("turn must end with the named error-streak error, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{`{"x":1}`, `{"x":3}`, "ungrounded case", "not executed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("abort error must carry strike evidence %q, got:\n%s", want, msg)
		}
	}
}

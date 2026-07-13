package hookbus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
)

// TestHookRegisteredMidSessionInterceptsLiveRegistry (or-0sk, A2 runtime tool
// registration): a package hook registered AFTER a tool registry was built —
// mid-session, mid-turn — intercepts that registry's very next dispatch,
// because SetIntercept holds the bus's live method, not a registration-time
// snapshot. Unregistering restores clean dispatch on the same registry. This
// is the runtime-extension invariant the per-turn registry rebuild rides on.
func TestHookRegisteredMidSessionInterceptsLiveRegistry(t *testing.T) {
	bus := &Bus{}
	r := tools.NewRegistry()
	r.SetIntercept(bus.BeforeToolCall) // wired exactly as specTools wires Default
	r.Register(tools.Tool{
		Name:        "echo",
		Description: "echo input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Run:         func(_ context.Context, in json.RawMessage) (string, error) { return string(in), nil },
	})
	ctx := context.Background()

	// Before any hook: dispatch is clean (isErr=false, the tool's own output).
	if out, isErr := r.Dispatch(ctx, "echo", json.RawMessage(`{"v":1}`)); isErr || !strings.Contains(out, `"v":1`) {
		t.Fatalf("pre-hook dispatch should be clean: isErr=%v out=%q", isErr, out)
	}

	// Register a blocking hook on the LIVE bus — no registry rebuild.
	unregister, err := bus.Register(Hook{
		Name:   "runtime-package",
		Domain: DomainGeneration,
		BeforeToolCall: func(tool string, in json.RawMessage) ToolCallDecision {
			if tool == "echo" {
				return ToolCallDecision{Block: true, Reason: "runtime-package: echo disabled"}
			}
			return ToolCallDecision{Input: in}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, isErr := r.Dispatch(ctx, "echo", json.RawMessage(`{"v":2}`))
	if !isErr || !strings.Contains(out, "echo disabled") {
		t.Fatalf("a hook registered mid-session must BLOCK the ALREADY-BUILT registry's next dispatch (isErr with the reason): isErr=%v out=%q", isErr, out)
	}

	// Unregister → the same registry dispatches clean again (detach restores).
	unregister()
	if out, isErr := r.Dispatch(ctx, "echo", json.RawMessage(`{"v":3}`)); isErr || strings.Contains(out, "disabled") {
		t.Fatalf("after unregister the live registry must dispatch clean: isErr=%v out=%q", isErr, out)
	}
}

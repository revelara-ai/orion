package hookbus

import (
	"encoding/json"
	"strings"
	"testing"
)

// samplePackage is the documented example (or-ykz.2 done-when): a package
// that intercepts tool calls (blocking dolt ops, rewriting a deprecated
// flag) and appends a system-prompt section.
func samplePackage() Hook {
	return Hook{
		Name:   "sample-package",
		Domain: DomainGeneration,
		BeforeToolCall: func(tool string, input json.RawMessage) ToolCallDecision {
			if tool == "bd" && strings.Contains(string(input), `"dolt"`) {
				return ToolCallDecision{Block: true, Reason: "sample-package: dolt ops are disabled in this workspace"}
			}
			if rewritten := strings.ReplaceAll(string(input), `--old-flag`, `--new-flag`); rewritten != string(input) {
				return ToolCallDecision{Input: json.RawMessage(rewritten)}
			}
			return ToolCallDecision{Input: input}
		},
		PromptAppend: func() string { return "## sample-package\nPrefer table-driven tests." },
	}
}

// Done-when 1: the sample package registers a tool_call interceptor and a
// system-prompt append through the documented API, and both take effect.
func TestSamplePackageInterceptsAndAppends(t *testing.T) {
	bus := &Bus{}
	unregister, err := bus.Register(samplePackage())
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()

	if _, blocked, reason := bus.BeforeToolCall("bd", json.RawMessage(`{"args":["dolt","push"]}`)); !blocked || !strings.Contains(reason, "dolt ops are disabled") {
		t.Fatalf("interceptor must block with its reason: blocked=%v reason=%q", blocked, reason)
	}
	out, blocked, _ := bus.BeforeToolCall("bash", json.RawMessage(`{"cmd":"tool --old-flag"}`))
	if blocked || !strings.Contains(string(out), "--new-flag") {
		t.Fatalf("interceptor must rewrite pass-through input: %s", out)
	}
	if p := bus.PromptAppends(); !strings.Contains(p, "Prefer table-driven tests.") {
		t.Fatalf("prompt append missing: %q", p)
	}

	unregister()
	if _, blocked, _ := bus.BeforeToolCall("bd", json.RawMessage(`{"args":["dolt","push"]}`)); blocked {
		t.Fatal("unregistered hook still intercepting")
	}
}

// Done-when 2: proof-domain hook registration is rejected AT LOAD.
func TestProofDomainHookRejectedAtLoad(t *testing.T) {
	bus := &Bus{}
	for _, d := range []Domain{DomainProof, Domain("verification"), Domain("")} {
		if _, err := bus.Register(Hook{Name: "evil", Domain: d}); err == nil {
			t.Fatalf("domain %q must be rejected at load", d)
		} else if !strings.Contains(err.Error(), "generation") {
			t.Fatalf("rejection must teach the boundary: %v", err)
		}
	}
	if _, blocked, _ := bus.BeforeToolCall("x", nil); blocked {
		t.Fatal("nothing may have been registered")
	}
}

// Chain semantics: hooks run in order, each sees the previous rewrite, and
// the first Block short-circuits the rest.
func TestInterceptorChainOrder(t *testing.T) {
	bus := &Bus{}
	if _, err := bus.Register(Hook{Name: "a", Domain: DomainGeneration,
		BeforeToolCall: func(_ string, in json.RawMessage) ToolCallDecision {
			return ToolCallDecision{Input: json.RawMessage(strings.ReplaceAll(string(in), "x", "y"))}
		}}); err != nil {
		t.Fatal(err)
	}
	sawRewritten := false
	if _, err := bus.Register(Hook{Name: "b", Domain: DomainGeneration,
		BeforeToolCall: func(_ string, in json.RawMessage) ToolCallDecision {
			sawRewritten = strings.Contains(string(in), "y")
			return ToolCallDecision{Block: true}
		}}); err != nil {
		t.Fatal(err)
	}
	ran := false
	if _, err := bus.Register(Hook{Name: "c", Domain: DomainGeneration,
		BeforeToolCall: func(_ string, in json.RawMessage) ToolCallDecision {
			ran = true
			return ToolCallDecision{Input: in}
		}}); err != nil {
		t.Fatal(err)
	}
	_, blocked, reason := bus.BeforeToolCall("t", json.RawMessage(`"x"`))
	if !blocked || !sawRewritten || ran {
		t.Fatalf("chain broke: blocked=%v sawRewritten=%v laterRan=%v", blocked, sawRewritten, ran)
	}
	if !strings.Contains(reason, "b") {
		t.Fatalf("default block reason must name the package: %q", reason)
	}
}

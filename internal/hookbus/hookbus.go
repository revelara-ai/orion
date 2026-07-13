// Package hookbus is the runtime extension API for installed packages
// (or-ykz.2, A1): a lifecycle hook bus that lets GENERATION-DOMAIN packages
// reshape the loop — intercept/rewrite/block tool calls and append system-
// prompt sections. The trust boundary is structural: registration itself
// rejects any hook claiming the proof domain (PRD Trust invariants 8-9 — the
// proof domain is immutable and never third-party-extensible), so a package
// cannot even LOAD a proof-side hook, let alone run one.
//
// # Hook API (the documented package surface)
//
// A package registers one Hook value on the Default bus at init/load time:
//
//	unregister, err := hookbus.Default.Register(hookbus.Hook{
//	    Name:   "my-package",
//	    Domain: hookbus.DomainGeneration,
//	    BeforeToolCall: func(tool string, input json.RawMessage) hookbus.ToolCallDecision {
//	        if tool == "bd" && strings.Contains(string(input), `"dolt"`) {
//	            return hookbus.ToolCallDecision{Block: true, Reason: "my-package: dolt ops are disabled here"}
//	        }
//	        return hookbus.ToolCallDecision{Input: input} // pass through (possibly rewritten)
//	    },
//	    PromptAppend: func() string { return "## my-package\nPrefer table-driven tests." },
//	})
//
// Hooks run in registration order; the first Block wins; each hook sees the
// previous hook's rewritten input. Nil callbacks are skipped.
//
// RUNTIME REGISTRATION (or-0sk, A2): the bus is consulted LIVE on every
// dispatch — a hook registered mid-session intercepts already-built tool
// registries' very next call (SetIntercept holds the bus method, not a
// snapshot), and unregistering restores them. Pinned by
// TestHookRegisteredMidSessionInterceptsLiveRegistry.
package hookbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Domain is the trust domain a hook declares. Only the generation domain is
// extensible; everything else is rejected at load.
type Domain string

const (
	DomainGeneration Domain = "generation"
	DomainProof      Domain = "proof"
)

// ToolCallDecision is a BeforeToolCall verdict: pass (possibly rewritten
// Input) or Block with a Reason the model reads back.
type ToolCallDecision struct {
	Input  json.RawMessage
	Block  bool
	Reason string
}

// Hook is one package's registration.
type Hook struct {
	Name           string
	Domain         Domain
	BeforeToolCall func(tool string, input json.RawMessage) ToolCallDecision
	PromptAppend   func() string
}

// Bus fans lifecycle events out to registered hooks.
type Bus struct {
	mu    sync.RWMutex
	hooks []Hook
}

// Default is the process bus packages register on at load time.
var Default = &Bus{}

// Register loads a hook. It FAILS for any non-generation domain — the proof
// domain is not extensible, by construction. Returns an unregister func.
func (b *Bus) Register(h Hook) (func(), error) {
	if strings.TrimSpace(h.Name) == "" {
		return nil, fmt.Errorf("hookbus: a hook needs a package name")
	}
	if h.Domain != DomainGeneration {
		return nil, fmt.Errorf("hookbus: package %q requested domain %q — only the generation domain is extensible (the proof domain is immutable, PRD trust invariants 8-9)", h.Name, h.Domain)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hooks = append(b.hooks, h)
	name := h.Name
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, x := range b.hooks {
			if x.Name == name {
				b.hooks = append(b.hooks[:i], b.hooks[i+1:]...)
				return
			}
		}
	}, nil
}

// BeforeToolCall runs the interceptor chain: hooks in registration order,
// each seeing the previous rewrite; the first Block short-circuits.
func (b *Bus) BeforeToolCall(tool string, input json.RawMessage) (json.RawMessage, bool, string) {
	b.mu.RLock()
	hooks := append([]Hook(nil), b.hooks...)
	b.mu.RUnlock()
	cur := input
	for _, h := range hooks {
		if h.BeforeToolCall == nil {
			continue
		}
		d := h.BeforeToolCall(tool, cur)
		if d.Block {
			reason := d.Reason
			if reason == "" {
				reason = "blocked by package " + h.Name
			}
			return cur, true, reason
		}
		if d.Input != nil {
			cur = d.Input
		}
	}
	return cur, false, ""
}

// PromptAppends concatenates every hook's system-prompt contribution.
func (b *Bus) PromptAppends() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var sb strings.Builder
	for _, h := range b.hooks {
		if h.PromptAppend == nil {
			continue
		}
		if s := strings.TrimSpace(h.PromptAppend()); s != "" {
			sb.WriteString("\n\n")
			sb.WriteString(s)
		}
	}
	return sb.String()
}

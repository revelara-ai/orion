// Package lang is the single registry of the languages Orion can generate AND
// prove (or-4y7, V2.2 polyglot). It is the source of truth for language
// capability: a language is "provable" — it won't be refused at ratify/build
// (or-hn15.5's directionBuildRefusal) — exactly when it is Registered here.
// completeness.provableDirections["direction.language"] is sourced from
// Registered(), so the capability manifest can never claim a language with
// nothing behind it: registering a language means supplying its Adapter.
//
// The Adapter surface grows as the polyglot seams land (or-4y7.2–.8 add the
// generation/toolchain/proof/hazard/export capabilities, each via its own
// per-subsystem registry keyed by the same language string). Go is the default:
// For("") resolves to the Go adapter, and the Go path is byte-identical.
package lang

import "sort"

// Adapter is a registered language's capability surface. In or-4y7.1 it just
// identifies the language; later slices extend it (and add sibling per-subsystem
// registries) without changing this contract for the Go default.
type Adapter interface {
	Language() string
}

var registry = map[string]Adapter{}

// Register adds a language adapter, keyed by its Language(). Idempotent; called
// from a language's wiring init.
func Register(a Adapter) { registry[a.Language()] = a }

// For returns the adapter for a language. "" resolves to the Go default. An
// UNREGISTERED non-empty language returns nil — NEVER silently the Go adapter,
// which would be the silent-emit-Go anti-pattern the direction rail refuses; a
// caller only reaches build after the refusal gate cleared the language, so it
// is registered by then.
func For(language string) Adapter {
	if language == "" {
		language = "go"
	}
	return registry[language]
}

// Registered returns the registered language names, sorted. It is the capability
// authority read by the direction gate.
func Registered() []string {
	out := make([]string, 0, len(registry))
	for l := range registry {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

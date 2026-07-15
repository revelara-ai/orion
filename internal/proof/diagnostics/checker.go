package diagnostics

import (
	"context"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// Checker is a language's fast-feedback static tier (or-4y7.4): compile + lint
// conformance (Check), entry-symbol conformance (CheckEntry), and unit-case
// reference validity (CheckUnitRefs) — run before the heavier proof modes so a
// non-compiling artifact fails in seconds. Resolved by language; the Go checker
// (go vet + go/parser) is the default and byte-identical to V2.0.
type Checker interface {
	Language() string
	Check(ctx context.Context, dir string) Result
	CheckEntry(dir, entry string) Result
	CheckUnitRefs(dir string, cases []spec.BehavioralCase) Result
}

var checkers = map[string]Checker{}

func registerChecker(c Checker) { checkers[c.Language()] = c }

// For resolves the checker for a language ("" → go). An unregistered language
// returns nil (its registration must accompany lang.Registered()); a Go contract
// always resolves the Go checker.
func For(language string) Checker {
	if language == "" {
		language = "go"
	}
	return checkers[language]
}

// goChecker is the default: the V2.0 go vet + go/parser diagnostics, verbatim.
type goChecker struct{}

func (goChecker) Language() string { return "go" }

func (goChecker) Check(ctx context.Context, dir string) Result { return Check(ctx, dir) }

func (goChecker) CheckEntry(dir, entry string) Result { return CheckEntry(dir, entry) }

func (goChecker) CheckUnitRefs(dir string, cases []spec.BehavioralCase) Result {
	return CheckUnitRefs(dir, cases)
}

func init() { registerChecker(goChecker{}) }

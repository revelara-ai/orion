package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/harness"
)

// genFunc adapts a func to Generator.
type genFunc func(ctx context.Context, req GenRequest, dir string) (Artifact, error)

func (f genFunc) Generate(ctx context.Context, req GenRequest, dir string) (Artifact, error) {
	return f(ctx, req, dir)
}

// TestFailoverOnOverloadContinues (or-ykz.13 DONE-WHEN): a rate-limited
// primary fails over to the next preset and the run CONTINUES; the event is
// surfaced with from/to/reason.
func TestFailoverOnOverloadContinues(t *testing.T) {
	var hops []string
	f := FailoverGenerator{
		Chain: []NamedGenerator{
			{Name: "claude", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
				return Artifact{}, errors.New("429 rate limit exceeded, retry later")
			})},
			{Name: "gemini", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
				return Artifact{Files: []string{"main.go"}, Narrative: "done"}, nil
			})},
		},
		OnFailover: func(from, to, reason string) { hops = append(hops, from+"→"+to+": "+reason) },
	}
	art, err := f.Generate(context.Background(), GenRequest{}, t.TempDir())
	if err != nil || len(art.Files) != 1 {
		t.Fatalf("the chain must continue on the fallback: %v %+v", err, art)
	}
	if len(hops) != 1 || !strings.Contains(hops[0], "claude→gemini") || !strings.Contains(hops[0], "429") {
		t.Fatalf("the failover must be surfaced with from/to/reason: %v", hops)
	}
}

// TestFailoverStopsOnHardError: a non-dependency error never fails over —
// the next vendor would hit the same wall.
func TestFailoverStopsOnHardError(t *testing.T) {
	second := 0
	f := FailoverGenerator{Chain: []NamedGenerator{
		{Name: "claude", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
			return Artifact{}, errors.New("invalid request: the spec slice is malformed")
		})},
		{Name: "gemini", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
			second++
			return Artifact{}, nil
		})},
	}}
	_, err := f.Generate(context.Background(), GenRequest{}, t.TempDir())
	if err == nil || second != 0 {
		t.Fatalf("a hard error must stop the chain (second called %d): %v", second, err)
	}
	if !strings.Contains(err.Error(), "agent claude") {
		t.Fatalf("the failing entry must be named: %v", err)
	}
}

// TestFailoverOnRefusalTriesAlternate (or-mvr.15's trigger class): a policy
// refusal gets ONE alternate-vendor attempt.
func TestFailoverOnRefusalTriesAlternate(t *testing.T) {
	f := FailoverGenerator{Chain: []NamedGenerator{
		{Name: "claude", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
			return Artifact{}, &harness.RefusalError{Text: "cannot help", StopDetail: "refusal"}
		})},
		{Name: "gemini", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
			return Artifact{Files: []string{"main.go"}}, nil
		})},
	}}
	if art, err := f.Generate(context.Background(), GenRequest{}, t.TempDir()); err != nil || len(art.Files) != 1 {
		t.Fatalf("a refusal must try the alternate vendor once: %v", err)
	}
}

// TestTurnDeadlineBoundsHungAgent (or-ykz.13, the load-bearing half): a hung
// agent turn is bounded by the per-turn deadline — the run never wedges — and
// the deadline is failover-eligible, so the chain advances past the hang.
func TestTurnDeadlineBoundsHungAgent(t *testing.T) {
	t.Setenv("ORION_AGENT_TURN_TIMEOUT", "60ms")
	f := FailoverGenerator{Chain: []NamedGenerator{
		{Name: "hung", Gen: genFunc(func(ctx context.Context, _ GenRequest, _ string) (Artifact, error) {
			<-ctx.Done() // hang until the deadline fires
			return Artifact{}, ctx.Err()
		})},
		{Name: "gemini", Gen: genFunc(func(context.Context, GenRequest, string) (Artifact, error) {
			return Artifact{Files: []string{"main.go"}}, nil
		})},
	}}
	start := time.Now()
	art, err := f.Generate(context.Background(), GenRequest{}, t.TempDir())
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a hung agent must be bounded by the turn deadline, took %v", elapsed)
	}
	if err != nil || len(art.Files) != 1 {
		t.Fatalf("the chain must advance past the hang: %v", err)
	}

	// Unset env → the 20m default (never zero, never wedge-forever).
	t.Setenv("ORION_AGENT_TURN_TIMEOUT", "")
	if TurnTimeout() != defaultTurnTimeout {
		t.Fatal("unset must use the bounded default")
	}
	t.Setenv("ORION_AGENT_TURN_TIMEOUT", "garbage")
	if TurnTimeout() != defaultTurnTimeout {
		t.Fatal("invalid must use the bounded default")
	}
}

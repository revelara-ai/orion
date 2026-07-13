package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/harness"
)

// Vendor-agent failover (or-ykz.13, A12): an ordered chain across vendor
// agents (e.g. claude → gemini → codex), with the two halves the audit named:
//
//   - PER-TURN DEADLINE (load-bearing): a vendor generation turn had NO
//     deadline — a hung agent wedged an unattended run forever. Every turn
//     is now bounded (ORION_AGENT_TURN_TIMEOUT, default 20m).
//   - FAILOVER on the DEPENDENCY class: deadline, rate-limit/overload/quota,
//     spawn/connect failure — plus the or-mvr.15 trigger class: a REFUSAL
//     retries once on the next vendor (a different model may not policy-block
//     the same prompt). Hard task errors never fail over (the next vendor
//     would hit the same wall).
//
// Credential rotation rides the chain: each entry is a PRESET, and presets
// carry their own env allowlists — two entries of the same agent with
// different auth profiles rotate credentials by construction.

// defaultTurnTimeout bounds one vendor generation turn.
const defaultTurnTimeout = 20 * time.Minute

// TurnTimeout resolves the per-turn deadline (ORION_AGENT_TURN_TIMEOUT, e.g.
// "10m"; unset/invalid → the 20m default).
func TurnTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ORION_AGENT_TURN_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultTurnTimeout
}

// NamedGenerator is one failover-chain entry.
type NamedGenerator struct {
	Name string
	Gen  Generator
}

// FailoverGenerator tries each chain entry in order, failing over ONLY on
// the dependency/refusal class. OnFailover surfaces every hop (visible
// notice + recording); it receives the failing entry, the next one, and why.
type FailoverGenerator struct {
	Chain      []NamedGenerator
	OnFailover func(from, to, reason string)
}

// Generate runs the chain: each entry gets one deadline-bounded turn.
func (f FailoverGenerator) Generate(ctx context.Context, req GenRequest, dir string) (Artifact, error) {
	if len(f.Chain) == 0 {
		return Artifact{}, fmt.Errorf("failover: empty agent chain")
	}
	timeout := TurnTimeout()
	var lastErr error
	for i, entry := range f.Chain {
		tctx, cancel := context.WithTimeout(ctx, timeout)
		art, err := entry.Gen.Generate(tctx, req, dir)
		cancel()
		if err == nil {
			return art, nil
		}
		lastErr = fmt.Errorf("agent %s: %w", entry.Name, err)
		if !FailoverEligible(err) || i == len(f.Chain)-1 {
			return Artifact{}, lastErr
		}
		next := f.Chain[i+1].Name
		if f.OnFailover != nil {
			f.OnFailover(entry.Name, next, err.Error())
		}
	}
	return Artifact{}, lastErr
}

// FailoverEligible classifies an error as the dependency/refusal class the
// chain may route around. Hard task errors return false — the next vendor
// would hit the same wall.
func FailoverEligible(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true // the hung-agent class: the turn deadline fired
	}
	var re *harness.RefusalError
	if errors.As(err, &re) {
		return true // or-mvr.15 trigger class: one alternate-vendor attempt
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"rate limit", "rate-limit", "429", "529", "overload", "quota",
		"capacity", "too many requests", "connect agent", "spawn",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

package conductor

import (
	"context"
	"log"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/reliabilityfloor"
)

// floorSource builds the reliability SignalSource. Overridable in tests.
// A nil store yields no source (fail-open): polaris.Consumer caches through the
// store and panics without one, and the floor must never take down the loop.
var floorSource = func(store *contextstore.Store) reliabilityfloor.SignalSource {
	if store == nil {
		return nil
	}
	return &reliabilityfloor.PolarisSource{
		Consumer: polaris.NewConsumer(mcpClientFromCredentials(store), store),
	}
}

// floorSignals retrieves up to 5 intent-keyed reliability signals (fail-open).
func floorSignals(ctx context.Context, store *contextstore.Store, projectID, intent string) []reliabilityfloor.Signal {
	return reliabilityfloor.Retrieve(ctx, floorSource(store), projectID, intent, 5)
}

// runFloorChecks runs the mechanizable signals' golangci-lint checks under safeenv,
// log-only, against the changed .go dirs.
func runFloorChecks(ctx context.Context, dir string, sigs []reliabilityfloor.Signal, changed []string) reliabilityfloor.LintResult {
	mech, _ := reliabilityfloor.Split(sigs)
	args := reliabilityfloor.LintArgs(mech, reliabilityfloor.GoDirs(changed))
	return reliabilityfloor.RunLint(ctx, dir, args)
}

func logFloor(res ChangeResult) {
	if len(res.FloorSignals) == 0 {
		log.Printf("reliability floor: no signals")
		return
	}
	log.Printf("reliability floor: %d signals; lint ran=%v exitOK=%v skipped=%q",
		len(res.FloorSignals), res.FloorLint.Ran, res.FloorLint.ExitOK, res.FloorLint.Skipped)
}

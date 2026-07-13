package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/harnesseval"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// cmdEvals implements `orion evals` (or-gb1.2): the tier-2 longitudinal
// harness eval — trend the deployed loop's own audit trail per model
// stratum and escalate deterministic regressions.
func cmdEvals(args []string) int {
	fs := flag.NewFlagSet("evals", flag.ContinueOnError)
	minN := fs.Int("min-n", 3, "minimum runs per window before a stratum can flag")
	margin := fs.Float64("margin", 0.15, "significance margin a degradation must exceed")
	escalate := fs.Bool("escalate", false, "file an inbox escalation per flagged regression")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
		proj, _, err := c.Store().CurrentOrLastDeliveredProjectSpec(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion evals: no project:", err)
			return 1
		}
		report, regs, err := harnesseval.Report(ctx, c.Store(), proj.ID, *minN, *margin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion evals:", err)
			return 1
		}
		fmt.Print(report)
		if *escalate && len(regs) > 0 {
			_ = c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
				for _, r := range regs {
					if _, e := tx.Escalations().CreateDetailed(ctx, proj.ID, "",
						"harness quality regression (tier-2 eval)", r.String()); e != nil {
						return e
					}
				}
				return nil
			})
			fmt.Printf("%d regression escalation(s) filed\n", len(regs))
		}
		return 0
	})
}

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof/formal"
)

// cmdDesign implements `orion design` (or-56c.2): the human side of the
// design-proof loop. `show` prints the drafted formal model with its hash;
// `ratify <hash>` is the human signature that anchors the EXACT reviewed
// bytes — only then does the model gain proof authority.
func cmdDesign(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion design: usage: orion design show | ratify <hash>")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion design:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion design:", err)
		return 1
	}
	defer store.Close()
	ctx := context.Background()

	proj, _, err := store.CurrentOrLastDeliveredProjectSpec(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion design: no project:", err)
		return 1
	}
	dm, ok, err := formal.LoadDesignModel(ctx, store, proj.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion design:", err)
		return 1
	}
	if !ok {
		fmt.Println("no design-proof model exists for this project (the trigger did not fire, or synthesis has not run)")
		return 0
	}

	switch args[0] {
	case "show":
		state := "DRAFT — awaiting ratification (orion design ratify " + dm.Hash + ")"
		if dm.Ratified {
			state = "RATIFIED by " + dm.RatifiedBy
		}
		fmt.Printf("design-proof model %s\nbackend: %s (installed: %v)\ntrigger: %s\nstate: %s\n\n%s\n",
			dm.Hash, dm.Backend, dm.BackendAvailable, dm.TriggerReason, state, dm.ModelText)
		return 0
	case "ratify":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "orion design ratify: the reviewed model's hash is required (see orion design show)")
			return 2
		}
		who := gitUserName()
		if who == "" {
			who = os.Getenv("USER")
		}
		ratified, err := formal.RatifyDesignModel(ctx, store, proj.ID, strings.TrimSpace(args[1]), who)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion design ratify:", err)
			return 1
		}
		fmt.Printf("design-proof model %s ratified by %s — it is now a proof-domain artifact\n", ratified.Hash, ratified.RatifiedBy)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "orion design: unknown subcommand:", args[0])
		return 2
	}
}

func gitUserName() string {
	out, err := exec.Command("git", "config", "user.name").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

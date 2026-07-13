package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/revelara-ai/orion/internal/harnessconfig"
)

// cmdHarness implements `orion harness` (or-mvr.6): the staged-rollout
// surface for the externalized harness config. A prompt/checklist/rules
// change ships as a CANDIDATE behind a versioned canary fraction; rollback
// and promote are each one command; none of it needs a recompile.
func cmdHarness(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion harness: usage: orion harness status | canary <version> <fraction> | rollback | promote | validate")
		return 2
	}
	switch args[0] {
	case "status":
		fmt.Println(harnessconfig.CanaryStatus())
		fmt.Println("config dir:", harnessconfig.Dir())
		return 0
	case "canary":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "orion harness canary: usage: orion harness canary <version> <fraction> — put candidate files in", harnessconfig.Dir()+"/candidate/")
			return 2
		}
		version, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion harness canary: version must be an integer:", err)
			return 2
		}
		fraction, err := strconv.ParseFloat(args[2], 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion harness canary: fraction must be a number in [0,1]:", err)
			return 2
		}
		if err := harnessconfig.StartCanary(version, fraction); err != nil {
			fmt.Fprintln(os.Stderr, "orion harness canary:", err)
			return 1
		}
		fmt.Printf("canary v%d started at fraction %v — candidate files read from %s/candidate/\n", version, fraction, harnessconfig.Dir())
		return 0
	case "rollback":
		if err := harnessconfig.Rollback(); err != nil {
			fmt.Fprintln(os.Stderr, "orion harness rollback:", err)
			return 1
		}
		fmt.Println("canary rolled back — every site reads the stable config (candidate files preserved for the post-mortem)")
		return 0
	case "promote":
		if err := harnessconfig.Promote(); err != nil {
			fmt.Fprintln(os.Stderr, "orion harness promote:", err)
			return 1
		}
		fmt.Println("candidate promoted to stable — canary ended")
		return 0
	case "validate":
		errs := harnessconfig.Validate()
		if len(errs) == 0 {
			fmt.Println("harness config valid (absent files use compiled defaults)")
			return 0
		}
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "invalid:", e)
		}
		return 1
	default:
		fmt.Fprintln(os.Stderr, "orion harness: unknown subcommand:", args[0])
		return 2
	}
}

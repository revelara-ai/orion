package main

import (
	"context"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/dependencyprovenance"
)

// cmdDeps implements `orion deps verify <pkg>`: a hallucinated or anomalous
// dependency exits non-zero (rejected before it can enter the build).
func cmdDeps(args []string) int {
	if len(args) < 2 || args[0] != "verify" {
		fmt.Fprintln(os.Stderr, "orion deps: usage: orion deps verify <module>")
		return 2
	}
	pkg := args[1]
	v := dependencyprovenance.Verify(context.Background(), dependencyprovenance.NewProxyResolver(), pkg, dependencyprovenance.DefaultPolicy())
	if !v.OK {
		fmt.Fprintf(os.Stderr, "orion deps verify: REJECTED %s — %s\n", pkg, v.Reason)
		return 1
	}
	fmt.Printf("orion deps verify: ok %s\n", pkg)
	return 0
}

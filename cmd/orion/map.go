package main

import (
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/brownfield"
)

// cmdMap prints the deterministic codebase map for a repo (the structural
// understanding the grill reads to ground a brownfield spec/change). Usage:
//
//	orion map [dir]   (dir defaults to .)
func cmdMap(args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if dir == "." {
		if abs, err := os.Getwd(); err == nil {
			dir = abs
		}
	}
	fmt.Print(brownfield.ScanRepoMap(dir).Digest())
	return 0
}

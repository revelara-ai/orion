package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/actuation"
)

// cmdRedButton implements the standalone emergency stop (or-v9f.14) — decoupled
// from `orion conductor stop`, so a run can be halted and resumed without
// killing the process:
//
//	orion redbutton engage    halt all mutating actuation (dispatch, export, git/bd writes)
//	orion redbutton release   restore actuation
//	orion redbutton status    engaged | clear
func cmdRedButton(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion redbutton: usage: orion redbutton engage|release|status")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion redbutton:", err)
		return 1
	}
	rb := actuation.RedButton{Path: filepath.Join(dir, "red_button")}
	switch args[0] {
	case "engage":
		if err := rb.Engage(); err != nil {
			fmt.Fprintln(os.Stderr, "orion redbutton engage:", err)
			return 1
		}
		fmt.Println("red button ENGAGED — no new cluster dispatch, no export/git/bd writes; in-flight proofs finish; release with: orion redbutton release")
		return 0
	case "release":
		if err := rb.Release(); err != nil {
			fmt.Fprintln(os.Stderr, "orion redbutton release:", err)
			return 1
		}
		fmt.Println("red button released — actuation restored")
		return 0
	case "status":
		if rb.Engaged() {
			fmt.Println("engaged")
		} else {
			fmt.Println("clear")
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "orion redbutton: unknown subcommand", args[0])
		return 2
	}
}

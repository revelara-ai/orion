// Command orion-cli is the operator and dogfood CLI for Orion.
//
// In E0-1 it supports only --version. Subcommands (run, detect, roundtrip)
// land in subsequent epics.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/version"
)

const progName = "orion-cli"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [--version]\n", progName)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	flag.Usage()
	os.Exit(2)
}

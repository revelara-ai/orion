package main

import (
	"fmt"
	"io"

	"github.com/revelara-ai/orion/internal/version"
)

func printVersion(w io.Writer) {
	_, _ = fmt.Fprintln(w, version.String())
}

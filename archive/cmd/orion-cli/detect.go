package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/revelara-ai/orion/internal/detection"
)

// detectCmd implements `orion-cli detect --repo=... --service=... [--format=text|json] [--rvl-binary=rvl]`
type detectCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newDetectCmd(stdout, stderr io.Writer) *detectCmd {
	return &detectCmd{stdout: stdout, stderr: stderr}
}

func (c *detectCmd) Name() string { return "detect" }

func (c *detectCmd) Synopsis() string {
	return "Run rvl-cli scanner against a repo and emit findings"
}

func (c *detectCmd) Run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
	fs.SetOutput(c.stderr)

	repo := fs.String("repo", "", "absolute path to the cloned target repo (required)")
	service := fs.String("service", "", "service name (required)")
	format := fs.String("format", "text", "output format: text | json")
	rvlBinary := fs.String("rvl-binary", "", "rvl executable name or path (default: 'rvl' from $PATH)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(c.stderr, "Usage: %s detect --repo=<path> --service=<name> [--format=text|json] [--rvl-binary=<path>]\n\n", progName)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already wrote the error; usage is printed for -h
		return 2
	}

	if *repo == "" || *service == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: --repo and --service are both required")
		fs.Usage()
		return 2
	}

	if *format != "text" && *format != "json" {
		_, _ = fmt.Fprintf(c.stderr, "error: --format=%s is not supported (want 'text' or 'json')\n", *format)
		return 2
	}

	scanner := detection.NewScanner(detection.ScannerConfig{
		RvlBinary: *rvlBinary,
	})

	findings, stats, err := scanner.Run(ctx, detection.ScanOptions{
		RepoPath: *repo,
		Service:  *service,
	})
	if err != nil {
		switch {
		case errors.Is(err, detection.ErrInvalidOptions):
			_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
			return 2
		case errors.Is(err, detection.ErrSubprocessFailure):
			_, _ = fmt.Fprintf(c.stderr, "error: rvl subprocess failed: %v\n", err)
			return 3
		case errors.Is(err, detection.ErrParseFailure):
			_, _ = fmt.Fprintf(c.stderr, "error: rvl produced unparseable output: %v\n", err)
			return 4
		default:
			_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
			return 1
		}
	}

	if *format == "json" {
		envelope := struct {
			Stats    detection.ScanStats `json:"stats"`
			Findings []detection.Finding `json:"findings"`
		}{
			Stats:    stats,
			Findings: findings,
		}
		enc := json.NewEncoder(c.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(envelope); err != nil {
			_, _ = fmt.Fprintf(c.stderr, "error: encoding JSON: %v\n", err)
			return 1
		}
		return 0
	}

	// text format (default): tab-aligned summary
	tw := tabwriter.NewWriter(c.stdout, 2, 2, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "FILE\tLINE\tSEVERITY\tSLUG\tCATEGORY\tCONTROLS\n")
	for _, f := range findings {
		controls := ""
		for i, code := range f.ControlCodes {
			if i > 0 {
				controls += ","
			}
			controls += code
		}
		sev := f.Confidence
		if f.Impact != "" {
			sev = f.Impact
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n", f.File, f.Line, sev, f.Slug, f.Category, controls)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(c.stdout, "\n%d finding(s) total\n", stats.FindingsTotal)
	return 0
}

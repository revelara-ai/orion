// Package preflight is Orion's startup missing-tool check (or-f96q): use
// whatever version of a dependent tool the developer already has (PATH-first —
// Orion holds no opinion on system vs brew vs pyenv); when one is missing,
// offer to install it interactively — "you're missing {foo}, install now?
// [Y/n]" — and run the install only on confirmation. Declines persist, so "n"
// means don't-ask-again (doctor still reports the gap); non-TTY runs (CI,
// conductor, headless verbs) are silently skipped and never block on stdin.
package preflight

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Recipe describes one dependent tool and how to install it. Exactly one of
// Pkg/GoModule/SuggestURL drives the offer: a package-manager install, a
// `go install`, or a printed pointer (no exec) for tools without a recipe
// Orion trusts.
type Recipe struct {
	Tool       string            // binary name resolved on PATH
	Why        string            // one-line consequence while missing
	Pkg        map[string]string // package manager → package name
	GoModule   string            // non-empty → `go install <module>` (requires go)
	SuggestURL string            // fallback pointer when nothing is executable
}

// recipes are the dependent tools preflight covers, in prompt order. Extend
// here as new language arms land (python3/node join with their adapters).
var recipes = []Recipe{
	{
		Tool: "bwrap",
		Why:  "proof sandbox isolation (without it, Python/lint proof execs refuse and Go proofs need an explicit unsafe override)",
		Pkg: map[string]string{
			"apt-get": "bubblewrap", "dnf": "bubblewrap", "pacman": "bubblewrap",
			"zypper": "bubblewrap", "apk": "bubblewrap",
		},
	},
	{
		Tool:     "gopls",
		Why:      "pre-proof LSP diagnostics (skipped while missing)",
		GoModule: "golang.org/x/tools/gopls@latest",
	},
	{
		Tool:       "bd",
		Why:        "beads issue tracking",
		SuggestURL: "https://github.com/gastownhall/beads",
	},
}

// pmOrder is the package-manager probe order; the first one present wins.
var pmOrder = []string{"apt-get", "dnf", "pacman", "zypper", "apk", "brew"}

// pmArgv builds the install command for a package manager. brew never uses
// sudo; everything else does (the terminal is interactive here, so sudo can
// prompt for its password normally).
func pmArgv(pm, pkg string) []string {
	switch pm {
	case "apt-get":
		return []string{"sudo", "apt-get", "install", "-y", pkg}
	case "dnf":
		return []string{"sudo", "dnf", "install", "-y", pkg}
	case "pacman":
		return []string{"sudo", "pacman", "-S", "--noconfirm", pkg}
	case "zypper":
		return []string{"sudo", "zypper", "--non-interactive", "install", pkg}
	case "apk":
		return []string{"sudo", "apk", "add", pkg}
	case "brew":
		return []string{"brew", "install", pkg}
	}
	return nil
}

// Options injects every external so Run is fully testable (no real installs,
// no real TTY in the suite).
type Options struct {
	LookPath  func(string) (string, error) // PATH lookup (defaults to exec.LookPath)
	IsTTY     bool                         // prompt only when stdin is a terminal
	In        io.Reader                    // prompt input (stdin)
	Out       io.Writer                    // prompt output (stderr — never the TUI's stdout)
	Runner    func(argv []string) error    // executes an install command
	PrefsPath string                       // decline persistence ("" disables)
	AssumeYes bool                         // --yes: install without prompting
}

// Outcome reports what happened for one missing tool.
type Outcome struct {
	Tool      string
	Installed bool
	Declined  bool
	Err       error
}

// Run checks every recipe against the host and offers to install what's
// missing. It returns an outcome per missing tool that was acted on (installed,
// declined, or failed); a fully provisioned host returns nothing and prints
// nothing.
func Run(opts Options) []Outcome {
	if !opts.IsTTY && !opts.AssumeYes {
		// Headless without explicit consent: never block on stdin, never touch
		// the host. --yes (doctor --fix --yes in CI) is that consent and reads
		// nothing, so it may proceed TTY-less.
		return nil
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	declined := loadDeclined(opts.PrefsPath)
	reader := bufio.NewReader(opts.In)
	pm := ""
	for _, cand := range pmOrder {
		if _, err := lookPath(cand); err == nil {
			pm = cand
			break
		}
	}

	var out []Outcome
	for _, r := range recipes {
		if _, err := lookPath(r.Tool); err == nil {
			continue // the user's version, whatever it is, wins
		}
		if declined[r.Tool] {
			continue // "n" means don't-ask-again
		}
		argv := installArgv(r, pm, lookPath)
		if argv == nil {
			// Nothing Orion trusts to exec — point, don't push.
			hint := r.SuggestURL
			if hint == "" {
				hint = "no known installer for this host"
			}
			fmt.Fprintf(opts.Out, "orion: %s not found — %s (see %s)\n", r.Tool, r.Why, hint)
			continue
		}
		if !opts.AssumeYes {
			fmt.Fprintf(opts.Out, "orion: you're missing %s — %s.\nInstall now via `%s`? [Y/n] ", r.Tool, r.Why, strings.Join(argv, " "))
			line, _ := reader.ReadString('\n')
			ans := strings.ToLower(strings.TrimSpace(line))
			if ans != "" && ans != "y" && ans != "yes" {
				declined[r.Tool] = true
				saveDeclined(opts.PrefsPath, declined)
				fmt.Fprintf(opts.Out, "orion: skipping %s (won't ask again; `orion doctor` still reports it)\n", r.Tool)
				out = append(out, Outcome{Tool: r.Tool, Declined: true})
				continue
			}
		}
		if err := opts.Runner(argv); err != nil {
			fmt.Fprintf(opts.Out, "orion: installing %s failed: %v\n", r.Tool, err)
			out = append(out, Outcome{Tool: r.Tool, Err: err})
			continue
		}
		fmt.Fprintf(opts.Out, "orion: %s installed\n", r.Tool)
		out = append(out, Outcome{Tool: r.Tool, Installed: true})
	}
	return out
}

// installArgv picks the executable install command for a recipe: its
// package-manager package under the detected manager, else its go-install
// module when go is present. nil = nothing trusted to exec.
func installArgv(r Recipe, pm string, lookPath func(string) (string, error)) []string {
	if pm != "" && r.Pkg[pm] != "" {
		return pmArgv(pm, r.Pkg[pm])
	}
	if r.GoModule != "" {
		if _, err := lookPath("go"); err == nil {
			return []string{"go", "install", r.GoModule}
		}
	}
	return nil
}

// ExecRunner runs an install command interactively (inherited stdio, so sudo
// can prompt). It is the production Runner; tests inject fakes.
func ExecRunner(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 G702 -- argv comes from the compiled recipe table above, never from user or generated content
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// prefs is the decline-persistence document.
type prefs struct {
	Declined []string `json:"declined"`
}

func loadDeclined(path string) map[string]bool {
	m := map[string]bool{}
	if path == "" {
		return m
	}
	data, err := os.ReadFile(path) // #nosec G304 -- orion's own data-dir prefs file
	if err != nil {
		return m
	}
	var p prefs
	if json.Unmarshal(data, &p) == nil {
		for _, t := range p.Declined {
			m[t] = true
		}
	}
	return m
}

func saveDeclined(path string, m map[string]bool) {
	if path == "" {
		return
	}
	p := prefs{}
	for t, d := range m {
		if d {
			p.Declined = append(p.Declined, t)
		}
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

package preflight

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// lookWith returns a LookPath that finds only the named binaries.
func lookWith(present ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New(name + " not found")
	}
}

// TestPromptsAndInstallsOnYes (or-f96q): a missing installable tool is offered
// with the exact package-manager argv, and "y" runs it.
func TestPromptsAndInstallsOnYes(t *testing.T) {
	var ran [][]string
	var out bytes.Buffer
	res := Run(Options{
		LookPath:  lookWith("apt-get", "go", "gopls", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader("y\n"),
		Out:       &out,
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 1 || strings.Join(ran[0], " ") != "sudo apt-get install -y bubblewrap" {
		t.Fatalf("want the apt bubblewrap install argv, got %v", ran)
	}
	if !strings.Contains(out.String(), "bwrap") || !strings.Contains(out.String(), "[Y/n]") {
		t.Fatalf("the prompt must name the tool and offer [Y/n]:\n%s", out.String())
	}
	if len(res) == 0 {
		t.Fatal("want an outcome for the installed tool")
	}
}

// TestEmptyAnswerDefaultsToYes (or-f96q): pressing Enter accepts ([Y/n] — capital Y).
func TestEmptyAnswerDefaultsToYes(t *testing.T) {
	var ran [][]string
	Run(Options{
		LookPath:  lookWith("apt-get", "go", "gopls", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader("\n"),
		Out:       &bytes.Buffer{},
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 1 {
		t.Fatalf("empty answer must default to yes; runner calls: %v", ran)
	}
}

// TestDeclinePersistsAndIsNotReasked (or-f96q): "n" must not install, and the
// decline is remembered — the next Run does not re-prompt for the same tool.
func TestDeclinePersistsAndIsNotReasked(t *testing.T) {
	prefs := filepath.Join(t.TempDir(), "toolprefs.json")
	look := lookWith("apt-get", "go", "gopls", "protoc")
	var ran [][]string
	runner := func(argv []string) error { ran = append(ran, argv); return nil }

	var out1 bytes.Buffer
	Run(Options{LookPath: look, IsTTY: true, In: strings.NewReader("n\n"), Out: &out1, Runner: runner, PrefsPath: prefs})
	if len(ran) != 0 {
		t.Fatalf("'n' must not install, ran %v", ran)
	}
	if !strings.Contains(out1.String(), "[Y/n]") {
		t.Fatalf("first run must have prompted:\n%s", out1.String())
	}

	var out2 bytes.Buffer
	Run(Options{LookPath: look, IsTTY: true, In: strings.NewReader("y\n"), Out: &out2, Runner: runner, PrefsPath: prefs})
	if strings.Contains(out2.String(), "[Y/n]") {
		t.Fatalf("a declined tool must NOT be re-asked:\n%s", out2.String())
	}
	if len(ran) != 0 {
		t.Fatalf("a declined tool must NOT be installed later, ran %v", ran)
	}
}

// TestNonTTYNeverPrompts (or-f96q): headless/CI/conductor runs must never block
// on stdin — no prompt, no install, no output.
func TestNonTTYNeverPrompts(t *testing.T) {
	var ran [][]string
	var out bytes.Buffer
	Run(Options{
		LookPath:  lookWith("apt-get", "protoc"),
		IsTTY:     false,
		In:        strings.NewReader("y\n"),
		Out:       &out,
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 0 || out.Len() != 0 {
		t.Fatalf("non-TTY must be silent and side-effect free; ran=%v out=%q", ran, out.String())
	}
}

// TestAssumeYesInstallsWithoutReadingStdin (or-f96q): --yes (doctor --fix in CI)
// installs every missing installable tool without consuming stdin.
func TestAssumeYesInstallsWithoutReadingStdin(t *testing.T) {
	var ran [][]string
	Run(Options{
		LookPath:  lookWith("apt-get", "go", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader(""), // nothing to read — must not matter
		Out:       &bytes.Buffer{},
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
		AssumeYes: true,
	})
	// bwrap via apt + gopls via go install (both missing here).
	if len(ran) != 2 {
		t.Fatalf("want both installable tools installed under --yes, ran %v", ran)
	}
}

// TestAssumeYesWorksWithoutTTY (or-f96q): `doctor --fix --yes` in CI has no
// terminal but is explicit consent — it installs, reading nothing.
func TestAssumeYesWorksWithoutTTY(t *testing.T) {
	var ran [][]string
	Run(Options{
		LookPath:  lookWith("apt-get", "go", "gopls", "protoc"),
		IsTTY:     false,
		In:        strings.NewReader(""),
		Out:       &bytes.Buffer{},
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
		AssumeYes: true,
	})
	if len(ran) != 1 {
		t.Fatalf("--yes must install without a TTY, ran %v", ran)
	}
}

// TestGoInstallRecipe (or-f96q): a go-ecosystem tool (gopls) installs via
// `go install` when go is present.
func TestGoInstallRecipe(t *testing.T) {
	var ran [][]string
	Run(Options{
		LookPath:  lookWith("apt-get", "go", "bwrap", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader("y\n"),
		Out:       &bytes.Buffer{},
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 1 || strings.Join(ran[0], " ") != "go install golang.org/x/tools/gopls@latest" {
		t.Fatalf("want the gopls go-install argv, got %v", ran)
	}
}

// TestNothingMissingIsSilent (or-f96q): a fully provisioned host gets no output.
func TestNothingMissingIsSilent(t *testing.T) {
	var ran [][]string
	var out bytes.Buffer
	res := Run(Options{
		LookPath:  lookWith("apt-get", "go", "bwrap", "gopls", "bd", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader(""),
		Out:       &out,
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 0 || out.Len() != 0 || len(res) != 0 {
		t.Fatalf("nothing missing must mean no prompts and no installs; ran=%v out=%q res=%v", ran, out.String(), res)
	}
}

// TestSuggestOnlyToolIsNeverExecuted (or-f96q): a tool without a trusted install
// recipe (bd) gets a pointer, never an exec.
func TestSuggestOnlyToolIsNeverExecuted(t *testing.T) {
	var ran [][]string
	var out bytes.Buffer
	Run(Options{
		LookPath:  lookWith("apt-get", "go", "bwrap", "gopls", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader("y\ny\n"),
		Out:       &out,
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 0 {
		t.Fatalf("a suggest-only tool must never exec an installer, ran %v", ran)
	}
	if !strings.Contains(out.String(), "bd") {
		t.Fatalf("the suggestion must name the tool:\n%s", out.String())
	}
}

// TestNoPackageManagerFallsBackToSuggestion (or-f96q): with no known package
// manager on the host, a PM-installable tool degrades to a printed suggestion.
func TestNoPackageManagerFallsBackToSuggestion(t *testing.T) {
	var ran [][]string
	var out bytes.Buffer
	Run(Options{
		LookPath:  lookWith("go", "gopls", "bd", "protoc"), // no apt/dnf/… and no bwrap
		IsTTY:     true,
		In:        strings.NewReader("y\n"),
		Out:       &out,
		Runner:    func(argv []string) error { ran = append(ran, argv); return nil },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	if len(ran) != 0 {
		t.Fatalf("no package manager → nothing to exec, ran %v", ran)
	}
	if !strings.Contains(out.String(), "bwrap") {
		t.Fatalf("the missing tool must still be surfaced:\n%s", out.String())
	}
}

// TestFailedInstallReportsFailure (or-f96q): a runner error is an explicit failed
// outcome, never a silent success.
func TestFailedInstallReportsFailure(t *testing.T) {
	var out bytes.Buffer
	res := Run(Options{
		LookPath:  lookWith("apt-get", "go", "gopls", "protoc"),
		IsTTY:     true,
		In:        strings.NewReader("y\n"),
		Out:       &out,
		Runner:    func([]string) error { return errors.New("boom") },
		PrefsPath: filepath.Join(t.TempDir(), "toolprefs.json"),
	})
	var failed bool
	for _, r := range res {
		if r.Tool == "bwrap" && !r.Installed && r.Err != nil {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("a failed install must surface as a failed outcome, got %+v", res)
	}
}

// or-mkxd: protoc is a generation-time tool (never needed under proof — the
// .pb.go files are committed as source); PATH-first with a package recipe.
func TestProtocRecipePresent(t *testing.T) {
	for _, r := range recipes {
		if r.Tool == "protoc" {
			if len(r.Pkg) == 0 {
				t.Fatal("protoc recipe must carry package-manager installs")
			}
			return
		}
	}
	t.Fatal("protoc recipe missing from preflight")
}

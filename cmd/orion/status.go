package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/tui"
)

// cmdStatus implements `orion status` (or-gik.4): print the full init status banner — the SAME
// renderer the TUI uses — with a LIVE Polaris reachability probe. The live probe is the one
// place the CLI deliberately differs from the network-free TUI launch banner; blocking is
// acceptable here. Informational: it always exits 0 (the polaris/subsystem rows report state;
// `orion doctor` is the gate that flips an exit code).
func cmdStatus(_ []string) int {
	dir, _ := doctorDataDir()
	rep := health.Probe(health.Options{
		DataDir:  dir,
		LookPath: exec.LookPath,
		AgentEnv: os.Getenv("ORION_AGENT"),
		Polaris:  livePolarisCheck,
	})
	fmt.Print(tui.Render(rep, bannerIdentity(), terminalWidth()))
	return 0
}

// bannerIdentity builds the banner's identity block from the local environment.
func bannerIdentity() tui.Identity {
	cwd, _ := os.Getwd()
	return tui.Identity{
		Version: resolveVersion(),
		Branch:  gitBranch(),
		Brain:   brainLabel(),
		Cwd:     abbrevHome(cwd),
		Session: time.Now().Format("20060102_1504"),
		Budget:  "0 tok · $0.00",
	}
}

// abbrevHome replaces the home-dir prefix with ~ for a compact cwd display.
func abbrevHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// brainLabel mirrors the TUI's conductorBrain selection: native + model when ANTHROPIC_API_KEY
// is set, else offline/deterministic.
func brainLabel() string {
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		model := os.Getenv("ORION_MODEL")
		if model == "" {
			model = llm.DefaultAnthropicModel
		}
		return "native · " + model + " · Anthropic"
	}
	return "offline — deterministic"
}

func gitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "—"
	}
	return strings.TrimSpace(string(out))
}

// terminalWidth returns the stdout terminal width, or 100 for a non-TTY (piped) status.
func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 100
}

// livePolarisCheck reports Polaris reachability with a live Me() call (CLI path — blocking is
// acceptable, unlike the TUI launch banner's cached-credential check).
func livePolarisCheck() health.Check {
	dir, err := credentialsDir()
	if err != nil {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: "no credentials dir"}
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: err.Error()}
	}
	tok, ok, err := store.Load()
	if err != nil || !ok {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: "not logged in"}
	}
	id, err := polaris.NewClient(tok.BaseURL).Me(context.Background(), tok.AccessToken)
	if err != nil {
		return health.Check{Name: "revelara.ai", Status: health.Warn, Detail: "cached credential present for " + tok.BaseURL + " (server unreachable)"}
	}
	who := id.Email
	if who == "" {
		who = "authenticated"
	}
	return health.Check{Name: "revelara.ai", Status: health.OK, Detail: "connected as " + who}
}

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/llmsetup"
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
		Budget:  spendSummary(),
	}
}

// abbrevHome replaces the home-dir prefix with ~ for a compact cwd display.
func abbrevHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// brainLabel mirrors the TUI's conductorBrain selection via llmsetup: native +
// model + provider when a brain resolves, else offline/deterministic.
func brainLabel() string {
	b := llmsetup.Select()
	if b.Provider == nil {
		return "offline — deterministic"
	}
	return "native · " + b.Model + " · " + b.ProviderName
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

// spendSummary reads the persistent ledger (or-v9f.28): cumulative project
// spend split by role/model — no more hardcoded zeros.
func spendSummary() string {
	dir, err := resolveDataDir()
	if err != nil {
		return "0 tok · $0.00"
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		return "0 tok · $0.00"
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	proj, _, err := store.CurrentOrLastDeliveredProjectSpec(ctx)
	if err != nil {
		return "0 tok · $0.00"
	}
	tok, dol, err := store.SumSpend(ctx, proj.ID)
	if err != nil || tok == 0 {
		return "0 tok · $0.00"
	}
	out := fmt.Sprintf("%s tok · $%.2f", compactTokens(tok), dol)
	if rows, rerr := store.SpendByRole(ctx, proj.ID); rerr == nil && len(rows) > 0 {
		parts := make([]string, 0, 3)
		for i, r := range rows {
			if i >= 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s $%.2f", r.Role, r.Dollars))
		}
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	return out
}

func compactTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

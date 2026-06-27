package agentruntime

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
)

// GenRequest describes what to build, derived from the executable spec /
// ProofObligation. It is intentionally open-ended: the generator writes arbitrary
// code, not one template.
type GenRequest struct {
	Description string
	Module      string
	Route       string
	Format      string
	TimeZone    string
	Port        int
}

// Artifact is generation output: the worktree dir and the files written into it.
type Artifact struct {
	Dir   string
	Files []string
	// Narrative is the agent's own self-report (its streamed reasoning/messages) during the
	// turn. It is an UNTRUSTED generation-domain artifact — when a build fails it is recorded
	// quarantined (generation-tier) so the next attempt sees "what the agent thought went
	// wrong" without it ever reaching a proof prompt (or-7mr / or-hd3.5).
	Narrative string
}

// Generator turns a GenRequest into code on disk.
type Generator interface {
	Generate(ctx context.Context, req GenRequest, dir string) (Artifact, error)
}

// ACPSession is the minimal ACP surface the generator drives (satisfied by
// *acp.Client).
type ACPSession interface {
	Initialize(ctx context.Context) error
	SessionNew(ctx context.Context) (string, error)
	Prompt(ctx context.Context, sessionID, text string, onUpdate func(acp.Update)) (acp.PromptResult, error)
}

// ACPDriver connects to an agent scoped to root and returns the session plus a
// cleanup func.
type ACPDriver func(ctx context.Context, root string) (ACPSession, func(), error)

// AgentGenerator drives a spawned vendor agent over ACP to WRITE code from the
// spec — this is what lets Orion build new applications, not one template (SPEC
// §3). The agent writes files via fs/write_text_file, served by a worktree-scoped
// fs. Proof gates the output independently; test authoring is a separate
// proof-domain agent (generation ⊥ proof).
type AgentGenerator struct {
	Driver ACPDriver
}

// Generate runs one generation turn and returns the files the agent wrote.
func (g AgentGenerator) Generate(ctx context.Context, req GenRequest, dir string) (Artifact, error) {
	if g.Driver == nil {
		return Artifact{}, fmt.Errorf("generator: no ACP driver configured")
	}
	sess, cleanup, err := g.Driver(ctx, dir)
	if err != nil {
		return Artifact{}, fmt.Errorf("generator: connect agent: %w", err)
	}
	defer cleanup()

	if err := sess.Initialize(ctx); err != nil {
		return Artifact{}, fmt.Errorf("generator: initialize: %w", err)
	}
	sid, err := sess.SessionNew(ctx)
	if err != nil {
		return Artifact{}, fmt.Errorf("generator: session/new: %w", err)
	}
	// Accumulate the agent's streamed text (its self-report) as the narrative — previously
	// discarded with an empty callback (or-7mr).
	var narrative strings.Builder
	if _, err := sess.Prompt(ctx, sid, RenderPrompt(req), func(u acp.Update) {
		if t := strings.TrimSpace(u.Text); t != "" {
			if narrative.Len() > 0 {
				narrative.WriteByte('\n')
			}
			narrative.WriteString(t)
		}
	}); err != nil {
		return Artifact{}, fmt.Errorf("generator: prompt: %w", err)
	}

	files, err := listFiles(dir)
	if err != nil {
		return Artifact{}, err
	}
	if len(files) == 0 {
		return Artifact{}, fmt.Errorf("generator: agent produced no files")
	}
	return Artifact{Dir: dir, Files: files, Narrative: narrative.String()}, nil
}

// RenderPrompt renders the GenRequest into agent instructions. The agent writes
// the program into the working directory via fs/write_text_file.
func RenderPrompt(req GenRequest) string {
	var b strings.Builder
	b.WriteString("Write a complete, compilable Go program for the following specification.\n")
	if req.Description != "" {
		b.WriteString("Goal: " + req.Description + "\n")
	}
	if req.Module != "" {
		b.WriteString("Go module: " + req.Module + "\n")
	}
	if req.Route != "" {
		b.WriteString("HTTP route: " + req.Route + "\n")
	}
	if req.Format != "" {
		b.WriteString("Response format: " + req.Format + "\n")
	}
	if req.TimeZone != "" {
		b.WriteString("Timezone: " + req.TimeZone + "\n")
	}
	if req.Port != 0 {
		b.WriteString(fmt.Sprintf("Port: %d\n", req.Port))
	}
	b.WriteString("Write all files into the working directory via fs/write_text_file, then end the turn.\n")
	return b.String()
}

// SpawnDriver is the production ACPDriver: it spawns a vendor agent from a preset
// and drives it over ACP, serving fs/* scoped to the worktree root.
func SpawnDriver(preset Preset, role string, gate acp.PermissionGate) ACPDriver {
	return func(ctx context.Context, root string) (ACPSession, func(), error) {
		agent, err := Spawn(ctx, SpawnConfig{Preset: preset, Role: role, Dir: root}, nil)
		if err != nil {
			return nil, func() {}, err
		}
		runCtx, cancel := context.WithCancel(ctx)
		client := acp.NewClient(agent.Stdout, agent.Stdin, gate, acp.ScopedFS{Root: root})
		go client.Run(runCtx)
		cleanup := func() {
			cancel()
			agent.Stop(2 * time.Second)
		}
		return client, cleanup, nil
	}
}

func listFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

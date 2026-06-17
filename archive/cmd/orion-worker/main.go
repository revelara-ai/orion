// Command orion-worker is the per-issue worker process the Conductor
// spawns inside a K8s pod (SPEC §11.1). On startup it reads the
// assignment from environment variables, opens an AgentRunner session
// against the configured LLM provider, registers the SPEC §11.3 tool
// set, and drives the synthesis pipeline to either a PRPlan or a
// terminal worker_session state.
//
// In this slice (orion-e44) the worker is wired against the agent
// package's contracts; the live K8s pod spec, repo-cache mount, and
// PR-opening side effects ship in orion-e45 / e46 / e49.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/agent"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/version"
)

const exitConfig = 64

func main() {
	model := flag.String("model", envOr("ORION_WORKER_MODEL", "gemini-1.5-pro"), "LLM model name")
	maxTurns := flag.Int("max-turns", 8, "maximum continuation turns per session (SPEC §11.4)")
	tokenBudget := flag.Int("token-budget", 200000, "per-session token budget (SPEC §11.4 #3)")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.String())
		return
	}

	cfg, err := assignmentFromEnv()
	if err != nil {
		log.Printf("orion-worker: assignment error: %v", err)
		os.Exit(exitConfig)
	}
	log.Printf("orion-worker: starting run=%s claim=%s workspace_key=%s",
		cfg.RunID, cfg.ClaimID, cfg.WorkspaceKey)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	registry := agent.NewRegistry()
	if err := registerStandardTools(registry, cfg); err != nil {
		log.Printf("orion-worker: tool registration failed: %v", err)
		os.Exit(exitConfig)
	}

	mode := harness.MaterializerLocal
	if envOr("K8S_HARNESS_ENABLED", "") == "true" {
		mode = harness.MaterializerK8s
	}
	log.Printf("orion-worker: harness materializer mode=%s", mode)
	log.Printf("orion-worker: tools registered: %d", len(registry.Definitions()))
	log.Printf("orion-worker: model=%s max_turns=%d token_budget=%d", *model, *maxTurns, *tokenBudget)
	log.Printf("orion-worker: full agent + harness wiring deferred to orion-e48 (Conductor scheduler)")

	// Demonstrate the runner wiring contract by constructing an
	// LLMRunner with a fake generator; the agent surface is
	// exercised by the package-level tests. The worker exits 0 so
	// the K8s pod terminates cleanly.
	if err := runWiringSelfTest(ctx, registry, cfg); err != nil {
		log.Printf("orion-worker: self-test failed: %v", err)
		os.Exit(1)
	}
	log.Printf("orion-worker: self-test passed; exiting 0")
}

// assignment describes the worker's startup environment, populated
// from env vars by the Conductor's pod spec (orion-e48 wires those).
type assignment struct {
	RunID           uuid.UUID
	ClaimID         uuid.UUID
	WorkerSessionID uuid.UUID
	WorkspaceKey    string
	IssueExternalID string
	WorkspaceRoot   string
	ADRRoot         string
	WriteableLabels []string
	IneligiblePaths []string
	CommandAllow    []string
	FencingToken    int64
}

func assignmentFromEnv() (assignment, error) {
	var a assignment
	var err error
	a.RunID, err = uuid.Parse(os.Getenv("ORION_RUN_ID"))
	if err != nil {
		return a, fmt.Errorf("ORION_RUN_ID: %w", err)
	}
	a.ClaimID, err = uuid.Parse(os.Getenv("ORION_CLAIM_ID"))
	if err != nil {
		return a, fmt.Errorf("ORION_CLAIM_ID: %w", err)
	}
	a.WorkerSessionID, err = uuid.Parse(os.Getenv("ORION_WORKER_SESSION_ID"))
	if err != nil {
		return a, fmt.Errorf("ORION_WORKER_SESSION_ID: %w", err)
	}
	a.WorkspaceKey = os.Getenv("ORION_WORKSPACE_KEY")
	if a.WorkspaceKey == "" {
		return a, errors.New("ORION_WORKSPACE_KEY is required")
	}
	a.IssueExternalID = os.Getenv("ORION_ISSUE_EXTERNAL_ID")
	a.WorkspaceRoot = envOr("ORION_WORKSPACE_ROOT", "/sandbox-root/repo")
	a.ADRRoot = envOr("ORION_ADR_ROOT", "docs/adr")
	return a, nil
}

// registerStandardTools registers every SPEC §11.3 tool. Each tool
// receives the worker's WorkspaceConfig so structural enforcement is
// applied per-pod (no shared mutable state).
func registerStandardTools(r *agent.Registry, cfg assignment) error {
	ws := agent.WorkspaceConfig{
		WorkspaceRoot:    cfg.WorkspaceRoot,
		IneligiblePaths:  cfg.IneligiblePaths,
		WriteableLabels:  cfg.WriteableLabels,
		CommandWhitelist: cfg.CommandAllow,
		ADRRoot:          cfg.ADRRoot,
		IssueExternalID:  cfg.IssueExternalID,
	}
	for _, t := range []agent.Tool{
		agent.ApplyPatchTool{Cfg: ws},
		agent.RunCommandTool{Cfg: ws},
		agent.ReadFileTool{Cfg: ws},
		agent.SubmitPatchForVerificationTool{Cfg: ws},
		agent.TrackerCommentTool{Cfg: ws},
		agent.TrackerLabelTool{Cfg: ws},
		agent.CreateADRTool{Cfg: ws},
		// query_run_snapshot needs a SnapshotReader; deferred to
		// orion-e48 when the run snapshot is loaded at start.
	} {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}

func runWiringSelfTest(ctx context.Context, registry *agent.Registry, cfg assignment) error {
	gen := &echoGenerator{}
	wsID := cfg.WorkerSessionID
	claimID := cfg.ClaimID
	runner, err := agent.NewLLMRunner(agent.LLMRunnerConfig{
		Generator:       gen,
		Registry:        registry,
		Sink:            agent.NewNoopEventSink(),
		Recorder:        agent.NewInMemoryScopeRecorder(),
		RunID:           cfg.RunID,
		ClaimID:         &claimID,
		WorkerSessionID: &wsID,
	})
	if err != nil {
		return err
	}
	sid, err := runner.StartSession(ctx, agent.Prompt{
		System:      "self-test",
		Model:       "self-test",
		TokenBudget: 1024,
	})
	if err != nil {
		return err
	}
	turnCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = runner.Turn(turnCtx, sid, "ping", registry.Definitions())
	if err != nil {
		return err
	}
	return runner.Cancel(ctx, sid)
}

// echoGenerator is a minimal LLMGenerator used only by the self-test.
// Production wiring binds internal/llm.Generator (deferred to e46).
type echoGenerator struct{}

func (echoGenerator) Generate(_ context.Context, req agent.LLMRequest) (agent.LLMResponse, error) {
	last := ""
	if n := len(req.History); n > 0 {
		last = req.History[n-1].Content
	}
	return agent.LLMResponse{
		Content:      "self-test echo: " + last,
		TokensIn:     len(last),
		TokensOut:    32,
		FinishReason: agent.FinishStop,
	}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

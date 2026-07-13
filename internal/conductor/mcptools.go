package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/promptguard"
	"github.com/revelara-ai/orion/internal/tools"
)

// registerMCPTools connects to the AUTHENTICATED revelara.ai MCP service and exposes each of its
// tools (search_controls, search_incidents, explore_graph, get_control, retrieve_memories, …) as a
// first-class Conductor tool — so the agent can research reliability context directly, not just
// consume it in the background. This is what lets the Conductor act as an agent in its own right
// (or-xe7.10).
//
// Registration is guarded: with no cached credential, an unreachable service, or a failed handshake,
// NO tools are registered and the agent simply runs without the revelara.ai surface (never a hard
// dependency). The handshake + listing are time-bounded so a slow service can't hang agent startup.
func registerMCPTools(r *tools.Registry, store *contextstore.Store) {
	mcp := mcpClientFromCredentials(store)
	if mcp == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := mcp.Initialize(ctx); err != nil {
		return
	}
	remote, err := mcp.ListTools(ctx)
	if err != nil {
		return
	}
	for _, mt := range remote {
		name := mt.Name // the REMOTE tool name to call (the exposed name is prefixed for provenance)
		schema := mt.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		r.Register(tools.Tool{
			Name:        "revelara_" + name,
			Description: "[revelara.ai] " + mt.Description,
			InputSchema: schema,
			// The revelara.ai research tools are reads (search/get/explore/analyze/retrieve); tenant
			// isolation is enforced server-side by the token's org context.
			Safety: tools.Safety{ReadOnly: true, ParallelSafe: true},
			Run: func(ctx context.Context, input json.RawMessage) (string, error) {
				var args any
				if len(input) > 0 {
					_ = json.Unmarshal(input, &args)
				}
				cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				res, err := mcp.CallTool(cctx, name, args)
				if err != nil {
					return "", err
				}
				if res.IsError {
					return "revelara.ai tool error: " + guardToolText(mcpToolText(res)), nil
				}
				return guardToolText(mcpToolText(res)), nil
			},
		})
	}
}

// guardToolText neutralizes known injection patterns in an EXTERNAL tool
// result before the model reads it (or-ykz.17): MCP results are remote data —
// the classic injection-via-tool-output vector. ScopeContext keeps the guard
// conservative (instruction-injection shapes only) so legitimate incident
// text survives intact.
func guardToolText(s string) string {
	safe, _ := promptguard.Neutralize(s, promptguard.ScopeContext)
	return safe
}

// mcpToolText flattens an MCP tool result's content blocks into the text the model reads back.
func mcpToolText(res polaris.ToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

package polaris

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// callTimeout bounds each MCP tool call so an unreachable service never blocks the loop for long
// (Harness Reliability: revelara.ai reliability context is optional).
const callTimeout = 3 * time.Second

// Source records where a kind's data came from.
type Source string

// Where a consumed kind's data came from.
const (
	SourceLive  Source = "live"
	SourceCache Source = "cache"
	SourceEmpty Source = "empty"
)

// ReliabilityContext is the consumed revelara.ai context for a task. Reduced is true when any kind
// fell back to cache/empty (the MCP service was unreachable) — the loop proceeds, but the delivery
// envelope flags reduced reliability context.
type ReliabilityContext struct {
	Controls  json.RawMessage   `json:"controls"`
	Knowledge json.RawMessage   `json:"knowledge"`
	Risks     json.RawMessage   `json:"risks"`
	Reduced   bool              `json:"reduced"`
	Sources   map[string]Source `json:"sources"`
}

// Consumer pulls reliability context from the revelara.ai MCP service with a local cache fallback.
// A nil client means "no live MCP" → it serves cache/empty and flags reduced context.
type Consumer struct {
	mcp        *MCPClient
	store      *contextstore.Store
	ttlSeconds int
	initOnce   sync.Once
	initErr    error
}

// NewConsumer builds a consumer over an MCP client (nil = no live MCP → cache/empty).
func NewConsumer(mcp *MCPClient, store *contextstore.Store) *Consumer {
	return &Consumer{mcp: mcp, store: store, ttlSeconds: 3600}
}

// ensureInit performs the MCP handshake once, lazily, before the first tool call.
func (c *Consumer) ensureInit(ctx context.Context) error {
	c.initOnce.Do(func() {
		if c.mcp == nil {
			return
		}
		ictx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		_, c.initErr = c.mcp.Initialize(ictx)
	})
	return c.initErr
}

// Load pulls controls, knowledge, and risks for a project CONCURRENTLY via MCP tool calls, caching
// live results and falling back to cache (then empty) when the MCP service is unreachable. Each call
// is time-bounded, so an unreachable service bounds the whole call. It NEVER errors on
// unreachability — the loop proceeds on reduced context.
func (c *Consumer) Load(ctx context.Context, projectID, query string) (ReliabilityContext, error) {
	rc := ReliabilityContext{Sources: map[string]Source{}}
	kinds := []struct {
		name, tool string
		set        func(json.RawMessage)
	}{
		{"controls", "search_controls", func(m json.RawMessage) { rc.Controls = m }},
		{"knowledge", "search_knowledge", func(m json.RawMessage) { rc.Knowledge = m }},
		{"risks", "search_risks", func(m json.RawMessage) { rc.Risks = m }},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, k := range kinds {
		wg.Add(1)
		go func(name, tool string, set func(json.RawMessage)) {
			defer wg.Done()
			payload, src := c.callOrCache(ctx, projectID, name, tool, query)
			mu.Lock()
			set(payload)
			rc.Sources[name] = src
			if src != SourceLive {
				rc.Reduced = true
			}
			mu.Unlock()
		}(k.name, k.tool, k.set)
	}
	wg.Wait()
	return rc, nil
}

// callOrCache calls the MCP tool for a kind, caching a live result and falling back to cache then
// empty when the MCP service is unreachable.
func (c *Consumer) callOrCache(ctx context.Context, projectID, kind, tool, query string) (json.RawMessage, Source) {
	if body, ok := c.callTool(ctx, tool, query); ok {
		_ = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			return tx.PolarisContext().Upsert(ctx, projectID, kind, string(body), c.ttlSeconds)
		})
		return body, SourceLive
	}
	// Fall back to cache, then empty.
	var cached string
	var found bool
	_ = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, projectID, kind)
		if err != nil {
			return err
		}
		cached, found = e.Payload, ok
		return nil
	})
	if found && json.Valid([]byte(cached)) {
		return json.RawMessage(cached), SourceCache
	}
	return json.RawMessage("[]"), SourceEmpty
}

// callTool runs an MCP tool and returns its JSON payload (the concatenated text content), or
// ok=false on any error / non-JSON / tool-error result.
func (c *Consumer) callTool(ctx context.Context, tool, query string) (json.RawMessage, bool) {
	if c.mcp == nil || c.ensureInit(ctx) != nil {
		return nil, false
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	res, err := c.mcp.CallTool(cctx, tool, map[string]any{"query": query})
	if err != nil || res.IsError {
		return nil, false
	}
	var b strings.Builder
	for _, ct := range res.Content {
		b.WriteString(ct.Text)
	}
	body := json.RawMessage(strings.TrimSpace(b.String()))
	if !json.Valid(body) {
		return nil, false
	}
	return body, true
}

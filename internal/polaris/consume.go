package polaris

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// fetchTimeout bounds each Polaris consume call so an unreachable server never
// blocks the loop for long (Harness Reliability: Polaris is optional).
const fetchTimeout = 3 * time.Second

// fetch issues an authenticated GET and returns the body on 200.
func (c *Client) fetch(ctx context.Context, token, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// Source records where a kind's data came from.
type Source string

const (
	SourceLive  Source = "live"
	SourceCache Source = "cache"
	SourceEmpty Source = "empty"
)

// ReliabilityContext is the consumed Polaris context for a task. Reduced is true
// when any kind fell back to cache/empty (Polaris was unreachable) — the loop
// proceeds, but the delivery envelope flags reduced reliability context.
type ReliabilityContext struct {
	Controls  json.RawMessage   `json:"controls"`
	Knowledge json.RawMessage   `json:"knowledge"`
	Risks     json.RawMessage   `json:"risks"`
	Reduced   bool              `json:"reduced"`
	Sources   map[string]Source `json:"sources"`
}

// Consumer pulls reliability context from Polaris with a local cache fallback.
type Consumer struct {
	client     *Client
	store      *contextstore.Store
	token      string
	ttlSeconds int
}

// NewConsumer builds a consumer. A nil client (or empty token) means "no live
// Polaris" → the consumer serves cache/empty and flags reduced context.
func NewConsumer(client *Client, store *contextstore.Store, token string) *Consumer {
	return &Consumer{client: client, store: store, token: token, ttlSeconds: 3600}
}

// Load pulls controls, knowledge, and risks for a project CONCURRENTLY, caching
// live results and falling back to cache (then empty) when Polaris is
// unreachable. Each fetch is time-bounded, so an unreachable Polaris bounds the
// whole call at ~fetchTimeout. It NEVER errors on unreachability — the loop
// proceeds on reduced context.
func (c *Consumer) Load(ctx context.Context, projectID, query string) (ReliabilityContext, error) {
	rc := ReliabilityContext{Sources: map[string]Source{}}
	kinds := []struct {
		name, path string
		set        func(json.RawMessage)
	}{
		{"controls", "/api/v1/controls", func(m json.RawMessage) { rc.Controls = m }},
		{"knowledge", "/api/knowledge/search?q=" + url.QueryEscape(query), func(m json.RawMessage) { rc.Knowledge = m }},
		{"risks", "/api/v1/risks", func(m json.RawMessage) { rc.Risks = m }},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, k := range kinds {
		wg.Add(1)
		go func(name, path string, set func(json.RawMessage)) {
			defer wg.Done()
			payload, src := c.fetchOrCache(ctx, projectID, name, path)
			mu.Lock()
			set(payload)
			rc.Sources[name] = src
			if src != SourceLive {
				rc.Reduced = true
			}
			mu.Unlock()
		}(k.name, k.path, k.set)
	}
	wg.Wait()
	return rc, nil
}

func (c *Consumer) fetchOrCache(ctx context.Context, projectID, kind, path string) (json.RawMessage, Source) {
	if c.client != nil && c.token != "" {
		fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		body, err := c.client.fetch(fctx, c.token, path)
		cancel()
		if err == nil && json.Valid(body) {
			_ = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
				return tx.PolarisContext().Upsert(ctx, projectID, kind, string(body), c.ttlSeconds)
			})
			return body, SourceLive
		}
	}
	// Fall back to cache, then empty.
	var cached string
	var ok bool
	_ = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, found, err := tx.PolarisContext().Get(ctx, projectID, kind)
		if err != nil {
			return err
		}
		cached, ok = e.Payload, found
		return nil
	})
	if ok && json.Valid([]byte(cached)) {
		return json.RawMessage(cached), SourceCache
	}
	return json.RawMessage("[]"), SourceEmpty
}

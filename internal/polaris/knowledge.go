package polaris

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// KnowledgeInsight is the orion-side projection of one
// /api/v1/knowledge/insights item from Polaris. Field set is the
// subset orion's enrichment + patch synthesis pipeline consumes.
type KnowledgeInsight struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags,omitempty"`
	ControlCode string   `json:"control_code,omitempty"`
}

// listKnowledgeInsightsResponse mirrors the wire format.
type listKnowledgeInsightsResponse struct {
	Insights []KnowledgeInsight `json:"insights"`
	Total    int                `json:"total"`
}

// KnowledgeInsightsOptions filters the insights query.
type KnowledgeInsightsOptions struct {
	// Tags filters insights to those carrying any of these tags.
	Tags []string

	// ControlCode filters insights to those associated with one
	// specific Polaris control_code.
	ControlCode string

	// Limit caps result size. 0 = use Polaris's default.
	Limit int
}

// ListKnowledgeInsights fetches insights from Polaris with retry.
// Returns a snapshot whose contents do not change after this call
// returns; callers hold the slice directly.
func (c *Client) ListKnowledgeInsights(ctx context.Context, opts KnowledgeInsightsOptions) ([]KnowledgeInsight, error) {
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/v1/knowledge/insights"
	q := []string{}
	if len(opts.Tags) > 0 {
		q = append(q, "tags="+strings.Join(opts.Tags, ","))
	}
	if opts.ControlCode != "" {
		q = append(q, "control_code="+opts.ControlCode)
	}
	if opts.Limit > 0 {
		q = append(q, fmt.Sprintf("limit=%d", opts.Limit))
	}
	if len(q) > 0 {
		endpoint += "?" + strings.Join(q, "&")
	}
	body, err := c.doWithRetry(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var resp listKnowledgeInsightsResponse
	if jerr := json.Unmarshal(body, &resp); jerr != nil {
		return nil, fmt.Errorf("polaris: parse insights: %w", jerr)
	}
	return resp.Insights, nil
}

// SearchHit is the orion-side projection of one /api/search result.
type SearchHit struct {
	ID      string  `json:"id"`
	Source  string  `json:"source"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

// SearchOptions controls a Polaris search call. Marshaled directly to
// the /api/search request body.
type SearchOptions struct {
	// Query is the natural-language search string. Required.
	Query string `json:"query"`

	// Source restricts hits to a single source ("incidents",
	// "knowledge", etc.). Empty means all.
	Source string `json:"source,omitempty"`

	// Limit caps result size. 0 = use Polaris's default.
	Limit int `json:"limit,omitempty"`
}

type searchResponse struct {
	Hits  []SearchHit `json:"hits"`
	Total int         `json:"total"`
}

// Search calls /api/search with the given query and returns hits with retry.
func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]SearchHit, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("%w: Search requires Query", ErrInvalidConfig)
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/search"
	payload, _ := json.Marshal(opts)
	body, err := c.doWithRetry(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	var resp searchResponse
	if jerr := json.Unmarshal(body, &resp); jerr != nil {
		return nil, fmt.Errorf("polaris: parse search: %w", jerr)
	}
	return resp.Hits, nil
}

// ForesightChain is one chain returned by /api/knowledge/foresight.
type ForesightChain struct {
	ID    string   `json:"id"`
	Steps []string `json:"steps"`
}

// ForesightOptions controls a Polaris foresight call. Marshaled
// directly to the /api/knowledge/foresight request body.
type ForesightOptions struct {
	// Anchor is the natural-language description of the change being
	// considered. Required.
	Anchor string `json:"anchor"`

	// MaxChains caps how many chains to return. 0 = use Polaris's default.
	MaxChains int `json:"max_chains,omitempty"`
}

type foresightResponse struct {
	Chains []ForesightChain `json:"chains"`
}

// Foresight calls /api/knowledge/foresight and returns chains.
// Polaris computes downstream-effect chains; orion uses them as
// priors when prompting the patch synthesizer.
func (c *Client) Foresight(ctx context.Context, opts ForesightOptions) ([]ForesightChain, error) {
	if strings.TrimSpace(opts.Anchor) == "" {
		return nil, fmt.Errorf("%w: Foresight requires Anchor", ErrInvalidConfig)
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/knowledge/foresight"
	payload, _ := json.Marshal(opts)
	body, err := c.doWithRetry(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	var resp foresightResponse
	if jerr := json.Unmarshal(body, &resp); jerr != nil {
		return nil, fmt.Errorf("polaris: parse foresight: %w", jerr)
	}
	return resp.Chains, nil
}

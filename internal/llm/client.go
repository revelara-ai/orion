package llm

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// Generator is the abstraction every consumer should depend on. Production
// code constructs a *Client via NewClient; tests inject a fake.
type Generator interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}

// Client is the production Generator backed by google.golang.org/genai
// against the Vertex AI backend.
type Client struct {
	cfg    Config
	client *genai.Client
}

// NewClient builds a Vertex-backed Generator. Caller MUST defer Close().
//
// The auth chain matches polaris's pattern: ADC via gcloud or workload
// identity. Errors during client creation surface ErrAuthMissing or the
// underlying SDK error wrapped in ErrGenerationFailed.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	gc, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  cfg.ProjectID,
		Location: cfg.Location,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGenerationFailed, err)
	}
	return &Client{cfg: cfg, client: gc}, nil
}

// Close releases the underlying gRPC connection. Safe to call once.
func (c *Client) Close() error {
	// genai.Client doesn't expose an explicit Close in current versions; if
	// future versions do, route here. For now, returning nil is correct
	// and matches the polaris pattern.
	return nil
}

// Generate runs one model call. Returns ErrGenerationFailed on transport
// errors and ErrEmptyResponse when the model returns no candidate text.
func (c *Client) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	if req.User == "" {
		return GenerateResponse{}, fmt.Errorf("%w: User text is empty", ErrInvalidConfig)
	}

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: req.User}}},
	}

	gcfg := &genai.GenerateContentConfig{}
	if req.System != "" {
		gcfg.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: req.System}},
		}
	}
	if req.MaxTokens > 0 {
		v := int32(req.MaxTokens) //#nosec G115 -- req.MaxTokens is bounded by caller
		gcfg.MaxOutputTokens = v
	}
	if req.Temperature >= 0 {
		t := req.Temperature
		gcfg.Temperature = &t
	}

	resp, err := c.client.Models.GenerateContent(ctx, c.cfg.Model, contents, gcfg)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("%w: %v", ErrGenerationFailed, err)
	}
	if resp == nil || len(resp.Candidates) == 0 {
		return GenerateResponse{}, ErrEmptyResponse
	}

	cand := resp.Candidates[0]
	if cand.Content == nil || len(cand.Content.Parts) == 0 {
		return GenerateResponse{}, ErrEmptyResponse
	}

	var text string
	for _, p := range cand.Content.Parts {
		text += p.Text
	}
	if text == "" {
		return GenerateResponse{}, ErrEmptyResponse
	}

	out := GenerateResponse{
		Text:         text,
		Model:        c.cfg.Model,
		FinishReason: string(cand.FinishReason),
	}
	if resp.UsageMetadata != nil {
		out.TokensIn = int(resp.UsageMetadata.PromptTokenCount)
		out.TokensOut = int(resp.UsageMetadata.CandidatesTokenCount)
	}
	return out, nil
}

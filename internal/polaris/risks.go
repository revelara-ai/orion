package polaris

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ApplicableRisk is the orion-side projection of one
// /api/v1/risks?status=applicable item from Polaris. Used by the patch
// synthesizer to avoid duplicating remediation already in flight.
type ApplicableRisk struct {
	ID          string `json:"id"`
	ControlCode string `json:"control_code"`
	Service     string `json:"service"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
}

// listRisksResponse mirrors the wire format.
type listRisksResponse struct {
	Risks []ApplicableRisk `json:"risks"`
	Total int              `json:"total"`
}

// ListApplicableRisksOptions filters the risks query.
type ListApplicableRisksOptions struct {
	// Service restricts results to one service. Empty = all.
	Service string

	// Limit caps result size. 0 = use Polaris's default.
	Limit int
}

// ListApplicableRisks fetches risks with status=applicable from
// Polaris with retry. Returns a snapshot whose contents do not change
// after this call returns.
func (c *Client) ListApplicableRisks(ctx context.Context, opts ListApplicableRisksOptions) ([]ApplicableRisk, error) {
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/v1/risks"
	q := []string{"status=applicable"}
	if opts.Service != "" {
		q = append(q, "service="+opts.Service)
	}
	if opts.Limit > 0 {
		q = append(q, fmt.Sprintf("limit=%d", opts.Limit))
	}
	endpoint += "?" + strings.Join(q, "&")
	body, err := c.doWithRetry(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var resp listRisksResponse
	if jerr := json.Unmarshal(body, &resp); jerr != nil {
		return nil, fmt.Errorf("polaris: parse risks: %w", jerr)
	}
	return resp.Risks, nil
}

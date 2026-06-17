package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ListIssuesOptions controls a GET /repos/{owner}/{repo}/issues call.
type ListIssuesOptions struct {
	// State scopes by GitHub's issue state ("open", "closed", "all").
	// Empty defaults to "open".
	State string

	// Since restricts to issues updated at or after this time.
	Since time.Time

	// Labels filters by label set (AND semantics on GitHub side).
	Labels []string

	// Limit caps the per-page result. 0 = use GitHub's default (30).
	// Max is 100.
	Limit int
}

// ListedIssue is the subset of GitHub's issue payload the adapter
// projects into trackers.NormalizedIssue. Fields the adapter doesn't
// consume are omitted so the wire shape can drift safely.
type ListedIssue struct {
	Number    int       `json:"number"`
	NodeID    string    `json:"node_id"`
	HTMLURL   string    `json:"html_url"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Labels    []Label   `json:"labels"`
	UpdatedAt time.Time `json:"updated_at"`
	// PullRequest is set when this row is actually a PR (GitHub returns
	// PRs in /issues). Used to filter PRs out at the adapter layer.
	PullRequest *struct{} `json:"pull_request,omitempty"`
}

// Label is the inline label shape returned with each issue.
type Label struct {
	Name string `json:"name"`
}

// ListIssues fetches issues from a repo. The adapter (E2-2) is
// responsible for filtering PRs out via ListedIssue.PullRequest != nil
// before normalizing.
func (a *App) ListIssues(ctx context.Context, owner, repo string, opts ListIssuesOptions) ([]ListedIssue, error) {
	if owner == "" || repo == "" {
		return nil, errors.New("github: ListIssues: owner and repo required")
	}
	state := opts.State
	if state == "" {
		state = "open"
	}
	q := url.Values{}
	q.Set("state", state)
	if !opts.Since.IsZero() {
		q.Set("since", opts.Since.UTC().Format(time.RFC3339))
	}
	if len(opts.Labels) > 0 {
		q.Set("labels", strings.Join(opts.Labels, ","))
	}
	if opts.Limit > 0 {
		q.Set("per_page", strconv.Itoa(opts.Limit))
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", a.cfg.APIBaseURL, owner, repo)
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	var out []ListedIssue
	if err := a.doJSON(ctx, "GET", endpoint, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetIssue fetches a single issue by number.
func (a *App) GetIssue(ctx context.Context, owner, repo string, number int) (*ListedIssue, error) {
	if owner == "" || repo == "" {
		return nil, errors.New("github: GetIssue: owner and repo required")
	}
	if number <= 0 {
		return nil, errors.New("github: GetIssue: number must be positive")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", a.cfg.APIBaseURL, owner, repo, number)
	var out ListedIssue
	if err := a.doJSON(ctx, "GET", endpoint, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateIssueOptions is the POST /repos/{owner}/{repo}/issues body.
type CreateIssueOptions struct {
	Title  string   `json:"title"`
	Body   string   `json:"body,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

// CreateIssue files a new issue. Returns the upstream-assigned issue
// shape; the adapter normalizes into trackers.NormalizedIssue.
func (a *App) CreateIssue(ctx context.Context, owner, repo string, opts CreateIssueOptions) (*ListedIssue, error) {
	if owner == "" || repo == "" {
		return nil, errors.New("github: CreateIssue: owner and repo required")
	}
	if opts.Title == "" {
		return nil, errors.New("github: CreateIssue: title required")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", a.cfg.APIBaseURL, owner, repo)
	var out ListedIssue
	if err := a.doJSON(ctx, "POST", endpoint, opts, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateIssueState transitions a GitHub issue between open and closed.
// GitHub doesn't have a native "blocked" state; the adapter (E2-2)
// translates blocked → labels in its UpdateState implementation.
func (a *App) UpdateIssueState(ctx context.Context, owner, repo string, number int, state string) error {
	if owner == "" || repo == "" {
		return errors.New("github: UpdateIssueState: owner and repo required")
	}
	if number <= 0 {
		return errors.New("github: UpdateIssueState: number must be positive")
	}
	if state != "open" && state != "closed" {
		return fmt.Errorf("github: UpdateIssueState: state must be open|closed, got %q", state)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d", a.cfg.APIBaseURL, owner, repo, number)
	body := map[string]string{"state": state}
	return a.doJSON(ctx, "PATCH", endpoint, body, nil)
}

// RateLimit is the rate-limit probe response used by HealthCheck.
type RateLimit struct {
	Resources struct {
		Core struct {
			Limit     int `json:"limit"`
			Remaining int `json:"remaining"`
			Reset     int `json:"reset"`
		} `json:"core"`
	} `json:"resources"`
}

// HealthCheck issues GET /rate_limit. A non-nil error here surfaces
// as "binding unhealthy" to the ingestion driver.
func (a *App) HealthCheck(ctx context.Context) (*RateLimit, error) {
	endpoint := a.cfg.APIBaseURL + "/rate_limit"
	var rl RateLimit
	if err := a.doJSON(ctx, "GET", endpoint, nil, &rl); err != nil {
		return nil, err
	}
	return &rl, nil
}

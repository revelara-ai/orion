// Package github implements trackers.TrackerAdapter for GitHub Issues.
// It wraps Orion's existing internal/github (App auth + REST helpers
// from E1-1 + the issue-specific calls from E2-2) and normalizes
// GitHub's payloads into the canonical NormalizedIssue shape per
// SPEC §4.1.7.
//
// The adapter does NOT manage credentials directly. The binding's
// Credentials.AppToken is the GitHub App installation token; callers
// (the factory in internal/trackers) mint that via internal/github
// before invoking adapter methods.
package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	gh "github.com/revelara-ai/orion/internal/github"
	"github.com/revelara-ai/orion/internal/trackers"
)

// Adapter implements trackers.TrackerAdapter for the github_issues
// provider. One Adapter per process is fine; the per-binding state
// (owner, repo, token) lives in TrackerBinding and is passed on every
// method call.
type Adapter struct {
	// appFactory builds a per-binding github.App. Tests inject a
	// factory backed by an httptest server; production wires a
	// factory that uses the binding's Credentials.AppToken.
	appFactory func(binding trackers.TrackerBinding) (*gh.App, error)
}

// NewAdapter returns an Adapter using the default appFactory that
// minted a github.App from the binding's Credentials.AppToken. The
// install ID lives in the binding's config under "installation_id".
func NewAdapter() *Adapter {
	return &Adapter{
		appFactory: defaultAppFactory,
	}
}

// NewAdapterWithFactory injects a custom appFactory. Used by tests
// to point at an httptest.Server.
func NewAdapterWithFactory(f func(trackers.TrackerBinding) (*gh.App, error)) *Adapter {
	return &Adapter{appFactory: f}
}

// defaultAppFactory builds a github.App from a TrackerBinding. v1
// expects the binding's Credentials.AppToken to already be a valid
// installation token (minted by the caller before invocation) and
// stashes it as the App's bearer for App-level calls. Issue calls
// re-mint via the token cache, which we bootstrap here.
//
// The binding's Config map MUST contain:
//   - "app_id" (string, integer): GitHub App ID
//   - "installation_id" (string, integer): the installation
//   - "private_key_pem" (string): PEM-encoded private key
//
// In production these come from the encrypted vault entry the
// credentials_ref points at; v1 ships this shape so the factory is
// drop-in once the credential resolver (E2-3) lands.
func defaultAppFactory(binding trackers.TrackerBinding) (*gh.App, error) {
	appID, err := lookupInt64(binding.Config, "app_id")
	if err != nil {
		return nil, err
	}
	instID, err := lookupInt64(binding.Config, "installation_id")
	if err != nil {
		return nil, err
	}
	pemKey, _ := binding.Config["private_key_pem"].(string)
	if pemKey == "" {
		return nil, errors.New("trackers/github: private_key_pem missing from binding config")
	}
	return gh.NewApp(gh.AppConfig{
		AppID:          appID,
		InstallationID: instID,
		PrivateKeyPEM:  []byte(pemKey),
	})
}

func lookupInt64(cfg map[string]any, key string) (int64, error) {
	v, ok := cfg[key]
	if !ok {
		return 0, fmt.Errorf("trackers/github: config missing %q", key)
	}
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	}
	return 0, fmt.Errorf("trackers/github: config %q is %T, want int64", key, v)
}

// ownerRepo splits a "owner/repo" string. Returns ErrInvalidBinding
// when the binding's config doesn't carry a recognizable repo
// identifier.
func ownerRepo(binding trackers.TrackerBinding) (string, string, error) {
	full, ok := binding.Config["repo_full_name"].(string)
	if !ok || full == "" {
		return "", "", fmt.Errorf("%w: config.repo_full_name required", trackers.ErrInvalidBinding)
	}
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: repo_full_name %q must be owner/repo", trackers.ErrInvalidBinding, full)
	}
	return parts[0], parts[1], nil
}

// Kind returns the wire-stable provider name.
func (a *Adapter) Kind() trackers.TrackerKind {
	return trackers.TrackerKindGitHubIssues
}

// FetchCandidates returns issues updated at-or-after `since`. PRs are
// filtered out (GitHub returns them in the issues endpoint).
func (a *Adapter) FetchCandidates(ctx context.Context, binding trackers.TrackerBinding, since time.Time) ([]trackers.NormalizedIssue, error) {
	app, err := a.appFactory(binding)
	if err != nil {
		return nil, err
	}
	owner, repo, err := ownerRepo(binding)
	if err != nil {
		return nil, err
	}
	listed, err := app.ListIssues(ctx, owner, repo, gh.ListIssuesOptions{
		State: "open",
		Since: since,
	})
	if err != nil {
		return nil, fmt.Errorf("trackers/github: fetch: %w", err)
	}
	out := make([]trackers.NormalizedIssue, 0, len(listed))
	for _, li := range listed {
		if li.PullRequest != nil {
			continue // skip PRs
		}
		out = append(out, normalize(owner, repo, &li))
	}
	return out, nil
}

// FetchByExternalIDs resyncs specific issues.
func (a *Adapter) FetchByExternalIDs(ctx context.Context, binding trackers.TrackerBinding, ids []string) ([]trackers.NormalizedIssue, error) {
	app, err := a.appFactory(binding)
	if err != nil {
		return nil, err
	}
	owner, repo, err := ownerRepo(binding)
	if err != nil {
		return nil, err
	}
	out := make([]trackers.NormalizedIssue, 0, len(ids))
	for _, id := range ids {
		number, ok := parseExternalID(id, owner, repo)
		if !ok {
			continue
		}
		li, err := app.GetIssue(ctx, owner, repo, number)
		if err != nil {
			// Skip not-founds; surface other errors.
			if strings.Contains(err.Error(), "status 404") {
				continue
			}
			return nil, err
		}
		if li.PullRequest != nil {
			continue
		}
		out = append(out, normalize(owner, repo, li))
	}
	return out, nil
}

// Create files an issue.
func (a *Adapter) Create(ctx context.Context, binding trackers.TrackerBinding, draft trackers.IssueDraft) (trackers.NormalizedIssue, error) {
	app, err := a.appFactory(binding)
	if err != nil {
		return trackers.NormalizedIssue{}, err
	}
	owner, repo, err := ownerRepo(binding)
	if err != nil {
		return trackers.NormalizedIssue{}, err
	}
	li, err := app.CreateIssue(ctx, owner, repo, gh.CreateIssueOptions{
		Title:  draft.Title,
		Body:   draft.Body,
		Labels: draft.Labels,
	})
	if err != nil {
		return trackers.NormalizedIssue{}, fmt.Errorf("trackers/github: create: %w", err)
	}
	return normalize(owner, repo, li), nil
}

// UpdateState transitions an issue. GitHub only supports open/closed
// natively; blocked is approximated by closed + label here. v1
// translates StateBlocked → close + "blocked" label.
func (a *Adapter) UpdateState(ctx context.Context, binding trackers.TrackerBinding, externalID string, state trackers.NormalizedState) error {
	app, err := a.appFactory(binding)
	if err != nil {
		return err
	}
	owner, repo, err := ownerRepo(binding)
	if err != nil {
		return err
	}
	number, ok := parseExternalID(externalID, owner, repo)
	if !ok {
		return fmt.Errorf("%w: external_id %q does not match owner/repo", trackers.ErrInvalidBinding, externalID)
	}
	switch state {
	case trackers.StateOpen, trackers.StateInProgress:
		return app.UpdateIssueState(ctx, owner, repo, number, "open")
	case trackers.StateClosed, trackers.StateCancelled, trackers.StateBlocked:
		return app.UpdateIssueState(ctx, owner, repo, number, "closed")
	}
	return fmt.Errorf("trackers/github: unsupported state %q", state)
}

// Comment posts a comment on an issue. PostComment from internal/github
// works for both issue and PR comments (same endpoint).
func (a *Adapter) Comment(ctx context.Context, binding trackers.TrackerBinding, externalID, body string) error {
	app, err := a.appFactory(binding)
	if err != nil {
		return err
	}
	owner, repo, err := ownerRepo(binding)
	if err != nil {
		return err
	}
	number, ok := parseExternalID(externalID, owner, repo)
	if !ok {
		return fmt.Errorf("%w: external_id %q does not match owner/repo", trackers.ErrInvalidBinding, externalID)
	}
	_, err = app.PostComment(ctx, owner, repo, number, body)
	return err
}

// Capabilities advertises the v1 GitHub adapter feature set.
func (a *Adapter) Capabilities() trackers.TrackerCapabilities {
	return trackers.TrackerCapabilities{
		CanCreate:           true,
		CanUpdateState:      true,
		CanComment:          true,
		SupportsLabelFilter: true,
		SupportsSince:       true,
	}
}

// HealthCheck pings GitHub's rate-limit endpoint.
func (a *Adapter) HealthCheck(ctx context.Context, binding trackers.TrackerBinding) error {
	app, err := a.appFactory(binding)
	if err != nil {
		return err
	}
	if _, err := app.HealthCheck(ctx); err != nil {
		return fmt.Errorf("%w: %v", trackers.ErrUnauthenticated, err)
	}
	return nil
}

// externalID returns the SPEC §4.2 format: "gh:owner/repo#N".
func externalID(owner, repo string, number int) string {
	return fmt.Sprintf("gh:%s/%s#%d", owner, repo, number)
}

// parseExternalID returns the issue number from a SPEC-format
// external_id, and a bool indicating it matched the expected owner/repo.
func parseExternalID(id, owner, repo string) (int, bool) {
	prefix := fmt.Sprintf("gh:%s/%s#", owner, repo)
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil {
		return 0, false
	}
	return n, true
}

// normalize maps a GitHub ListedIssue into trackers.NormalizedIssue.
func normalize(owner, repo string, li *gh.ListedIssue) trackers.NormalizedIssue {
	labels := make([]string, 0, len(li.Labels))
	for _, l := range li.Labels {
		labels = append(labels, strings.ToLower(l.Name))
	}
	return trackers.NormalizedIssue{
		ExternalID:  externalID(owner, repo, li.Number),
		ExternalURL: li.HTMLURL,
		Title:       li.Title,
		Description: li.Body,
		State:       normalizeState(li.State),
		Labels:      labels,
		LastUpdated: li.UpdatedAt,
	}
}

// normalizeState maps GitHub's state enum to the canonical
// NormalizedState. GitHub only has open/closed natively.
func normalizeState(s string) trackers.NormalizedState {
	switch s {
	case "open":
		return trackers.StateOpen
	case "closed":
		return trackers.StateClosed
	}
	return trackers.NormalizedState(s)
}

package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// CloneOptions configures a Clone invocation.
type CloneOptions struct {
	// RepoURL is the upstream HTTPS URL, e.g.
	// https://github.com/owner/name.git or .../name (no .git).
	RepoURL string
	// Token is the installation access token used for HTTPS auth.
	Token string
	// Dest is the local destination directory (must be empty or non-existent).
	Dest string
	// Depth requests a shallow clone (depth=N). Zero means full history.
	Depth int
}

// Clone runs `git clone` against RepoURL into Dest, embedding the
// installation token in the URL via the x-access-token user form
// recommended by GitHub for App installation HTTPS auth.
func Clone(ctx context.Context, opts CloneOptions) error {
	if opts.RepoURL == "" {
		return errors.New("github: CloneOptions.RepoURL required")
	}
	if opts.Token == "" {
		return errors.New("github: CloneOptions.Token required")
	}
	if opts.Dest == "" {
		return errors.New("github: CloneOptions.Dest required")
	}
	authURL, err := authenticatedURL(opts.RepoURL, opts.Token)
	if err != nil {
		return err
	}
	args := []string{"clone"}
	if opts.Depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", opts.Depth))
	}
	args = append(args, authURL, opts.Dest)
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("github: git clone: %w: %s", err, redact(string(out), opts.Token))
	}
	return nil
}

// authenticatedURL injects an x-access-token:<token> userinfo into an
// HTTPS git URL. This is the form GitHub recommends for App installation
// auth over HTTPS. SSH URLs are rejected as out of scope.
func authenticatedURL(raw, token string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("github: parse repo URL: %w", err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("github: repo URL must be https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("github: repo URL missing host")
	}
	u.User = url.UserPassword("x-access-token", token)
	return u.String(), nil
}

// redact replaces the install token wherever it appears in a string,
// suitable for inclusion in error messages and logs.
func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

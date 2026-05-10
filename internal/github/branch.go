package github

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// BranchPrefix is the namespace Orion writes all branches under, so
// customer repos can policy-match orion/* without ambiguity (SPEC §4.2).
const BranchPrefix = "orion/"

// validBranchChar matches characters that survive ExternalIDSanitize
// unchanged. Everything else collapses to '-'.
var validBranchChar = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// SanitizeExternalID maps an external_id (a tracker identifier such as
// `gh-customer-svc-123` or `linear:ENG-42`) into a git-ref-safe string.
// Repeated runs of replacement chars are collapsed and leading/trailing
// dashes are trimmed.
func SanitizeExternalID(s string) string {
	s = validBranchChar.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}

// BranchName returns the canonical Orion branch name per SPEC §4.2:
// `orion/<run_id_short>-<external_id_sanitized>`. runIDShort SHOULD be 6
// chars; longer values are truncated.
func BranchName(runIDShort, externalID string) string {
	if len(runIDShort) > 6 {
		runIDShort = runIDShort[:6]
	}
	return BranchPrefix + runIDShort + "-" + SanitizeExternalID(externalID)
}

// CommitOptions describes one commit to land on a freshly-created branch.
type CommitOptions struct {
	// RepoDir is the absolute path to the cloned repo working tree.
	RepoDir string
	// BranchName is the orion/* branch to create off the current HEAD.
	BranchName string
	// AuthorName and AuthorEmail are stamped on the commit.
	AuthorName  string
	AuthorEmail string
	// Message is the commit body. The first line is the subject.
	Message string
	// Files is path -> file content. Paths are relative to RepoDir; any
	// path that escapes RepoDir after Clean is rejected.
	Files map[string]string
}

// Validate checks CommitOptions for missing or unsafe fields.
func (o CommitOptions) Validate() error {
	if o.RepoDir == "" {
		return errors.New("github: CommitOptions.RepoDir required")
	}
	if !filepath.IsAbs(o.RepoDir) {
		return fmt.Errorf("github: RepoDir must be absolute, got %q", o.RepoDir)
	}
	if o.BranchName == "" {
		return errors.New("github: CommitOptions.BranchName required")
	}
	if !strings.HasPrefix(o.BranchName, BranchPrefix) {
		return fmt.Errorf("github: branch name %q must start with %q", o.BranchName, BranchPrefix)
	}
	if o.AuthorName == "" || o.AuthorEmail == "" {
		return errors.New("github: AuthorName and AuthorEmail required")
	}
	if o.Message == "" {
		return errors.New("github: Message required")
	}
	if len(o.Files) == 0 {
		return errors.New("github: Files required (at least one)")
	}
	for path := range o.Files {
		clean := filepath.Clean(path)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("github: file path %q escapes RepoDir", path)
		}
	}
	return nil
}

// CommitAndPush checks out a new branch, writes the requested files,
// commits with the given author identity, and pushes the branch to
// origin authenticated by the install token in opts.Token.
func CommitAndPush(ctx context.Context, token string, opts CommitOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if token == "" {
		return errors.New("github: token required")
	}

	if _, err := runGit(ctx, opts.RepoDir, "checkout", "-b", opts.BranchName); err != nil {
		return err
	}
	for path, content := range opts.Files {
		full := filepath.Join(opts.RepoDir, filepath.Clean(path))
		if err := writeFileMkdir(full, []byte(content)); err != nil {
			return fmt.Errorf("github: write %s: %w", path, err)
		}
		if _, err := runGit(ctx, opts.RepoDir, "add", "--", filepath.Clean(path)); err != nil {
			return err
		}
	}
	commitArgs := []string{
		"-c", "user.name=" + opts.AuthorName,
		"-c", "user.email=" + opts.AuthorEmail,
		"commit", "-m", opts.Message,
	}
	if _, err := runGit(ctx, opts.RepoDir, commitArgs...); err != nil {
		return err
	}
	// Set origin to the authenticated form so the push works without
	// inheriting whatever URL the clone used.
	originOut, err := runGit(ctx, opts.RepoDir, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	authURL, err := authenticatedURL(strings.TrimSpace(originOut), token)
	if err != nil {
		return err
	}
	if _, err := runGit(ctx, opts.RepoDir, "remote", "set-url", "origin", authURL); err != nil {
		return err
	}
	if _, err := runGit(ctx, opts.RepoDir, "push", "-u", "origin", opts.BranchName); err != nil {
		return err
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //#nosec G204 -- args are typed/validated CommitOptions; binary is the system git
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("github: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

package github

import (
	"context"
	"errors"
	"fmt"
)

// PROptions configures PR creation.
type PROptions struct {
	// Owner and Repo identify the target repository.
	Owner string
	Repo  string
	// Head is the branch with new commits ("orion/...").
	Head string
	// Base is the target branch (typically "main").
	Base string
	// Title is the PR title.
	Title string
	// Body is the markdown PR description.
	Body string
	// Draft opens the PR in draft state.
	Draft bool
}

// PR is the subset of GitHub's PR response Orion needs downstream.
type PR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	NodeID  string `json:"node_id"`
}

// CreatePR opens a pull request via the REST API and returns the
// created PR's number and HTML URL.
func (a *App) CreatePR(ctx context.Context, opts PROptions) (*PR, error) {
	if opts.Owner == "" || opts.Repo == "" {
		return nil, errors.New("github: PROptions.Owner and Repo required")
	}
	if opts.Head == "" || opts.Base == "" {
		return nil, errors.New("github: PROptions.Head and Base required")
	}
	if opts.Title == "" {
		return nil, errors.New("github: PROptions.Title required")
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls", a.cfg.APIBaseURL, opts.Owner, opts.Repo)
	body := map[string]any{
		"title": opts.Title,
		"head":  opts.Head,
		"base":  opts.Base,
		"body":  opts.Body,
		"draft": opts.Draft,
	}
	var out PR
	if err := a.doJSON(ctx, "POST", url, body, &out); err != nil {
		return nil, err
	}
	if out.Number == 0 {
		return nil, errors.New("github: PR created but response missing number")
	}
	return &out, nil
}

// PostComment adds an issue-style comment to a PR (PRs are issues for
// the comment endpoint). Returns the created comment's HTML URL.
func (a *App) PostComment(ctx context.Context, owner, repo string, prNumber int, body string) (string, error) {
	if owner == "" || repo == "" {
		return "", errors.New("github: owner and repo required")
	}
	if prNumber <= 0 {
		return "", errors.New("github: prNumber must be positive")
	}
	if body == "" {
		return "", errors.New("github: comment body required")
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", a.cfg.APIBaseURL, owner, repo, prNumber)
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	if err := a.doJSON(ctx, "POST", url, map[string]any{"body": body}, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

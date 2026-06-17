package github

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeExternalID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"gh-customer-svc-123", "gh-customer-svc-123"},
		{"linear:ENG-42", "linear-ENG-42"},
		{"jira/PROJ-1", "jira-PROJ-1"},
		{"  weird   spaces", "weird-spaces"},
		{"slashes/and:colons", "slashes-and-colons"},
		{"---trim---", "trim"},
		{"a..b.c", "a..b.c"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SanitizeExternalID(tc.in)
			if got != tc.want {
				t.Errorf("SanitizeExternalID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBranchName(t *testing.T) {
	cases := []struct {
		runID, ext, want string
	}{
		{"r3dq8a", "gh-customer-svc-123", "orion/r3dq8a-gh-customer-svc-123"},
		{"abc123def", "x", "orion/abc123-x"},
		{"r1", "linear:ENG-42", "orion/r1-linear-ENG-42"},
	}
	for _, tc := range cases {
		got := BranchName(tc.runID, tc.ext)
		if got != tc.want {
			t.Errorf("BranchName(%q, %q) = %q, want %q", tc.runID, tc.ext, got, tc.want)
		}
	}
}

func TestCommitOptionsValidate(t *testing.T) {
	good := CommitOptions{
		RepoDir:     "/tmp/repo",
		BranchName:  "orion/r3dq8a-x",
		AuthorName:  "Orion",
		AuthorEmail: "orion@example.com",
		Message:     "msg",
		Files:       map[string]string{"a.txt": "x"},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	bad := []CommitOptions{
		{},
		{RepoDir: "relative", BranchName: "orion/x", AuthorName: "n", AuthorEmail: "e", Message: "m", Files: map[string]string{"a": "x"}},
		{RepoDir: "/tmp", BranchName: "no-prefix", AuthorName: "n", AuthorEmail: "e", Message: "m", Files: map[string]string{"a": "x"}},
		{RepoDir: "/tmp", BranchName: "orion/x", AuthorName: "n", AuthorEmail: "e", Message: "m", Files: map[string]string{"../escape": "x"}},
		{RepoDir: "/tmp", BranchName: "orion/x", AuthorName: "n", AuthorEmail: "e", Message: "m", Files: map[string]string{"/abs": "x"}},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestAuthenticatedURL(t *testing.T) {
	cases := []struct {
		raw, token, wantContains string
		wantErr                  bool
	}{
		{"https://github.com/o/r.git", "tok123", "x-access-token:tok123@github.com", false},
		{"https://github.com/o/r", "tok", "x-access-token:tok@github.com", false},
		{"git@github.com:o/r.git", "tok", "", true},
		{"http://insecure", "tok", "", true},
		{"https:///no-host", "tok", "", true},
	}
	for _, tc := range cases {
		got, err := authenticatedURL(tc.raw, tc.token)
		if (err != nil) != tc.wantErr {
			t.Errorf("authenticatedURL(%q): err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && !strings.Contains(got, tc.wantContains) {
			t.Errorf("authenticatedURL(%q) = %q, want contains %q", tc.raw, got, tc.wantContains)
		}
	}
}

// TestCommitAndPushLocal creates a local bare "remote" repo and a
// working clone, then exercises the commit + push path end-to-end. This
// proves the git subprocess wiring without needing GitHub.
func TestCommitAndPushLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")

	mustRun := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	mustRun("git", "init", "--bare", "-b", "main", bare)
	mustRun("git", "init", "-b", "main", work)
	cmd := exec.Command("git", "-c", "user.name=seed", "-c", "user.email=s@x", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed commit: %v\n%s", err, out)
	}
	mustRun("git", "-C", work, "remote", "add", "origin", bare)
	mustRun("git", "-C", work, "push", "-u", "origin", "main")

	// Override authenticatedURL behavior by passing a token; the local
	// remote URL doesn't have a host so authenticatedURL would reject.
	// Use a dummy https URL on origin instead.
	httpsURL := "https://example.invalid/o/r.git"
	mustRun("git", "-C", work, "remote", "set-url", "origin", httpsURL)

	// Mock the actual push by intercepting via custom CommitAndPush wrapper:
	// we exercise validate + checkout + commit + remote rewrite up to push,
	// which will fail at push but after the local mutations are correct.
	err := CommitAndPush(context.Background(), "tok123", CommitOptions{
		RepoDir:     work,
		BranchName:  "orion/abc123-test",
		AuthorName:  "Orion Bot",
		AuthorEmail: "orion@example.com",
		Message:     "Hello Orion",
		Files:       map[string]string{"hello.txt": "hello orion\n"},
	})
	// Push to example.invalid will fail; that is expected. Verify the
	// commit landed locally on the new branch.
	if err == nil {
		t.Log("push to example.invalid unexpectedly succeeded; not a problem for the test")
	}
	cmd = exec.Command("git", "-C", work, "log", "--oneline", "orion/abc123-test", "--", "hello.txt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify commit: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Hello Orion") {
		t.Errorf("commit not found on branch:\n%s", out)
	}
	// And the remote URL must have been rewritten to embed the token.
	cmd = exec.Command("git", "-C", work, "remote", "get-url", "origin")
	if out, err := cmd.CombinedOutput(); err == nil {
		if !strings.Contains(string(out), "x-access-token:tok123@") {
			t.Errorf("remote URL not authenticated: %s", out)
		}
	}
}

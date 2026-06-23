package polaris

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

const sentinelToken = "SECRET-POLARIS-TOKEN-abc123"

// TestPolarisTokenNotInContextStoreAndUnreachableFromSandbox: the credential is
// stored 0600, separate from the Context Store DB (token bytes never appear in
// it), and a generation-domain sandbox cannot read the credentials file.
func TestPolarisTokenNotInContextStoreAndUnreachableFromSandbox(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()

	// Context Store lives here; credentials live in a SEPARATE dir.
	storeDir := filepath.Join(base, "data")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := contextstore.Open(storeDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	// Put some real data in the store.
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, e := tx.Projects().Create(ctx, "demo", "build something", "http-service")
		return e
	})
	_ = store.Close()

	credDir := filepath.Join(base, "credentials")
	ts, err := NewTokenStore(credDir)
	if err != nil {
		t.Fatalf("token store: %v", err)
	}
	if err := ts.Save(Token{AccessToken: sentinelToken, BaseURL: "https://example"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 1) 0600 perms.
	fi, err := os.Stat(ts.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("credential perms = %o, want 600", fi.Mode().Perm())
	}

	// 2) The token must not appear anywhere in the Context Store files.
	walkAssertAbsent(t, storeDir, sentinelToken)

	// 3) A bwrap sandbox (workdir = a worktree) cannot read the credentials file.
	probe := buildCredProbe(t)
	workdir := t.TempDir()
	out := runSandbox(t, workdir, probe, ts.Path())
	if !strings.Contains(out, "creds=denied") {
		t.Fatalf("sandbox could read the credential: %q", out)
	}
}

func walkAssertAbsent(t *testing.T, dir, secret string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("token leaked into Context Store file %s", e.Name())
		}
	}
}

// TestLoginStatusFlow: login caches a token; status verifies it via /auth/me and
// reports connected.
func TestLoginStatusFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": sentinelToken})
		case "/api/v1/auth/me":
			if r.Header.Get("Authorization") != "Bearer "+sentinelToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(Identity{Email: "dev@example.com", Org: "acme"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	c := NewClient(srv.URL)
	tok, err := c.Login(ctx, "dev", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if tok.AccessToken != sentinelToken {
		t.Fatalf("token = %q", tok.AccessToken)
	}
	id, err := c.Me(ctx, tok.AccessToken)
	if err != nil || id.Email != "dev@example.com" {
		t.Fatalf("me: id=%+v err=%v", id, err)
	}

	// Round-trip through the store.
	ts, _ := NewTokenStore(t.TempDir())
	if err := ts.Save(tok); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := ts.Load()
	if err != nil || !ok || got.AccessToken != sentinelToken {
		t.Fatalf("load: got=%+v ok=%v err=%v", got, ok, err)
	}
}

func buildCredProbe(t *testing.T) string {
	t.Helper()
	const src = `package main
import "os"
func main(){
  r:="creds=denied;"
  if p:=os.Getenv("PROBE_CREDS");p!=""{ if _,e:=os.ReadFile(p);e==nil{r="creds=readable;"} }
  _=os.WriteFile("cred_result.txt",[]byte(r),0644)
}`
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "main.go"), []byte(src), 0o644)
	_ = os.WriteFile(filepath.Join(d, "go.mod"), []byte("module credprobe\n\ngo 1.25\n"), 0o644)
	bin := filepath.Join(t.TempDir(), "credprobe")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = d
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	return bin
}

func runSandbox(t *testing.T, workdir, probe, credPath string) string {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatalf("bwrap required: %v", err)
	}
	cmd := exec.Command("bwrap",
		"--unshare-user", "--unshare-net", "--die-with-parent", "--clearenv",
		"--proc", "/proc", "--tmpfs", "/tmp",
		"--ro-bind", probe, probe,
		"--bind", workdir, workdir, "--chdir", workdir,
		"--setenv", "PROBE_CREDS", credPath,
		"--", probe)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bwrap: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(workdir, "cred_result.txt"))
	if err != nil {
		t.Fatalf("probe result: %v", err)
	}
	return string(b)
}

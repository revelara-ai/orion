package osvaudit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modRoot(t *testing.T, gomod string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const twoDeps = `module fixture

go 1.24

require (
	example.com/vulnerable v1.2.3
	example.com/clean v0.9.0 // indirect
)
`

// The or-ykz.16 done-when core: a dependency with a known CVE is FOUND (and
// only that one) — versions reach OSV v-stripped, indirects are screened too.
func TestAuditFindsKnownVulnerableDep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Queries []struct {
				Package struct {
					Name      string `json:"name"`
					Ecosystem string `json:"ecosystem"`
				} `json:"package"`
				Version string `json:"version"`
			} `json:"queries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		results := make([]map[string]any, len(req.Queries))
		for i, q := range req.Queries {
			if q.Version == "" || strings.HasPrefix(q.Version, "v") || q.Package.Ecosystem != "Go" {
				http.Error(w, "malformed query: version must be v-stripped, ecosystem Go", 400)
				return
			}
			if q.Package.Name == "example.com/vulnerable" {
				results[i] = map[string]any{"vulns": []map[string]string{{"id": "GO-2026-9999"}, {"id": "CVE-2026-0001"}}}
			} else {
				results[i] = map[string]any{}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer srv.Close()
	t.Setenv("ORION_OSV_URL", srv.URL)

	res := Audit(context.Background(), modRoot(t, twoDeps))
	if res.Skipped != "" {
		t.Fatalf("audit skipped unexpectedly: %s", res.Skipped)
	}
	if res.Checked != 2 {
		t.Fatalf("both deps (incl. indirect) must be screened, checked=%d", res.Checked)
	}
	if len(res.Findings) != 1 || res.Findings[0].Module != "example.com/vulnerable" {
		t.Fatalf("exactly the vulnerable dep must be flagged: %+v", res.Findings)
	}
	if !strings.Contains(res.Summary(), "GO-2026-9999") || !strings.Contains(res.Summary(), "CVE-2026-0001") {
		t.Fatalf("summary must carry the vulnerability ids: %s", res.Summary())
	}
}

// Offline OSV yields a visible SKIP — never findings, never a hard error.
func TestAuditOfflineSkipsVisibly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // now unreachable
	t.Setenv("ORION_OSV_URL", srv.URL)
	res := Audit(context.Background(), modRoot(t, twoDeps))
	if res.Skipped == "" || len(res.Findings) != 0 {
		t.Fatalf("offline must be a visible skip: %+v", res)
	}
}

func TestAuditKillSwitchAndStdlibOnly(t *testing.T) {
	t.Setenv("ORION_OSV", "off")
	if res := Audit(context.Background(), modRoot(t, twoDeps)); !strings.Contains(res.Skipped, "disabled") {
		t.Fatalf("kill switch must skip: %+v", res)
	}
	t.Setenv("ORION_OSV", "")
	res := Audit(context.Background(), modRoot(t, "module fixture\n\ngo 1.24\n"))
	if res.Skipped != "" || res.Checked != 0 || len(res.Findings) != 0 {
		t.Fatalf("stdlib-only module is clean by construction with no network: %+v", res)
	}
}

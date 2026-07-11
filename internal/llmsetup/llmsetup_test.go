package llmsetup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// setHome points HOME at a temp dir so ~/.orion/config.yaml is test-controlled.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".orion")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelectZeroConfigWithKey(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("want native brain, got offline: %s", b.Reason)
	}
	if b.ProviderName != "anthropic" || !strings.HasPrefix(b.Ref, "anthropic/") {
		t.Errorf("zero-config must select anthropic: %+v", b)
	}
}

func TestSelectZeroConfigNoKeyIsOffline(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider != nil {
		t.Fatal("no key + no config must be offline")
	}
	if !strings.Contains(b.Reason, "ANTHROPIC_API_KEY") {
		t.Errorf("reason must name the env var: %q", b.Reason)
	}
}

func TestSelectOrionModelEnvOverridesConfig(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: lmstudio/qwen3-32b\n")
	t.Setenv("ORION_MODEL", "ollama/llama3.3")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("offline: %s", b.Reason)
	}
	if b.ProviderName != "ollama" || b.Model != "llama3.3" {
		t.Errorf("ORION_MODEL must win over config model: %+v", b)
	}
}

func TestSelectConfigModel(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: lmstudio/qwen3-32b\n")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("offline: %s", b.Reason)
	}
	if b.Ref != "lmstudio/qwen3-32b" || b.Provider.Name() != "lmstudio" {
		t.Errorf("config model not honored: %+v", b)
	}
}

func TestSelectMalformedConfigIsOfflineWithReason(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: [broken")
	b := Select()
	if b.Provider != nil {
		t.Fatal("malformed config must not silently fall back to defaults")
	}
	if !strings.Contains(b.Reason, "config") {
		t.Errorf("reason must mention the config problem: %q", b.Reason)
	}
}

// TestListModelsParallelSortedByProvider: per-provider Models() calls run
// CONCURRENTLY (one slow endpoint must not serialize the whole listing behind
// its 3s timeout) while the output stays sorted by provider name with each
// provider's own model order preserved. The handlers track how many requests
// are in flight at once: a sequential implementation never overlaps
// (maxInflight == 1), the parallel one does. [or-1aw3 minor]
func TestListModelsParallelSortedByProvider(t *testing.T) {
	home := setHome(t)

	var mu sync.Mutex
	inflight, maxInflight := 0, 0
	srvFor := func(models ...string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			inflight++
			if inflight > maxInflight {
				maxInflight = inflight
			}
			mu.Unlock()
			time.Sleep(200 * time.Millisecond) // generous overlap window
			mu.Lock()
			inflight--
			mu.Unlock()
			parts := make([]string, len(models))
			for i, m := range models {
				parts[i] = fmt.Sprintf(`{"id":%q}`, m)
			}
			fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(parts, ","))
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	// Override ALL built-in providers so every configured provider points at a
	// test-controlled server (no key needed, no real localhost dialing).
	writeConfig(t, home, fmt.Sprintf(`
providers:
  anthropic: {type: openai, base_url: %s}
  gemini:    {type: openai, base_url: %s}
  lmstudio:  {type: openai, base_url: %s}
  ollama:    {type: openai, base_url: %s}
`, srvFor("a1", "a2").URL, srvFor("g1").URL, srvFor("l1").URL, srvFor("o1").URL))

	got := ListModels(context.Background())
	want := []string{"anthropic/a1", "anthropic/a2", "gemini/g1", "lmstudio/l1", "ollama/o1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListModels = %v, want %v (sorted by provider, server model order kept)", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxInflight < 2 {
		t.Errorf("maxInflight = %d, want >= 2 (per-provider Models() calls must overlap)", maxInflight)
	}
}

func TestRebuildBareIDStaysOnProvider(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	cur := Brain{ProviderName: "anthropic", Model: "claude-opus-4-8"}
	prov, ref, err := Rebuild(cur, "claude-sonnet-5")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "anthropic/claude-sonnet-5" || prov.Name() != "anthropic" {
		t.Errorf("bare id must stay on current provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildRefSwitchesProvider(t *testing.T) {
	setHome(t)
	cur := Brain{ProviderName: "anthropic", Model: "claude-opus-4-8"}
	prov, ref, err := Rebuild(cur, "lmstudio/qwen3-32b")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "lmstudio/qwen3-32b" || prov.Name() != "lmstudio" {
		t.Errorf("ref must switch provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildUnknownProviderErrors(t *testing.T) {
	setHome(t)
	_, _, err := Rebuild(Brain{ProviderName: "anthropic"}, "nope/m")
	if err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestRebuildFromBareArgResolvesAgainstCurrentRefProvider(t *testing.T) {
	setHome(t)
	prov, ref, err := RebuildFrom("lmstudio/qwen3-32b", "other-model")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "lmstudio/other-model" || prov.Name() != "lmstudio" {
		t.Errorf("bare arg must resolve against currentRef's provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildFromArgWithSlashSwitchesProvider(t *testing.T) {
	setHome(t)
	prov, ref, err := RebuildFrom("lmstudio/qwen3-32b", "ollama/llama3.3")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "ollama/llama3.3" || prov.Name() != "ollama" {
		t.Errorf("slash arg must switch provider regardless of currentRef: %s %s", ref, prov.Name())
	}
}

func TestRebuildFromGarbageProviderErrors(t *testing.T) {
	setHome(t)
	_, _, err := RebuildFrom("lmstudio/qwen3-32b", "nope/m")
	if err == nil {
		t.Fatal("unknown provider must error")
	}
}

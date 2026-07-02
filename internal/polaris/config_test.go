package polaris

import (
	"os"
	"testing"
)

func TestConfigStoreRoundTrip(t *testing.T) {
	cs, err := NewConfigStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c, err := cs.Load(); err != nil || c.MCPURL != "" {
		t.Fatalf("absent config should load as zero, got %+v err=%v", c, err)
	}
	if err := cs.Save(Config{MCPURL: "https://app.revelara.ai/mcp"}); err != nil {
		t.Fatal(err)
	}
	c, err := cs.Load()
	if err != nil || c.MCPURL != "https://app.revelara.ai/mcp" {
		t.Fatalf("round-trip = %+v err=%v", c, err)
	}
	if fi, _ := os.Stat(cs.Path()); fi.Mode().Perm() != 0o600 {
		t.Errorf("config perms = %o, want 600", fi.Mode().Perm())
	}
}

func TestResolveMCPURLPriority(t *testing.T) {
	cfg := Config{MCPURL: "https://cfg/mcp"}
	tok := Token{BaseURL: "https://tok/mcp"}
	if got := ResolveMCPURL("https://env/mcp", cfg, tok); got != "https://env/mcp" {
		t.Errorf("env override should win, got %s", got)
	}
	if got := ResolveMCPURL("", cfg, tok); got != "https://cfg/mcp" {
		t.Errorf("config should be next, got %s", got)
	}
	if got := ResolveMCPURL("", Config{}, tok); got != "https://tok/mcp" {
		t.Errorf("token endpoint should be last, got %s", got)
	}
	if got := ResolveMCPURL("", Config{}, Token{}); got != "" {
		t.Errorf("none set → empty, got %s", got)
	}
}

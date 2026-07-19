package conductor

import (
	"path/filepath"
	"testing"
)

// TestChooseEmbedderOptOut (or-o213): semantic recall is opt-OUT — on by
// default exactly when provisioned; "off" wins even when provisioned; an
// explicit provider config is honored regardless of provisioning.
func TestChooseEmbedderOptOut(t *testing.T) {
	always := func(string) bool { return true }
	never := func(string) bool { return false }

	cfg, on := chooseEmbedder("", "/data", always)
	if !on || cfg.Provider != "local" || cfg.ModelPath != filepath.Join("/data", "models") {
		t.Fatalf("unset + provisioned must be ON with the default local config, got on=%v cfg=%+v", on, cfg)
	}
	if _, on := chooseEmbedder("", "/data", never); on {
		t.Fatal("unset + unprovisioned must stay off (keyword+heat)")
	}
	if _, on := chooseEmbedder("", "", always); on {
		t.Fatal("no data dir must stay off")
	}
	for _, v := range []string{"off", "none", "0"} {
		if _, on := chooseEmbedder(v, "/data", always); on {
			t.Fatalf("explicit %q must disable even when provisioned", v)
		}
	}
	cfg, on = chooseEmbedder("local", "/data", never)
	if !on || cfg.Provider != "local" {
		t.Fatalf("an explicit provider must be honored regardless of provisioning, got on=%v cfg=%+v", on, cfg)
	}
}

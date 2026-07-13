package orchestrator

import (
	"os"
	"testing"
	"time"
)

// or-7et.1(1): the shadow proposer runs on the synchronous plan path, so it
// must be time-bounded — override via env, sane default, floor at 1s.
func TestShadowProposerTimeout(t *testing.T) {
	if v, ok := os.LookupEnv("ORION_SHADOW_PROPOSER_TIMEOUT_S"); ok {
		defer os.Setenv("ORION_SHADOW_PROPOSER_TIMEOUT_S", v)
	} else {
		defer os.Unsetenv("ORION_SHADOW_PROPOSER_TIMEOUT_S")
	}

	os.Unsetenv("ORION_SHADOW_PROPOSER_TIMEOUT_S")
	if got := shadowProposerTimeout(); got != 45*time.Second {
		t.Fatalf("default must be 45s, got %v", got)
	}
	os.Setenv("ORION_SHADOW_PROPOSER_TIMEOUT_S", "5")
	if got := shadowProposerTimeout(); got != 5*time.Second {
		t.Fatalf("env override must apply, got %v", got)
	}
	// A bogus / sub-1 value falls back to the default (never a 0 timeout that
	// would insta-cancel every shadow run).
	os.Setenv("ORION_SHADOW_PROPOSER_TIMEOUT_S", "0")
	if got := shadowProposerTimeout(); got != 45*time.Second {
		t.Fatalf("sub-1 must fall back to default, got %v", got)
	}
	os.Setenv("ORION_SHADOW_PROPOSER_TIMEOUT_S", "garbage")
	if got := shadowProposerTimeout(); got != 45*time.Second {
		t.Fatalf("garbage must fall back to default, got %v", got)
	}
}

package proofexec

import (
	"strings"
	"testing"
)

// or-mkxd: the hermetic proof env READS the host module cache — deps are
// provisioned OUTSIDE (generation time, ensureModDeps), proven INSIDE with
// GOPROXY still off. The goToolchain abstraction already passed GOMODCACHE
// through; plain toolEnv (the path behavioral proofs build under) did not —
// that gap made any generated code importing an uncached module an invariant
// '[build failed]' (or-4rxw: grpc-svc, three burned attempts).
func TestToolEnvPassesHostModuleCache(t *testing.T) {
	env := toolEnv("/goroot", "/work")
	if got, want := env["GOMODCACHE"], hostModCache(); got != want {
		t.Fatalf("toolEnv GOMODCACHE = %q, want the shared host resolver's %q", got, want)
	}
	if env["GOPROXY"] != "off" {
		t.Fatalf("GOPROXY must stay off (no network under proof), got %q", env["GOPROXY"])
	}
	if mc := env["GOMODCACHE"]; mc != "" && strings.HasPrefix(mc, "/work") {
		t.Fatalf("the module cache must be the HOST cache, never the workdir: %q", mc)
	}
}

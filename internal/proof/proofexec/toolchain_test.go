package proofexec

import (
	"strings"
	"testing"
)

// TestGoToolchainSnapshot (or-4y7.2): the Go toolchain — the default — emits the
// V2.0 allowlist, denied subcommands, and env verbatim. This pins byte-identity
// so the language-dispatch refactor is provably a no-op on the Go path.
func TestGoToolchainSnapshot(t *testing.T) {
	tc := toolchainFor("")
	if tc == nil || tc.Language() != "go" {
		t.Fatal(`toolchainFor("") must resolve to the go toolchain`)
	}
	if toolchainFor("go") != tc {
		t.Fatal(`toolchainFor("go") must equal toolchainFor("")`)
	}
	if toolchainFor("ruby") != nil {
		t.Fatal("an unregistered language must resolve to nil, never a silent Go toolchain")
	}

	// Allowlist: exactly go + golangci-lint.
	if err := tc.Allow("go", []string{"build", "./..."}); err != nil {
		t.Fatalf("`go build` must be allowed: %v", err)
	}
	if err := tc.Allow("golangci-lint", []string{"run"}); err != nil {
		t.Fatalf("golangci-lint must be allowed: %v", err)
	}
	if err := tc.Allow("bash", nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("a non-allowlisted tool must be refused, got %v", err)
	}
	// Denied go subcommands (arbitrary-code vectors).
	for _, sub := range []string{"run", "generate", "get", "install", "tool"} {
		if err := tc.Allow("go", []string{sub, "./..."}); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("`go %s` must be denied, got %v", sub, err)
		}
	}

	// Env: the hermetic keys pinned to the V2.0 values.
	env := tc.Env("/tmp/wd")
	for k, want := range map[string]string{
		"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOENV": "off",
		"CGO_ENABLED": "0", "GOFLAGS": "",
	} {
		if env[k] != want {
			t.Errorf("env[%q] = %q, want %q", k, env[k], want)
		}
	}
	if env["GOROOT"] == "" {
		t.Error("GOROOT must be bound")
	}

	// `go` is the PRIMARY toolchain binary (resident under Roots, override-
	// eligible); golangci-lint is auxiliary.
	if !tc.IsPrimary("go") || tc.IsPrimary("golangci-lint") {
		t.Fatalf("IsPrimary: go must be primary, golangci-lint auxiliary")
	}
	p, err := tc.ResolveBin("", "go")
	if err != nil || !strings.HasSuffix(p, "/bin/go") {
		t.Fatalf("ResolveBin(go) = %q err=%v", p, err)
	}
	if !tc.UnsafeNoneOverride() {
		t.Error("the Go toolchain must permit the operator unisolated-backend override")
	}
	if len(tc.Roots("")) == 0 || !strings.HasSuffix(tc.Roots("")[0], strings.TrimSuffix(p, "/bin/go")) {
		t.Errorf("Roots must include GOROOT (%v)", tc.Roots(""))
	}
}

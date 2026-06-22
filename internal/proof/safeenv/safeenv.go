// Package safeenv builds a scrubbed environment for proof execs. The proof
// pipeline compiles and runs GENERATED (potentially untrusted) code via `go
// build`/`go test`; those execs must NOT inherit the host process environment,
// which — since the native-harness pivot — holds the model API key
// (ANTHROPIC_API_KEY) and other secrets. A hostile generated artifact's init()
// or TestMain could otherwise read the key during the build/test phase and
// exfiltrate it. This is deny-by-default: only the Go-toolchain + system vars the
// build genuinely needs pass through; everything else (every secret) is dropped.
//
// This is the env-scrub layer of the boundary; full process isolation
// (network + filesystem, via internal/sandbox bwrap) is the comprehensive fix.
package safeenv

import (
	"os"
	"strings"
)

// allowed is the set of environment variable names a `go build`/`go test` of the
// generated service legitimately needs. Anything not here (notably API keys,
// tokens, and credentials) is dropped.
var allowed = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
	"TMPDIR": true, "TMP": true, "TEMP": true,
	"GOPATH": true, "GOROOT": true, "GOCACHE": true, "GOMODCACHE": true,
	"GOPROXY": true, "GOSUMDB": true, "GONOSUMCHECK": true, "GONOSUMDB": true,
	"GOPRIVATE": true, "GOTOOLCHAIN": true, "GO111MODULE": true, "GOWORK": true,
	"GOOS": true, "GOARCH": true, "GOARM": true, "GOTMPDIR": true, "GOENV": true,
	"GODEBUG": true, "CGO_ENABLED": true, "GOMAXPROCS": true,
}

// Build returns a scrubbed environment for `go build`/`go test` of generated
// code: the allowlisted toolchain/system vars from the host, with GOFLAGS forced
// empty (an inherited -mod=vendor etc. would break the isolated build) and no
// secrets. The host's API keys never reach the code under proof.
func Build() []string {
	out := make([]string, 0, len(allowed)+1)
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if ok && allowed[k] && k != "GOFLAGS" {
			out = append(out, kv)
		}
	}
	out = append(out, "GOFLAGS=")
	return out
}

// Map returns the same scrubbed environment as Build in key/value form, for
// callers (e.g. internal/sandbox, which takes a map[string]string) that need it
// shaped that way. Same allowlist, same GOFLAGS-forced-empty, same secret drop.
func Map() map[string]string {
	m := make(map[string]string, len(allowed)+1)
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok && allowed[k] && k != "GOFLAGS" {
			m[k] = v
		}
	}
	m["GOFLAGS"] = ""
	return m
}

package proofexec

// CapabilityManifest describes the hermetic proof environment for the agents
// that generate code proven under it (or-fvkm, CAST F2: the generator had no
// representation of these constraints — a controller with a flawed process
// model and no feedback path to correct it). Kept ADJACENT to toolEnv so the
// description and the enforcement drift together or not at all. Rendered into
// the change-flow context (submit_change_intent result, diffgen role).
func CapabilityManifest() string {
	return `PROOF ENVIRONMENT (hermetic — generated code must build and pass here):
- network: DENIED (GOPROXY=off). Module deps are auto-provisioned by the harness at generation time (go mod tidy + download to the host cache the proof env reads). Never write code that fetches anything at build or test time.
- protoc: ABSENT under proof. Run any codegen at generation time and commit the generated files (.pb.go etc.) as ordinary source.
- CGO: DISABLED (CGO_ENABLED=0) — pure-Go only.
- toolchain: local only (GOTOOLCHAIN=local); minimal PATH (go + allowlisted verify tools). No external binaries at proof time.`
}

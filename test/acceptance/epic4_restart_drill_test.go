//go:build epic4_live_minikube

package acceptance_test

// Epic 4 restart-recovery drill (SPEC §25.3). Build tag
// `epic4_live_minikube` keeps it out of the default `go test ./...`
// cycle until the slices that ship the Conductor + Lookout + worker
// land. In dry-run mode the file is compile-checked but not executed;
// in live mode the smoke wrapper invokes it explicitly via
// `go test -tags=epic4_live_minikube`.
//
// What this drill exercises (the bookend the rest of Epic 4 must
// satisfy):
//
//  1. Bring up two Conductor replicas wired to the same Postgres.
//  2. Replica A leader-elects via the PG advisory lock and reads the
//     fixture backlog.json. Conductor claims fixture-issue-1.
//  3. Conductor spawns an orion-worker pod in a per-run K8s namespace
//     that mounts the per-tenant repo cache. WorkerSession row is
//     created with a fencing_token guard for replica A.
//  4. While the worker is still running, the test kills replica A.
//  5. Replica B acquires the leader lease, increments the fencing
//     token, and reads the in-flight run state from the DB.
//  6. The Lookout (co-located with the worker) re-attaches to replica
//     B and continues to report progress.
//  7. The worker completes its phase chain (spawning, running,
//     verifying, pr_opened) and the namespace is torn down.
//
// Invariants asserted (pinned in expected_restart_drill_shape.json):
//
//   * No two worker pods exist with the same workspace_key in the
//     entire drill (idempotency holds across leader handover).
//   * Every state mutation after the kill carries replica B's
//     fencing_token; replica A's token is now stale.
//   * The run reaches state `completed` exactly once.
//   * The per-run K8s namespace is deleted by run-end + namespace
//     reaper grace period.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fixtureRoot returns the absolute path to the Epic 4 fixture. Used by
// every subtest so the drill is invariant under cwd.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "fixtures", "epic4-conductor")
}

// shapeContract returns the parsed expected-shape JSON. Each drill
// subtest reads its invariants from here so the contract and the test
// agree.
func shapeContract(t *testing.T) map[string]any {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	p := filepath.Join(filepath.Dir(here), "expected_restart_drill_shape.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read shape contract: %v", err)
	}
	var shape map[string]any
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatalf("parse shape contract: %v", err)
	}
	return shape
}

// requireLiveEnv guards the drill against running without the
// operator-provisioned pre-conditions. The smoke wrapper validates
// these too; duplicating here so a direct `go test` invocation cannot
// silently skip the prerequisites.
func requireLiveEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"POSTGRES_DSN",
		"ORION_TENANT_ID",
		"ORION_NAMESPACE",
		"ORION_LEADER_LEASE_SECONDS",
		"ORION_FIXTURE_REPO_PATH",
	} {
		if os.Getenv(key) == "" {
			t.Skipf("missing env %s; see docs/runbooks/epic4_smoke.md", key)
		}
	}
}

func TestEpic4FixtureIsAddressable(t *testing.T) {
	requireLiveEnv(t)
	root := fixtureRoot(t)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("fixture not addressable: %v", err)
	}
}

// The body of the actual restart drill is intentionally a hard-fail
// placeholder until the Conductor + Lookout + worker slices land. It
// asserts each invariant pinned in expected_restart_drill_shape.json
// so the bookend cannot silently drift away from the contract.
//
// Slices that close the placeholders:
//
//	orion-e41: leader election (advisory lock + fencing tokens)
//	orion-e42: Run + IssueClaim state machines
//	orion-e43: WorkerSession + SpawnIntent + idempotent pod create
//	orion-e44: orion-worker binary + AgentRunner contract
//	orion-e45: per-tenant repo cache + NetworkPolicy isolation
//	orion-e46: live K8s harness materializer
//	orion-e47: Lookout reconciler + heartbeat
//	orion-e48: Conductor scheduler tick
//	orion-e49: live PR opening from worker
func TestEpic4RestartRecoveryDrill(t *testing.T) {
	requireLiveEnv(t)
	shape := shapeContract(t)
	invariants, ok := shape["invariants"].(map[string]any)
	if !ok {
		t.Fatal("shape.invariants is not a map")
	}
	replicas, ok := invariants["conductor_replicas"].(float64)
	if !ok || int(replicas) < 2 {
		t.Fatalf("invariant.conductor_replicas=%v; need >=2 for the drill", invariants["conductor_replicas"])
	}

	// The implementation of each step lives behind the slices listed
	// above. Until orion-e48 (the Conductor scheduler tick) closes,
	// this test must fail loudly when run live so the bookend stays
	// the pinned failing target.
	t.Fatal("restart-recovery drill not yet wired: requires orion-e41..orion-e49 to merge first")
}

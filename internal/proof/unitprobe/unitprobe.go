// Package unitprobe is the empirical channel for unit cases (or-v9f.23 §B2):
// it builds the synthesized driver WITH the artifact module and re-execs it per
// restart segment in one scratch cwd — crossing a segment is a genuine process
// boundary while on-disk state persists (R9-class persistence proofs). The
// driver binary derives from the UNTRUSTED artifact, so every run is sandboxed
// exactly like execprobe's.
package unitprobe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
)

const segmentTimeout = 15 * time.Second

// BuildDriver synthesizes and compiles the unit driver inside the staged
// artifact dir. Returns the driver binary path, or ok=false with detail when
// the artifact carries no unit cases or the driver cannot build (obligations
// then stay unexecuted — a loud coverage hole, never a silent pass).
func BuildDriver(ctx context.Context, stagedDir string, cases []spec.BehavioralCase) (bin string, ok bool, detail string, err error) {
	files := testsynth.SynthesizeUnitDriver(cases, modulePath(stagedDir))
	if files == nil {
		return "", false, "no unit cases", nil
	}
	for rel, content := range files {
		p := filepath.Join(stagedDir, rel)
		if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
			return "", false, "", e
		}
		if e := os.WriteFile(p, []byte(content), 0o644); e != nil {
			return "", false, "", e
		}
	}
	out, code, e := proofexec.GoToolchain(ctx, stagedDir, "build", "-o", "unit_driver", "./orion_unit_driver")
	if e != nil {
		return "", false, "", fmt.Errorf("unit driver build exec: %w", e)
	}
	if code != 0 {
		return "", false, "unit driver build failed (are the case calls on the EXPORTED surface?): " + strings.TrimSpace(out), nil
	}
	return filepath.Join(stagedDir, "unit_driver"), true, "", nil
}

// RunRound executes every unit case once: per case a fresh scratch cwd, per
// segment one sandboxed driver invocation. All segments exit 0 → passed.
func RunRound(ctx context.Context, driverBin string, cases []spec.BehavioralCase) (map[string]truthalign.ObligationStatus, string) {
	obs := map[string]truthalign.ObligationStatus{}
	var fails []string
	backend, err := sandbox.New(os.Getenv("ORION_SANDBOX_ISOLATION"))
	if err != nil {
		backend, _ = sandbox.New("none")
	}
	for _, cs := range cases {
		if cs.Kind != spec.KindUnit || cs.Unit == nil {
			continue
		}
		pass, detail := runCase(ctx, backend, driverBin, cs)
		obs[cs.ID] = truthalign.ObligationStatus{Executed: true, Passed: pass}
		if !pass {
			fails = append(fails, cs.ID+": "+detail)
		}
	}
	return obs, strings.Join(fails, "; ")
}

func runCase(ctx context.Context, backend sandbox.Backend, driverBin string, cs spec.BehavioralCase) (bool, string) {
	scratch, err := os.MkdirTemp("", "orion-unitprobe-*")
	if err != nil {
		return false, "scratch dir: " + err.Error()
	}
	defer os.RemoveAll(scratch)
	for seg := 0; seg < testsynth.UnitSegments(cs); seg++ {
		cctx, cancel := context.WithTimeout(ctx, segmentTimeout)
		res, rerr := backend.Run(cctx, sandbox.Spec{
			Workdir: scratch,
			Argv:    []string{driverBin, cs.ID, fmt.Sprint(seg)},
			ROBinds: []string{filepath.Dir(driverBin)},
		})
		cancel()
		if rerr != nil {
			return false, fmt.Sprintf("segment %d: %v", seg, rerr)
		}
		if res.ExitCode != 0 {
			return false, fmt.Sprintf("segment %d: %s", seg, strings.TrimSpace(res.Stderr))
		}
	}
	return true, ""
}

// modulePath reads the module directive from go.mod.
func modulePath(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "artifact"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(m)
		}
	}
	return "artifact"
}

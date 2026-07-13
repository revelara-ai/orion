// Package execprobe is the empirical channel for exec cases (or-v9f.3,
// Orion-Obligation-Vocabulary-Design §4.3): it runs the REAL built binary with
// the ratified argv in a seeded scratch dir under the proof sandbox and
// evaluates exit/stdout/stderr through the compiled-in casecheck oracle — the
// identical semantics the behavioral corpus embeds. Behavioral proves the entry
// function; this proves the SHIPPED process: argv parsing in main, real exit
// codes, env plumbing, the OS boundary.
package execprobe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/casecheck"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
)

const defaultWithinMS = 15000

// TimeScale reads ORION_PROOF_TIME_SCALE (float >= 1.0): an execution-time
// multiplier that absorbs infra slowness WITHOUT touching case identity —
// deadlines are semantics and are hashed; calibration never re-anchors a spec.
func TimeScale() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("ORION_PROOF_TIME_SCALE"), 64); err == nil && v >= 1.0 {
		return v
	}
	return 1.0
}

// RunRound executes every exec case once against the built binary and returns
// per-case obligations plus a joined detail line for failures. A fresh scratch
// dir is seeded per case; the binary runs sandboxed (no network, scrubbed env +
// the case's allowlisted env), with the case's semantic deadline × timeScale.
func RunRound(ctx context.Context, bin string, cases []spec.BehavioralCase, timeScale float64) (map[string]truthalign.ObligationStatus, string) {
	obs := map[string]truthalign.ObligationStatus{}
	var fails []string
	backend, err := sandbox.New(os.Getenv("ORION_SANDBOX_ISOLATION"))
	if err != nil {
		backend, _ = sandbox.New("none")
	}
	for _, c := range cases {
		if c.Kind != spec.KindExec || c.Exec == nil || len(c.Exec.Steps) == 0 {
			continue
		}
		pass, detail := runCase(ctx, backend, bin, c, timeScale)
		obs[c.ID] = truthalign.ObligationStatus{Executed: true, Passed: pass}
		if !pass {
			fails = append(fails, c.ID+": "+detail)
		}
	}
	return obs, strings.Join(fails, "; ")
}

func runCase(ctx context.Context, backend sandbox.Backend, bin string, c spec.BehavioralCase, timeScale float64) (bool, string) {
	st := c.Exec.Steps[0]
	scratch, err := os.MkdirTemp("", "orion-execprobe-*")
	if err != nil {
		return false, "scratch dir: " + err.Error()
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	for _, s := range c.Exec.Seed {
		p := filepath.Join(scratch, s.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return false, "seed: " + err.Error()
		}
		if err := os.WriteFile(p, []byte(s.Content), 0o644); err != nil {
			return false, "seed: " + err.Error()
		}
	}

	withinMS := st.Expect.WithinMS
	if withinMS == 0 {
		withinMS = defaultWithinMS
	}
	deadline := time.Duration(float64(withinMS)*timeScale) * time.Millisecond
	cctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	argv := append([]string{bin}, st.Argv[1:]...) // "$BIN" resolved to the real binary
	res, err := backend.Run(cctx, sandbox.Spec{
		Workdir: scratch,
		Argv:    argv,
		Stdin:   st.Stdin,
		Env:     st.Env, // allowlisted at compile; the backend scrubs the rest
		ROBinds: []string{filepath.Dir(bin)},
	})
	if cctx.Err() == context.DeadlineExceeded {
		return false, fmt.Sprintf("deadline: no exit within %s (within_ms %d × scale %.1f)", deadline, withinMS, timeScale)
	}
	if err != nil {
		return false, "exec: " + err.Error()
	}

	if st.Expect.Exit != nil {
		if ok, detail := casecheck.OrionCheckExit(*st.Expect.Exit, res.ExitCode); !ok {
			return false, detail
		}
	}
	for _, a := range st.Expect.Stdout {
		if ok, detail := casecheck.OrionCheckStream(string(a.Kind), a.Value, a.Key, res.Stdout); !ok {
			return false, "stdout " + detail
		}
	}
	for _, a := range st.Expect.Stderr {
		if ok, detail := casecheck.OrionCheckStream(string(a.Kind), a.Value, a.Key, res.Stderr); !ok {
			return false, "stderr " + detail
		}
	}
	return true, ""
}

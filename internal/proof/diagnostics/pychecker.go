package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/proofexec"
)

// pyChecker is Python's fast-feedback static tier (or-4y7.9, library tracer):
// Check byte-compiles every production .py in the sandboxed interpreter
// (`python3 -m py_compile` — the cheapest whole-artifact syntax gate; there is
// no build step to piggyback on). CheckEntry is trivially OK: the tracer's
// program family is library, whose obligations are unit cases calling the
// module surface — there is no single entry symbol contract. CheckUnitRefs
// syntax-compiles every unit Call/Want expression with the same interpreter
// (in a scratch dir, never the artifact) so a malformed case fails in seconds,
// before the heavier proof modes; semantic validity (the module actually
// exposing the called surface) is the behavioral mode's job.
type pyChecker struct{}

func (pyChecker) Language() string { return "python" }

func (pyChecker) Check(ctx context.Context, dir string) Result {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".py") && !strings.HasPrefix(name, "orion_") {
			rel, rerr := filepath.Rel(dir, path)
			if rerr != nil {
				return rerr
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return Result{OK: false, Output: "walk: " + err.Error()}
	}
	if len(files) == 0 {
		return Result{OK: false, Output: "no .py sources in the artifact"}
	}
	args := append([]string{"-m", "py_compile"}, files...)
	stdout, stderr, code, rerr := proofexec.RunTool(ctx, dir, "python", "python3", args...)
	if rerr != nil {
		return Result{OK: false, Output: "py_compile launch: " + rerr.Error()}
	}
	if code != 0 {
		return Result{OK: false, Output: clip(strings.TrimSpace(stdout+stderr), 4000)}
	}
	return Result{OK: true}
}

func (pyChecker) CheckEntry(_, _ string) Result {
	// Library family: obligations call the module surface directly; no
	// entry-symbol contract exists to conform to.
	return Result{OK: true}
}

func (pyChecker) CheckUnitRefs(_ string, cases []spec.BehavioralCase) Result {
	var b strings.Builder
	b.WriteString("# Orion unit-case syntax gate (harness-authored).\nbad = []\n")
	n := 0
	for _, cs := range cases {
		if cs.Kind != spec.KindUnit || cs.Unit == nil {
			continue
		}
		for _, st := range cs.Unit.Steps {
			exprs := []string{st.Call}
			if st.Want != "" {
				exprs = append(exprs, st.Want)
			}
			for _, e := range exprs {
				fmt.Fprintf(&b, "try:\n    compile(%q, %q, 'eval')\nexcept SyntaxError as e:\n    bad.append(%q + ': ' + str(e))\n", e, "case "+cs.ID, cs.ID+" "+e)
				n++
			}
		}
	}
	if n == 0 {
		return Result{OK: true}
	}
	b.WriteString("if bad:\n    print('\\n'.join(bad))\n    raise SystemExit(1)\n")
	scratch, err := os.MkdirTemp("", "orion-pyrefs-*")
	if err != nil {
		return Result{OK: false, Output: "scratch: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	if err := os.WriteFile(filepath.Join(scratch, "orion_refs_check.py"), []byte(b.String()), 0o644); err != nil {
		return Result{OK: false, Output: "write check: " + err.Error()}
	}
	stdout, stderr, code, rerr := proofexec.RunTool(context.Background(), scratch, "python", "python3", "orion_refs_check.py")
	if rerr != nil {
		return Result{OK: false, Output: "refs check launch: " + rerr.Error()}
	}
	if code != 0 {
		return Result{OK: false, Output: "unit case expressions do not compile as python:\n" + clip(strings.TrimSpace(stdout+stderr), 2000)}
	}
	return Result{OK: true}
}

func init() { registerChecker(pyChecker{}) }

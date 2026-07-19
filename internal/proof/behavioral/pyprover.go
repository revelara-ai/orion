package behavioral

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// pyProver is Python's behavioral-proof surface (or-4y7.9, library tracer): it
// authors a stdlib-unittest corpus from the contract's UNIT cases (call an
// expression in the artifact module's namespace, compare to a literal or
// require a raising call matching a regex), runs it with the sandboxed
// interpreter (net denied, hermetic env), and emits the same
// ORION_OBLIGATION_RUN/PASS marker protocol the shared parseObligations reads.
// A case kind the tracer does not cover emits no test — its obligation shows
// as never-executed downstream, a visible coverage hole, never a silent pass.
// MutationScore is UNMEASURED (0,0): the shared gate reads that as
// Inconclusive — the honest reduced-proof posture until a Python mutation
// engine exists.
type pyProver struct{}

func (pyProver) Language() string { return "python" }

// pyIdentRE constrains the import name a unit case targets — it is interpolated
// into generated source, so only a plain (dotted) module identifier is allowed.
var pyIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

func (pyProver) SynthesizeCorpus(c testsynth.Contract, _ string) (map[string]string, error) {
	var unit []spec.BehavioralCase
	for _, cs := range c.Cases {
		if cs.Kind == spec.KindUnit && cs.Unit != nil {
			unit = append(unit, cs)
		}
	}
	if len(unit) == 0 {
		return nil, fmt.Errorf("python behavioral corpus: the contract has no unit cases — the python library tracer proves through unit obligations (declare them in the spec)")
	}
	var b strings.Builder
	b.WriteString("# Orion proof-domain corpus (authored by the harness, never the generating agent).\n")
	b.WriteString("import importlib\nimport re\nimport unittest\n\n\nclass OrionBehavioral(unittest.TestCase):\n")
	for i, cs := range unit {
		mod := strings.TrimSpace(cs.Unit.Pkg)
		if !pyIdentRE.MatchString(mod) {
			return nil, fmt.Errorf("python unit case %s: pkg %q is not an importable module name", cs.ID, cs.Unit.Pkg)
		}
		fmt.Fprintf(&b, "    def test_case_%03d(self):\n", i)
		fmt.Fprintf(&b, "        print(%q)\n", "ORION_OBLIGATION_RUN:"+cs.ID)
		fmt.Fprintf(&b, "        m = importlib.import_module(%q)\n", mod)
		fmt.Fprintf(&b, "        env = dict(vars(m))\n")
		for _, st := range cs.Unit.Steps {
			if st.WantErrRE != "" {
				fmt.Fprintf(&b, "        try:\n")
				fmt.Fprintf(&b, "            eval(compile(%q, '<case>', 'eval'), env)\n", st.Call)
				fmt.Fprintf(&b, "        except Exception as e:\n")
				fmt.Fprintf(&b, "            self.assertTrue(re.search(%q, str(e)), 'error %%r does not match %%r' %% (str(e), %q))\n", st.WantErrRE, st.WantErrRE)
				fmt.Fprintf(&b, "        else:\n")
				fmt.Fprintf(&b, "            self.fail('case %s: the call was required to raise')\n", cs.ID)
			} else {
				fmt.Fprintf(&b, "        got = eval(compile(%q, '<case>', 'eval'), env)\n", st.Call)
				fmt.Fprintf(&b, "        want = eval(compile(%q, '<want>', 'eval'), {})\n", st.Want)
				fmt.Fprintf(&b, "        self.assertEqual(got, want)\n")
			}
		}
		fmt.Fprintf(&b, "        print(%q)\n\n", "ORION_OBLIGATION_PASS:"+cs.ID)
	}
	b.WriteString("\nif __name__ == '__main__':\n    unittest.main(verbosity=2)\n")
	return map[string]string{"orion_behavioral_test.py": b.String()}, nil
}

func (pyProver) RunTests(ctx context.Context, proofDir string) (string, int, error) {
	// -u: unbuffered, so RUN markers survive a hard interpreter crash mid-case.
	stdout, stderr, code, err := proofexec.RunTool(ctx, proofDir, "python", "python3", "-u", "orion_behavioral_test.py")
	return stdout + stderr, code, err
}

// MutationScore: python has no mutation engine yet — a DECLARED capability
// fact; the shared gate labels the mode REDUCED (test-pass, quality unmeasured).
func (pyProver) MutationScore(context.Context, string, map[string]string, string, []string) (int, int, error) {
	return 0, 0, ErrMutationUnsupported
}

func init() { registerProver(pyProver{}) }

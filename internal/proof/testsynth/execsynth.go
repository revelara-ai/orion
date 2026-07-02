package testsynth

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/casecheck"
)

// execCaseTest emits the behavioral test for one exec case (or-v9f.3): seed a
// temp dir, call the artifact's run() entry IN-PROCESS with buffers, and assert
// via the embedded casecheck oracle. Bracketed by the same RUN/PASS markers the
// obligation parser already understands — zero gate changes.
func execCaseTest(cs spec.BehavioralCase) string {
	st := cs.Exec.Steps[0]
	var b strings.Builder
	fmt.Fprintf(&b, "\nfunc Test_obl_%s(t *testing.T) {\n", cs.ID)
	fmt.Fprintf(&b, "\tfmt.Println(%q)\n", "ORION_OBLIGATION_RUN:"+cs.ID)
	b.WriteString("\tdir := t.TempDir()\n")
	for _, s := range cs.Exec.Seed {
		fmt.Fprintf(&b, "\tif err := os.MkdirAll(filepath.Dir(filepath.Join(dir, %q)), 0o755); err != nil { t.Fatal(err) }\n", s.Path)
		fmt.Fprintf(&b, "\tif err := os.WriteFile(filepath.Join(dir, %q), []byte(%q), 0o644); err != nil { t.Fatal(err) }\n", s.Path, s.Content)
	}
	b.WriteString("\tt.Chdir(dir)\n")
	b.WriteString("\tvar stdout, stderr bytes.Buffer\n")
	fmt.Fprintf(&b, "\tenv := map[string]string{")
	for k, v := range st.Env {
		fmt.Fprintf(&b, "%q: %q, ", k, v)
	}
	b.WriteString("}\n")
	argv := make([]string, 0, len(st.Argv)-1)
	argv = append(argv, st.Argv[1:]...)
	fmt.Fprintf(&b, "\texit := run(%#v, strings.NewReader(%q), &stdout, &stderr, env)\n", argv, st.Stdin)
	if st.Expect.Exit != nil {
		fmt.Fprintf(&b, "\tif ok, detail := OrionCheckExit(%d, exit); !ok { t.Fatal(detail) }\n", *st.Expect.Exit)
	}
	for _, a := range st.Expect.Stdout {
		fmt.Fprintf(&b, "\tif ok, detail := OrionCheckStream(%q, %q, %q, stdout.String()); !ok { t.Fatal(detail) }\n", a.Kind, a.Value, a.Key)
	}
	for _, a := range st.Expect.Stderr {
		fmt.Fprintf(&b, "\tif ok, detail := OrionCheckStream(%q, %q, %q, stderr.String()); !ok { t.Fatal(detail) }\n", a.Kind, a.Value, a.Key)
	}
	fmt.Fprintf(&b, "\tfmt.Println(%q)\n}\n", "ORION_OBLIGATION_PASS:"+cs.ID)
	return b.String()
}

// execImports are the extra corpus imports exec cases need.
const execImports = `
import (
	"bytes"
	"os"
	"path/filepath"
)

var (
	_ = bytes.MinRead
	_ = os.WriteFile
	_ = filepath.Join
)
`

// SynthesizeSupportFiles returns files the corpus needs BESIDE the test file —
// the embedded casecheck oracle when exec cases are present (§4.1: one
// implementation, two compilation contexts).
func SynthesizeSupportFiles(c Contract) map[string]string {
	for _, cs := range c.Cases {
		if cs.Kind == spec.KindExec {
			return map[string]string{"orion_casecheck_test.go": casecheck.Source("main")}
		}
	}
	return nil
}

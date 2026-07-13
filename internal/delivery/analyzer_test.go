package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

func analyzerFixtureSpec() spec.ExecutableSpec {
	return spec.ExecutableSpec{
		Intent: "a time service",
		Hash:   "cafe1234cafe1234",
		ResponseContract: spec.ResponseContract{
			Port: 8080,
			Cases: []spec.BehavioralCase{{
				ID:      "case-now",
				Request: spec.RequestShape{Method: "GET", Path: "/time"},
				Expect: spec.ExpectShape{
					Status: 200, ContentType: "application/json",
					Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "now"}},
				},
			}},
		},
	}
}

func analyzerFixtureModel() stpa.Model {
	return stpa.Model{UCAs: []stpa.UCA{{ID: "UCA-1", Type: "not-provided", Hazard: "stale time served"}}}
}

func analyzerFixtureRunbook() Runbook {
	return Runbook{Sections: map[string]string{"incident_response": "1. check the process\n2. curl /time"}}
}

// Determinism: the analyzer is generated data, not free-form code — identical
// inputs must yield byte-identical source carrying the spec anchor, the
// runbook steps, and the STPA hypotheses.
func TestGenerateAnalyzerDeterministic(t *testing.T) {
	a := GenerateAnalyzer(analyzerFixtureSpec(), analyzerFixtureModel(), analyzerFixtureRunbook())
	b := GenerateAnalyzer(analyzerFixtureSpec(), analyzerFixtureModel(), analyzerFixtureRunbook())
	if a != b {
		t.Fatal("generation is not deterministic")
	}
	for _, must := range []string{"cafe1234cafe1234", "curl /time", "UCA-1", "DO NOT EDIT"} {
		if !strings.Contains(a, must) {
			t.Fatalf("generated source missing %q", must)
		}
	}
}

// The or-nkcf done-when: the emitted analyzer COMPILES standalone and
// correctly triages a live service — clean on a conformant one (exit 0),
// findings + hypotheses + proposed actions on a broken one (exit 2).
func TestGeneratedAnalyzerTriagesLiveService(t *testing.T) {
	src := GenerateAnalyzer(analyzerFixtureSpec(), analyzerFixtureModel(), analyzerFixtureRunbook())
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "analyze"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "analyze", "main.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module analyzed\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "analyze")
	build := exec.Command("go", "build", "-o", bin, "./cmd/analyze")
	build.Dir = dir
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated analyzer does not compile: %v\n%s", err, out)
	}

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"now":%q}`, time.Now().UTC().Format(time.RFC3339))
	}))
	defer good.Close()
	out, err := exec.Command(bin, "-base", good.URL).CombinedOutput()
	if err != nil {
		t.Fatalf("conformant service must yield exit 0, got %v\n%s", err, out)
	}
	var rep struct {
		Checks   []struct{ OK bool }
		Findings []string
	}
	if jerr := json.Unmarshal(out, &rep); jerr != nil {
		t.Fatalf("report is not JSON: %v\n%s", jerr, out)
	}
	if len(rep.Checks) != 1 || !rep.Checks[0].OK || len(rep.Findings) != 0 {
		t.Fatalf("clean service misreported: %s", out)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer bad.Close()
	out, err = exec.Command(bin, "-base", bad.URL).CombinedOutput()
	var ee *exec.ExitError
	if err == nil {
		t.Fatalf("broken service must yield findings (exit 2), got exit 0:\n%s", out)
	} else if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("broken service must exit 2, got %v\n%s", err, out)
	}
	text := string(out)
	for _, must := range []string{"status 500, expected 200", "UCA-1", "check the process", "human validates"} {
		if !strings.Contains(text, must) {
			t.Fatalf("findings report missing %q:\n%s", must, text)
		}
	}
}

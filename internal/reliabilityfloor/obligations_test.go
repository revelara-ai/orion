package reliabilityfloor

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	mech, adv := Split([]Signal{
		{ID: "a", Check: Check{Kind: CheckGolangciLint, Linters: []string{"noctx"}}},
		{ID: "b", Check: Check{Kind: CheckNone}},
	})
	if len(mech) != 1 || mech[0].ID != "a" || len(adv) != 1 || adv[0].ID != "b" {
		t.Fatalf("split wrong: mech=%v adv=%v", mech, adv)
	}
}

func TestLintArgs(t *testing.T) {
	sigs := []Signal{
		{Check: Check{Kind: CheckGolangciLint, Linters: []string{"bodyclose", "noctx"}}},
		{Check: Check{Kind: CheckGolangciLint, Linters: []string{"noctx"}}},
	}
	got := LintArgs(sigs, []string{"internal/foo"})
	want := []string{"run", "--no-config", "--default=none", "--enable=bodyclose", "--enable=noctx", "internal/foo/..."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v want %v", got, want)
	}
}

func TestLintArgsEmpty(t *testing.T) {
	if LintArgs(nil, []string{"x"}) != nil {
		t.Fatal("no mechanizable signals -> nil args")
	}
	if LintArgs([]Signal{{Check: Check{Kind: CheckGolangciLint, Linters: []string{"noctx"}}}}, nil) != nil {
		t.Fatal("no dirs -> nil args")
	}
}

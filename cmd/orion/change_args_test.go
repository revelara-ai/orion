package main

import "testing"

// or-ux02: `orion change --help` executed a LIVE change with intent "--help"
// (worktree, generator, regression gate — uninvited actuation). Flag-shaped
// arguments must never become intents.
func TestChangeHelpPrintsUsageNotRun(t *testing.T) {
	if got := cmdChange([]string{"--help"}); got != 0 {
		t.Fatalf("--help must print usage and exit 0, got %d", got)
	}
	if got := cmdChange([]string{"-h"}); got != 0 {
		t.Fatalf("-h must print usage and exit 0, got %d", got)
	}
}

func TestChangeRefusesFlagShapedIntent(t *testing.T) {
	if got := cmdChange([]string{"--force", "do something"}); got != 2 {
		t.Fatalf("an unknown flag-shaped leading arg must refuse with usage (2), got %d", got)
	}
}

func TestChangeDoubleDashAllowsDashIntent(t *testing.T) {
	// After --, a dash-leading intent is deliberate. It must pass the arg
	// guard and proceed to provider selection (which fails in tests — exit 1,
	// NOT the usage error 2).
	t.Setenv("ORION_MODEL", "nonexistent-provider/nope")
	if got := cmdChange([]string{"--", "--weird intent starting with dashes"}); got == 2 {
		t.Fatal("a '--'-separated intent must not be refused by the flag guard")
	}
}

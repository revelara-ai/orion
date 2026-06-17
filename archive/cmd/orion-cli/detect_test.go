package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDetectCmd_RejectsMissingRequiredFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newDetectCmd(&stdout, &stderr)

	cases := []struct {
		name string
		args []string
	}{
		{"no flags", []string{}},
		{"only repo", []string{"--repo=/tmp/x"}},
		{"only service", []string{"--service=foo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			rc := cmd.Run(context.Background(), tc.args)
			if rc != 2 {
				t.Errorf("rc=%d, want 2; stderr=%q", rc, stderr.String())
			}
		})
	}
}

func TestDetectCmd_RejectsBadFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newDetectCmd(&stdout, &stderr)
	rc := cmd.Run(context.Background(), []string{
		"--repo=/tmp/x", "--service=foo", "--format=yaml",
	})
	if rc != 2 {
		t.Errorf("rc=%d, want 2; stderr=%q", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), "yaml") {
		t.Errorf("stderr should mention the bad format: %q", stderr.String())
	}
}

func TestDetectCmd_NameAndSynopsis(t *testing.T) {
	cmd := newDetectCmd(nil, nil)
	if cmd.Name() != "detect" {
		t.Errorf("Name=%q, want 'detect'", cmd.Name())
	}
	if cmd.Synopsis() == "" {
		t.Error("Synopsis is empty")
	}
}

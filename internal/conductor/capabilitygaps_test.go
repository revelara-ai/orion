package conductor

import (
	"errors"
	"strings"
	"testing"
)

// or-fvkm: an intent implying a generation-time tool the host lacks surfaces
// the gap BEFORE any generation attempt — prevention, where or-kf7o is the
// backstop. Three burned attempts on or-4rxw were this check not existing.
func TestCapabilityGapsDetectsMissingProtoc(t *testing.T) {
	orig := lookPathFn
	t.Cleanup(func() { lookPathFn = orig })

	lookPathFn = func(string) (string, error) { return "", errors.New("not found") }
	gaps := capabilityGaps("add a gRPC service for real-time inference")
	if len(gaps) != 1 || !strings.Contains(gaps[0], "protoc") {
		t.Fatalf("a gRPC intent with protoc missing must surface the gap, got %v", gaps)
	}

	lookPathFn = func(string) (string, error) { return "/usr/bin/protoc", nil }
	if gaps := capabilityGaps("add a gRPC service"); len(gaps) != 0 {
		t.Fatalf("protoc present → no gap, got %v", gaps)
	}

	lookPathFn = func(string) (string, error) { return "", errors.New("not found") }
	if gaps := capabilityGaps("fix the retry logic in the queue consumer"); len(gaps) != 0 {
		t.Fatalf("an unrelated intent must surface no gaps, got %v", gaps)
	}
}

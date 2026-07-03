package acp

import (
	"context"
	"net"
	"strings"
	"testing"
)

// session/control round-trips: the client's Control call reaches the agent's registered
// ControlFunc and returns its result. A nil control reports "not supported".
func TestSessionControlRoundTrip(t *testing.T) {
	t.Run("with control", func(t *testing.T) {
		res := controlOnce(t, func(_ context.Context, sid, op, arg string) (string, error) {
			return "did " + op + " arg=" + arg + " on " + sid, nil
		}, "compact", "")
		if res != "did compact arg= on s1" {
			t.Errorf("control result = %q", res)
		}
	})
	t.Run("no control", func(t *testing.T) {
		if res := controlOnce(t, nil, "model", "x"); !strings.Contains(res, "not supported") {
			t.Errorf("nil control should report unsupported, got %q", res)
		}
	})
}

func controlOnce(t *testing.T, fn ControlFunc, op, arg string) string {
	t.Helper()
	clientEnd, agentEnd := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); clientEnd.Close(); agentEnd.Close() })

	agent := NewAgent(agentEnd, agentEnd, nil).SetControl(fn)
	go func() { _ = agent.Run(ctx) }()
	client := NewClient(clientEnd, clientEnd, nil, nil)
	go func() { _ = client.Run(ctx) }()

	res, err := client.Control(ctx, "s1", op, arg)
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	return res
}

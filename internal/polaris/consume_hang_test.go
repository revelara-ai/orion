package polaris

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestLoadBoundedWhenServiceHangs (or-mvr.4, C8 / inc-3ik): the Polaris kill
// test that unreachability tests miss. A refused connection errors fast; an
// endpoint that ACCEPTS and never responds only returns via callTimeout. Load
// must come back within that bound with reduced context and NO error — the
// loop proceeds, degraded.
func TestLoadBoundedWhenServiceHangs(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out callTimeout against a hung endpoint")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold open, never respond
		}
	}()

	c := NewConsumer(NewMCPClient("http://"+ln.Addr().String(), "tok"), openStore(t))
	start := time.Now()
	rc, err := c.Load(context.Background(), "proj-hang", "time service")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("an outage must degrade, not error the loop: %v", err)
	}
	if !rc.Reduced {
		t.Fatal("a hung service must flag reduced reliability context")
	}
	// ensureInit (callTimeout) + concurrent tool calls (callTimeout) + margin.
	// ABSOLUTE bound, deliberately not derived from callTimeout — a mutated
	// constant must not stretch the assertion with it.
	if elapsed > 10*time.Second {
		t.Fatalf("hung MCP service wedged Load for %v (bound 10s)", elapsed)
	}
}

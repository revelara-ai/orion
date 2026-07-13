package notify

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestNotifyBoundedWhenWebhookHangs (or-mvr.4, C8 / inc-3ik): the telemetry
// kill test. A webhook endpoint that ACCEPTS the connection and never responds
// (worse than refused — no fast error) must not wedge the loop. The call sites
// pass the loop's long-lived context, so the guard under test is Notify's OWN
// internal timeout — the test deliberately passes context.Background().
func TestNotifyBoundedWhenWebhookHangs(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the internal delivery timeout against a hung endpoint")
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
			defer c.Close() // hold the connection open; never write a response
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- notify(context.Background(), Event{Kind: "delivered", Task: "t1"}, "http://"+ln.Addr().String(), http.DefaultClient)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a hung delivery must surface an error (the call sites log-and-drop it), not report success")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("hanging webhook wedged notification delivery — the internal timeout is gone")
	}
}

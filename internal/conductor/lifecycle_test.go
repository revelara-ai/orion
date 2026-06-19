package conductor

import (
	"os"
	"testing"
	"time"
)

// TestCleanupVetoBlocksTeardownWhileAlive: while the control process is alive the
// cleanup veto holds and the agent file is present; after a clean stop the veto
// releases and the file is removed.
func TestCleanupVetoBlocksTeardownWhileAlive(t *testing.T) {
	dir := t.TempDir()
	m := &LifecycleManager{Dir: dir, Command: []string{"sleep", "60"}}

	if err := m.Start(time.Unix(0, 0).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Running: veto blocks teardown, agent file present, status reports running.
	if !m.CleanupVeto() {
		t.Fatal("cleanup veto must block teardown while the conductor is alive")
	}
	if running, pid := m.Status(); !running || pid <= 0 {
		t.Fatalf("status = running:%v pid:%d, want running", running, pid)
	}
	if _, err := os.Stat(m.agentFilePath()); err != nil {
		t.Fatalf("agent file missing while running: %v", err)
	}

	// Stop: veto releases, file removed.
	if err := m.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if m.CleanupVeto() {
		t.Fatal("cleanup veto must release after stop")
	}
	if _, err := os.Stat(m.agentFilePath()); !os.IsNotExist(err) {
		t.Fatalf("agent file must be removed on clean stop (err=%v)", err)
	}
}

// TestStartRejectsDoubleStart: a second start while alive is refused.
func TestStartRejectsDoubleStart(t *testing.T) {
	dir := t.TempDir()
	m := &LifecycleManager{Dir: dir, Command: []string{"sleep", "60"}}
	if err := m.Start(""); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop()
	if err := m.Start(""); err == nil {
		t.Fatal("double start must be refused while running")
	}
}

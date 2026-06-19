// Package empirical is the Lookout proof mode (or-dzo, PRD Phase E9). A transient
// observer BUILDS and RUNS the real artifact, then probes the running service —
// port open + the response actually conforms to the ResponseContract. Reality
// beats the report: an artifact whose tests pass but whose binary does not serve
// correctly fails here.
//
// Manifesto: proof reflects reality, not the agent's report of reality.
package empirical

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// ProbeResult is the empirical evidence surfaced by `orion proof show`.
type ProbeResult struct {
	PortOpen                  bool   `json:"port_open"`
	ResponseContractSatisfied bool   `json:"response_contract_satisfied"`
	Detail                    string `json:"detail,omitempty"`
}

// Prove builds the artifact, runs it on a free port, and probes the real running
// service against the contract. The service runs in its own process group and is
// reaped after the probe.
func Prove(ctx context.Context, artifactDir string, c testsynth.Contract) (truthalign.ModeResult, ProbeResult, error) {
	if c.Route == "" {
		c.Route = "/time"
	}
	binDir, err := os.MkdirTemp("", "orion-lookout-*")
	if err != nil {
		return truthalign.ModeResult{}, ProbeResult{}, err
	}
	defer os.RemoveAll(binDir)
	bin := filepath.Join(binDir, "svc")

	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Dir = artifactDir
	if out, err := build.CombinedOutput(); err != nil {
		return failMode("build failed: " + strings.TrimSpace(string(out))), ProbeResult{Detail: "build failed"}, nil
	}

	port, err := freePort()
	if err != nil {
		return truthalign.ModeResult{}, ProbeResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	svc := exec.CommandContext(runCtx, bin)
	svc.Env = []string{"PORT=" + fmt.Sprint(port), "PATH=/usr/bin:/bin"}
	svc.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := svc.Start(); err != nil {
		return failMode("service did not start: " + err.Error()), ProbeResult{Detail: "start failed"}, nil
	}
	defer func() {
		if svc.Process != nil {
			_ = syscall.Kill(-svc.Process.Pid, syscall.SIGKILL)
			_, _ = svc.Process.Wait()
		}
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	pr := probeContract(addr, c)
	return modeFrom(pr), pr, nil
}

func modeFrom(pr ProbeResult) truthalign.ModeResult {
	pass := pr.PortOpen && pr.ResponseContractSatisfied
	rate := 0.0
	if pass {
		rate = 1.0
	}
	return truthalign.ModeResult{
		Mode:    "empirical",
		Pass:    pass,
		Output:  pr.Detail,
		Metrics: map[string]float64{"empirical_pass_rate": rate, "run_count": 1},
	}
}

func failMode(detail string) truthalign.ModeResult {
	return truthalign.ModeResult{Mode: "empirical", Pass: false, Output: detail, Metrics: map[string]float64{"empirical_pass_rate": 0, "run_count": 1}}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// probeContract drives the real running service. PortOpen means the service
// actually answered HTTP (not merely a TCP socket existing) — so a non-serving
// artifact, or a stale socket, reads as not-open. It retries transient
// connection errors until a deadline (the server may be mid-startup).
func probeContract(addr string, c testsynth.Contract) ProbeResult {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(6 * time.Second)
	var resp *http.Response
	var lastErr error
	for time.Now().Before(deadline) {
		resp, lastErr = client.Get("http://" + addr + c.Route)
		if lastErr == nil {
			break
		}
		resp = nil
		time.Sleep(150 * time.Millisecond)
	}
	if resp == nil {
		return ProbeResult{PortOpen: false, Detail: "service never answered HTTP: " + errString(lastErr)}
	}
	defer resp.Body.Close()

	// The service answered HTTP → the port is genuinely open and serving.
	pr := ProbeResult{PortOpen: true}
	if resp.StatusCode != http.StatusOK {
		pr.Detail = fmt.Sprintf("status %d", resp.StatusCode)
		return pr
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	ct := resp.Header.Get("Content-Type")

	if strings.ToLower(c.Format) == "text" {
		if !strings.Contains(ct, "text/plain") {
			pr.Detail = "content-type " + ct
			return pr
		}
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(string(body))); err != nil {
			pr.Detail = "body not RFC3339"
			return pr
		}
		pr.ResponseContractSatisfied = true
		pr.Detail = "ok"
		return pr
	}
	if !strings.Contains(ct, "application/json") {
		pr.Detail = "content-type " + ct
		return pr
	}
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		pr.Detail = "body not JSON"
		return pr
	}
	// Generalized response contract (or-cfz): require the spec's key. The default
	// (empty) is the time contract — key "time" whose value must be RFC3339 — so
	// the time-service path is unchanged; any other key is asserted present.
	key, rfc3339 := c.JSONKey()
	val, ok := m[key]
	if !ok {
		pr.Detail = "missing " + key + " field"
		return pr
	}
	if rfc3339 {
		if _, err := time.Parse(time.RFC3339, val); err != nil {
			pr.Detail = key + " not RFC3339"
			return pr
		}
	}
	pr.ResponseContractSatisfied = true
	pr.Detail = "ok"
	return pr
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

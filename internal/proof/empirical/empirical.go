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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/safeenv"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// ProbeResult is the empirical evidence surfaced by `orion proof show`.
type ProbeResult struct {
	PortOpen                  bool   `json:"port_open"`
	ResponseContractSatisfied bool   `json:"response_contract_satisfied"`
	Detail                    string `json:"detail,omitempty"`
	// Cases is per-behavioral-case executed/passed when the contract carries cases
	// (the requirements model). Nil for the legacy single-contract probe.
	Cases map[string]truthalign.ObligationStatus `json:"-"`
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
	build.Env = safeenv.Build() // scrubbed: building generated code never sees host secrets
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
		Mode:        "empirical",
		Pass:        pass,
		Output:      pr.Detail,
		Metrics:     map[string]float64{"empirical_pass_rate": rate, "run_count": 1},
		Obligations: pr.Cases,
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
	base := "http://" + addr

	// Wait for the service to genuinely answer HTTP (port open + serving).
	probeRoute := c.Route
	if len(c.Cases) > 0 {
		probeRoute = c.Cases[0].Request.Path
	}
	if !waitForService(client, base+probeRoute) {
		return ProbeResult{PortOpen: false, Detail: "service never answered HTTP"}
	}

	// Requirements model: execute every behavioral case against the live service.
	if len(c.Cases) > 0 {
		pr := ProbeResult{PortOpen: true, Cases: map[string]truthalign.ObligationStatus{}}
		allPass := true
		var fails []string
		for _, cs := range c.Cases {
			ok, detail := checkCaseLive(client, base, cs)
			pr.Cases[cs.ID] = truthalign.ObligationStatus{Executed: true, Passed: ok}
			if !ok {
				allPass = false
				fails = append(fails, cs.ID+": "+detail)
			}
		}
		pr.ResponseContractSatisfied = allPass
		if allPass {
			pr.Detail = "ok"
		} else {
			pr.Detail = strings.Join(fails, "; ")
		}
		return pr
	}

	// Legacy single-contract probe (no cases): unchanged behavior.
	resp, err := client.Get(base + c.Route)
	if err != nil || resp == nil {
		return ProbeResult{PortOpen: false, Detail: "service never answered HTTP: " + errString(err)}
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

// waitForService retries GET until the service answers HTTP or the deadline passes
// (the binary may be mid-startup). A real HTTP answer means the port is open and
// serving, not merely a TCP socket existing.
func waitForService(client *http.Client, url string) bool {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// checkCaseLive issues a behavioral case's request against the running service and
// checks its expectations (status, content-type, body assertions).
func checkCaseLive(client *http.Client, base string, cs spec.BehavioralCase) (bool, string) {
	method := cs.Request.Method
	if method == "" {
		method = "GET"
	}
	u := base + cs.Request.Path
	if len(cs.Request.Query) > 0 {
		vals := url.Values{}
		for k, v := range cs.Request.Query {
			vals.Set(k, v)
		}
		u += "?" + vals.Encode()
	}
	var bodyReader io.Reader
	if cs.Request.Body != "" {
		bodyReader = strings.NewReader(cs.Request.Body)
	}
	req, err := http.NewRequest(method, u, bodyReader)
	if err != nil {
		return false, "bad request: " + err.Error()
	}
	for k, v := range cs.Request.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "no response: " + err.Error()
	}
	defer resp.Body.Close()
	if cs.Expect.Status != 0 && resp.StatusCode != cs.Expect.Status {
		return false, fmt.Sprintf("status %d, want %d", resp.StatusCode, cs.Expect.Status)
	}
	if cs.Expect.ContentType != "" {
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, cs.Expect.ContentType) {
			return false, "content-type " + ct + ", want " + cs.Expect.ContentType
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return checkLiveAssertions(body, cs.Expect.Assertions)
}

// checkLiveAssertions checks the response body against a case's assertions.
func checkLiveAssertions(body []byte, as []spec.BodyAssertion) (bool, string) {
	var m map[string]string
	parsed := false
	jsonOK := func() bool {
		if !parsed {
			parsed = true
			if json.Unmarshal(body, &m) != nil {
				m = nil
			}
		}
		return m != nil
	}
	for _, a := range as {
		switch a.Kind {
		case spec.AssertJSONErrorPresent:
			if !jsonOK() {
				return false, "body not JSON"
			}
			if strings.TrimSpace(m["error"]) == "" {
				return false, "missing non-empty error key"
			}
		case spec.AssertJSONKeyPresent:
			if !jsonOK() {
				return false, "body not JSON"
			}
			if strings.TrimSpace(m[a.Key]) == "" {
				return false, "missing non-empty key " + a.Key
			}
		case spec.AssertJSONKeyRFC3339:
			if !jsonOK() {
				return false, "body not JSON"
			}
			if _, err := time.Parse(time.RFC3339, m[a.Key]); err != nil {
				return false, a.Key + " not RFC3339"
			}
		case spec.AssertJSONKeyInTZ:
			if !jsonOK() {
				return false, "body not JSON"
			}
			pt, err := time.Parse(time.RFC3339, m[a.Key])
			if err != nil {
				return false, a.Key + " not RFC3339"
			}
			loc, err := time.LoadLocation(a.Value)
			if err != nil {
				return false, "bad zone " + a.Value
			}
			_, want := pt.In(loc).Zone()
			_, got := pt.Zone()
			if got != want {
				return false, fmt.Sprintf("%s offset %d, want zone %s (%d)", a.Key, got, a.Value, want)
			}
		case spec.AssertBodyRFC3339:
			if _, err := time.Parse(time.RFC3339, strings.TrimSpace(string(body))); err != nil {
				return false, "body not RFC3339"
			}
		}
	}
	return true, "ok"
}

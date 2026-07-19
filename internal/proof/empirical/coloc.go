package empirical

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// Co-located empirical run (or-6lm): the untrusted RUNNING binary is proof's
// last unisolated execution — it runs arbitrary main with full host network
// and filesystem. --unshare-net alone would strand it away from a host-side
// probe, so the PROBE MOVES IN with it: orion itself (a hidden verb) runs
// inside one bwrap netns, brings loopback up, starts the service, and probes
// it over 127.0.0.1 — egress stays denied, loopback works. Any miss (no
// bwrap, no orion binary, sandbox error) FALLS BACK to today's bare run with
// a warning: staged isolation, never a broken proof.

// colocRequest is the stdin contract of `orion __empirical-probe`.
type colocRequest struct {
	Bin      string             `json:"bin"`  // service binary path (inside the workdir bind)
	Port     int                `json:"port"` // loopback port to serve+probe
	Rounds   int                `json:"rounds"`
	Contract testsynth.Contract `json:"contract"`
}

// colocProbe attempts the sandboxed co-located run; ok=false → caller falls
// back to the bare path.
func colocProbe(ctx context.Context, binDir, bin string, port, rounds int, c testsynth.Contract) ([]ProbeResult, bool) {
	if os.Getenv("ORION_EMPIRICAL_SANDBOX") == "off" {
		return nil, false
	}
	probeBin := probeBinary()
	if probeBin == "" {
		return nil, false
	}
	be, err := sandbox.New("auto")
	if err != nil || be.Name() == "none" {
		return nil, false // no namespace isolation available → bare fallback
	}
	req, err := json.Marshal(colocRequest{Bin: bin, Port: port, Rounds: rounds, Contract: c})
	if err != nil {
		return nil, false
	}
	// Loader dirs for dynamically-linked binaries (a cgo-built orion or
	// service needs ld.so + libc); RO and only when present.
	roBinds := []string{probeBin}
	for _, d := range []string{"/lib", "/lib64", "/usr/lib"} {
		if st, serr := os.Stat(d); serr == nil && st.IsDir() {
			roBinds = append(roBinds, d)
		}
	}
	res, err := be.Run(ctx, sandbox.Spec{
		Workdir:  binDir,
		Argv:     []string{probeBin, "__empirical-probe"},
		Env:      map[string]string{"PATH": "/usr/bin:/bin"},
		NetAdmin: true, // the verb must be able to bring lo up in the fresh netns
		ROBinds:  roBinds,
		Stdin:    string(req),
		// AllowNet stays false: --unshare-net — the isolated netns holds BOTH
		// the service and the probe; the verb brings lo up inside.
	})
	if err != nil || res.ExitCode != 0 {
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		slog.Warn("empirical: co-located sandbox probe unavailable — falling back to the bare run", "exit", res.ExitCode, "err", detail, "stderr", truncateStr(res.Stderr, 300))
		return nil, false
	}
	var prs []ProbeResult
	if jerr := json.Unmarshal([]byte(res.Stdout), &prs); jerr != nil || len(prs) == 0 {
		slog.Warn("empirical: co-located probe returned no parsable results — falling back", "err", jerr)
		return nil, false
	}
	return prs, true
}

// probeBinary resolves the orion executable that carries the probe verb.
func probeBinary() string {
	if p := strings.TrimSpace(os.Getenv("ORION_EMPIRICAL_PROBE_BIN")); p != "" {
		return p
	}
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if strings.Contains(filepath.Base(self), "orion") {
		return self
	}
	return "" // a `go test` binary has no probe verb — bare fallback
}

// ProbeVerbMain is the body of `orion __empirical-probe` (or-6lm): inside
// the sandbox netns it brings loopback up, starts the service, probes the
// contract for N rounds, and emits the JSON results on stdout.
func ProbeVerbMain(stdin io.Reader, stdout io.Writer) int {
	var req colocRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, "empirical-probe: decode:", err)
		return 1
	}
	if err := bringUpLoopback(); err != nil {
		fmt.Fprintln(os.Stderr, "empirical-probe: loopback:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	svc := exec.CommandContext(ctx, req.Bin) // #nosec G204 -- the PROVEN artifact binary, executed inside the sandbox netns
	svc.Env = []string{"PORT=" + fmt.Sprint(req.Port), "PATH=/usr/bin:/bin"}
	if err := svc.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "empirical-probe: service start:", err)
		return 1
	}
	defer func() {
		if svc.Process != nil {
			_ = svc.Process.Kill()
			_, _ = svc.Process.Wait()
		}
	}()
	addr := fmt.Sprintf("127.0.0.1:%d", req.Port)
	rounds := req.Rounds
	if rounds <= 0 {
		rounds = 1
	}
	prs := make([]ProbeResult, 0, rounds)
	for i := 0; i < rounds; i++ {
		prs = append(prs, probeContract(addr, req.Contract))
	}
	if err := json.NewEncoder(stdout).Encode(prs); err != nil {
		return 1
	}
	return 0
}

// bringUpLoopback sets lo UP inside the (freshly unshared) netns — a new
// netns starts with loopback DOWN, and the userns owner holds CAP_NET_ADMIN
// over it, so a plain ioctl suffices (no ip(8) dependency in the jail).
func bringUpLoopback() error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(fd) }()
	var ifr struct {
		Name  [16]byte
		Flags uint16
		_     [22]byte
	}
	copy(ifr.Name[:], "lo")
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifr))); errno != 0 { //nolint:gosec // fixed-layout ifreq ioctl (lo up inside the netns)
		return fmt.Errorf("SIOCGIFFLAGS: %v", errno)
	}
	if ifr.Flags&syscall.IFF_UP != 0 {
		return nil // already up (host run, or a kernel that pre-ups lo) — setting flags needs CAP_NET_ADMIN we may not have
	}
	ifr.Flags |= syscall.IFF_UP | syscall.IFF_RUNNING
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifr))); errno != 0 { //nolint:gosec // fixed-layout ifreq ioctl (lo up inside the netns)
		return fmt.Errorf("SIOCSIFFLAGS: %v", errno)
	}
	return nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

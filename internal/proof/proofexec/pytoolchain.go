package proofexec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// pyToolchain is Python's proof-execution surface (or-4y7.9): `python3 -m unittest`
// (stdlib — present in EVERY interpreter, the python analog of `go test`) /
// `-m py_compile` / `-m pytest` (when the pinned interpreter carries it) over
// generated code, inside the same net-denied, secret-scrubbed sandbox as the Go
// arm. Two properties differ from Go by design:
//
//   - VERSION-FLEXIBLE (or-4y7.10, per the developer's requirement): the
//     interpreter is never assumed. ORION_PYTHON pins it (a name or path — the
//     direction.runtime resolution feeds this); otherwise python3 from PATH. Its
//     prefix and dynamic-library dirs are resolved from the TRUSTED host (like
//     `go env GOROOT`), so system/brew/pyenv layouts all work unconfigured.
//   - STRICTER fail-closed: Python executes generated code on EVERY invocation,
//     so there is NO unisolated-backend operator override (UnsafeNoneOverride
//     false) — no bwrap, no Python proof, ever.
//
// It is registered here but UNREACHABLE until "python" joins lang.Registered()
// (the capability manifest): ratification still refuses direction.language=python
// until the full adapter set lands (or-4y7.9's final wiring).
type pyToolchain struct{}

func (pyToolchain) Language() string { return "python" }

// pyDeniedModules are `-m` targets that install/fetch or build environments —
// never legitimate under proof (the sandbox denies the network anyway; refusing
// loudly beats a confusing timeout).
var pyDeniedModules = map[string]bool{"pip": true, "ensurepip": true, "venv": true}

func (pyToolchain) Allow(tool string, args []string) error {
	if tool != "python3" {
		return fmt.Errorf("proofexec: tool %q is not on the python verification allowlist (only python3)", tool)
	}
	for i, a := range args {
		if a == "-m" && i+1 < len(args) && pyDeniedModules[args[i+1]] {
			return fmt.Errorf("proofexec: `python3 -m %s` is not allowed under proof (installers/env builders)", args[i+1])
		}
	}
	return nil
}

func (pyToolchain) IsPrimary(tool string) bool { return tool == "python3" }

func (pyToolchain) ResolveBin(_ string) (string, error) {
	rt, err := pyRuntime()
	if err != nil {
		return "", err
	}
	return rt.bin, nil
}

func (pyToolchain) Roots() []string {
	rt, err := pyRuntime()
	if err != nil {
		return nil
	}
	return rt.roots
}

// Links recreates the host's usr-merge symlinks (/lib64→usr/lib64, …) inside the
// jail so the dynamically-linked interpreter's ELF loader resolves (see pyRuntime).
func (pyToolchain) Links() map[string]string {
	rt, err := pyRuntime()
	if err != nil {
		return nil
	}
	return rt.links
}

func (pyToolchain) Env(workdir string) map[string]string {
	env := safeenv.Map() // scrubbed allowlist (no secrets)
	binDir := "/usr/bin"
	if rt, err := pyRuntime(); err == nil {
		binDir = filepath.Dir(rt.bin)
	}
	env["PATH"] = binDir + ":/usr/bin:/bin"
	env["HOME"] = workdir
	env["PYTHONDONTWRITEBYTECODE"] = "1" // no .pyc litter; the workdir is the only writable path anyway
	env["PYTHONNOUSERSITE"] = "1"        // never read ~/.local site-packages (hermetic)
	env["PYTHONHASHSEED"] = "0"          // deterministic hashing across proof runs
	env["PIP_NO_INDEX"] = "1"            // belt+braces: pip is deny-listed AND index-less
	env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
	env["LC_ALL"] = "C.UTF-8"
	delete(env, "PYTHONPATH") // never inherit host module paths into proof
	return env
}

// UnsafeNoneOverride: NO — python executes generated code on every invocation;
// without a namespace sandbox it does not run, and there is no operator escape.
func (pyToolchain) UnsafeNoneOverride() bool { return false }

// pyRT is the resolved python runtime: the real interpreter binary, the read-only
// sandbox roots it needs (prefix trees + dynamic-library dirs + system libdirs),
// and the usr-merge symlinks the loader resolves through.
type pyRT struct {
	bin   string
	roots []string
	links map[string]string
}

var (
	pyOnce sync.Once
	pyVal  pyRT
	pyErr  error
)

// usrMergeTops are the top-level LOADER dirs that are symlinks under usr-merge
// (Debian, Ubuntu, Fedora, …). A dynamically-linked binary's ELF interpreter is
// reached through them (PT_INTERP → /lib64/ld-linux → usr/lib64/ld-linux → the
// real .so), and the intermediate links are RELATIVE — so the sandbox must
// recreate the top-level link and bind the REAL target dir (a path is never both
// bound and symlinked). /bin and /sbin stay invisible: the interpreter is exec'd
// by absolute path, and generated code gets no host binaries to shell out to.
var usrMergeTops = []string{"/lib", "/lib64"}

// pyRuntime resolves the pinned-or-default interpreter ONCE from the trusted
// host: ORION_PYTHON (name or path) else python3 on PATH; then its real binary,
// sys.base_prefix/sys.prefix (stdlib + site-packages), the directories of its
// dynamically linked libraries (ldd), the system libdirs, and the usr-merge
// symlinks — the exact RO set the sandbox needs, independent of layout (system,
// brew, pyenv, venv) so a dynamically-linked interpreter actually loads.
func pyRuntime() (pyRT, error) {
	pyOnce.Do(func() {
		// Resolution order (the or-4y7.10 hook): the developer's explicit
		// ORION_PYTHON pin (name or path) → the user's python3 as their PATH
		// resolves it. Orion uses whatever version the developer has; a missing
		// interpreter is the startup preflight's job (offer to install), never a
		// silent substitution here.
		interp := strings.TrimSpace(os.Getenv("ORION_PYTHON"))
		if interp == "" {
			interp = "python3"
		}
		bin, err := exec.LookPath(interp)
		if err != nil {
			pyErr = fmt.Errorf("proofexec: python interpreter %q not found on host (pin one with ORION_PYTHON): %w", interp, err)
			return
		}
		if resolved, serr := filepath.EvalSymlinks(bin); serr == nil {
			bin = resolved
		}
		// usr-merge links first: add() below rewrites any path under a symlinked
		// top (/lib64/…) to its real location (/usr/lib64/…), so no path is ever
		// both bound and symlinked (bwrap refuses that).
		links := map[string]string{}
		realTop := map[string]string{}
		for _, top := range usrMergeTops {
			fi, lerr := os.Lstat(top)
			if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if tgt, rerr := os.Readlink(top); rerr == nil {
				links[top] = tgt // e.g. "/lib64" → "usr/lib64" (relative, as bwrap wants)
			}
			if r, rerr := filepath.EvalSymlinks(top); rerr == nil {
				realTop[top] = r
			}
		}
		seen := map[string]bool{}
		var roots []string
		add := func(p string) {
			p = filepath.Clean(strings.TrimSpace(p))
			for top, real := range realTop {
				if p == top {
					p = real
				} else if strings.HasPrefix(p, top+"/") {
					p = real + strings.TrimPrefix(p, top)
				}
			}
			if p == "" || p == "/" || p == "." || seen[p] {
				return
			}
			if _, err := os.Stat(p); err != nil {
				return
			}
			seen[p] = true
			roots = append(roots, p)
		}
		// addReal also binds the fully-resolved location, covering RPATH trees
		// reached through directory symlinks (brew's opt/ → Cellar/).
		addReal := func(p string) {
			add(p)
			if r, serr := filepath.EvalSymlinks(p); serr == nil {
				add(r)
			}
		}
		// The interpreter's own tree (bin + lib live under <root>/bin/python3.x).
		addReal(filepath.Dir(filepath.Dir(bin)))
		// Trusted-host prefix query (the python analog of `go env GOROOT`).
		out, err := exec.Command(bin, "-E", "-S", "-c", "import sys;print(sys.base_prefix);print(sys.prefix)").Output() //nolint:gosec // trusted host interpreter resolution, no generated code
		if err != nil {
			pyErr = fmt.Errorf("proofexec: resolving %s prefix: %w", bin, err)
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			addReal(strings.TrimSpace(line))
		}
		// Dynamic-library dirs (python is not static like go): ldd on the trusted
		// host names every lib dir the interpreter loads. Best-effort — a static
		// or musl python simply has none.
		if lout, lerr := exec.Command("ldd", bin).Output(); lerr == nil { //nolint:gosec // trusted host binary inspection
			for _, line := range strings.Split(string(lout), "\n") {
				// "libX.so => /path/libX.so (0x…)" and the loader line
				// "<brew>/lib/ld.so => /lib64/ld-linux….so (0x…)" — take BOTH sides
				// so the interpreter's own libdir and the loader chain are covered.
				for _, half := range strings.SplitN(line, "=>", 2) {
					f := strings.Fields(strings.TrimSpace(half))
					if len(f) > 0 && strings.HasPrefix(f[0], "/") {
						addReal(filepath.Dir(f[0]))
					}
				}
			}
		}
		// Bind each loader top: the symlinked ones land at their real target (add
		// rewrites them; the recreated link reaches them), a real dir binds as-is.
		for _, top := range usrMergeTops {
			add(top)
		}
		pyVal = pyRT{bin: bin, roots: roots, links: links}
	})
	return pyVal, pyErr
}

func init() { registerToolchain(pyToolchain{}) }

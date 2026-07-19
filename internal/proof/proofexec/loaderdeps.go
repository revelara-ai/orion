package proofexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// hostLoaderDeps resolves the sandbox needs of a possibly dynamically-linked
// tool binary: the directories of every shared library its ELF loader will
// open (via trusted-host ldd) and the usr-merge symlinks (/lib64 → usr/lib64,
// …) the loader chain resolves through. A static binary contributes nothing.
// Orion binds whatever tool the user has — a CGO-built golangci-lint or a
// user-compiled linter must load in the jail exactly like a static one
// (or-f96q follow-up; the same lesson the python interpreter taught in
// or-4y7.9: intermediate loader symlinks are RELATIVE, so the top-level link
// must be recreated and the REAL dirs bound — never both on one path).
func hostLoaderDeps(bin string) (roots []string, links map[string]string) {
	loaderDepsMu.Lock()
	defer loaderDepsMu.Unlock()
	if c, ok := loaderDepsCache[bin]; ok {
		return c.roots, c.links
	}
	links = map[string]string{}
	realTop := map[string]string{}
	for _, top := range usrMergeTops {
		fi, err := os.Lstat(top)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if tgt, err := os.Readlink(top); err == nil {
			links[top] = tgt
		}
		if r, err := filepath.EvalSymlinks(top); err == nil {
			realTop[top] = r
		}
	}
	seen := map[string]bool{}
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
	// ldd on the trusted host names every lib dir the binary loads; take both
	// sides of "=>" so the loader chain itself is covered. A static binary
	// (ldd errors or prints "not a dynamic executable") yields nothing.
	if out, err := exec.Command("ldd", bin).Output(); err == nil { //nolint:gosec // trusted host binary inspection, never generated content
		for _, line := range strings.Split(string(out), "\n") {
			for _, half := range strings.SplitN(line, "=>", 2) {
				f := strings.Fields(strings.TrimSpace(half))
				if len(f) > 0 && strings.HasPrefix(f[0], "/") {
					add(filepath.Dir(f[0]))
				}
			}
		}
	}
	if len(roots) == 0 {
		links = nil // static: no dirs to reach, no links needed
	}
	loaderDepsCache[bin] = loaderDeps{roots: roots, links: links}
	return roots, links
}

type loaderDeps struct {
	roots []string
	links map[string]string
}

var (
	loaderDepsMu    sync.Mutex
	loaderDepsCache = map[string]loaderDeps{}
)

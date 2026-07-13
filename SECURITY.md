# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately via
**GitHub → Security → Report a vulnerability** (private security advisories)
on this repository. Do not open public issues for security reports. You should
receive an initial response within 7 days.

## Threat model: executing generated code

Orion generates code with LLMs and then builds, runs, and proves it. That
execution surface is sandboxed, and it is important to be precise about what
the sandbox does and does not guarantee.

**What Orion does:**

- **Generation and proof execution run under bubblewrap** (`internal/sandbox`):
  a scoped working directory, default-deny network egress (`--unshare-net`),
  and no host filesystem visibility beyond explicit read-only binds. A "none"
  fallback backend exists for environments without namespace support — it
  provides **no isolation** and is reported as such.
- **Proof subprocesses never inherit the host environment**
  (`internal/proof/safeenv`): a small allowlist (Go toolchain variables) is
  passed through; secrets and everything else are dropped, `GOFLAGS` is
  forcibly emptied.
- **Credentials are kept out of reach of generated code**: the revelara.ai
  OAuth token lives in a separate `credentials/` directory (0600), is never
  written to the context store, and is not bind-mounted into sandboxes.
- **Destructive actions are gated**: tools that act on the developer's
  environment require per-call approval; a file-backed red button revokes
  autonomy across processes; git pushes/PRs require explicit opt-in
  (`ORION_GIT_PR`).

**Known gaps (tracked, in progress):**

- The **empirical RUN/probe phase** executes the built artifact with network
  isolation weaker than the build sandbox (loopback-only netns is tracked as
  `or-6lm`).
- **Proof-exec sandbox hardening** (stricter tool binding, `GOPROXY` posture
  in hermetic modes) is tracked as `or-tf8`.
- Supply-chain audit of tool binaries and OSV/CVE scanning is tracked as
  `or-ykz.16`.

Treat generated-code execution as you would any CI runner executing untrusted
build steps: run Orion under a dedicated user account, and do not point it at
repositories containing credentials you cannot afford to expose to a build.

## Supported versions

Pre-1.0: only the latest `main` is supported. Fixes land forward; there are no
backported security patches yet.

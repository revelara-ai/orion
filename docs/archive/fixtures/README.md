# Orion Test Fixture: revelara-ai/microservices-demo

Epic 1 (and v1.x development generally) uses [`revelara-ai/microservices-demo`](https://github.com/revelara-ai/microservices-demo) as Orion's test target. It is a fork of [`GoogleCloudPlatform/microservices-demo`](https://github.com/GoogleCloudPlatform/microservices-demo) (the Google Cloud "Online Boutique" sample) and is treated by Orion exactly like a customer's repo.

## Why this repo

- **Real polyglot codebase.** ~10 microservices across Go, Python, C#, Java, Node, plus shell, HTML, and Dockerfiles. Exercises Orion's polyglot scope from day one.
- **Real architecture.** gRPC, Kubernetes manifests, Istio, Helm charts, Kustomize overlays. Realistic surface area for the architectural inferer (SPEC §12.1) and the harness synthesizer (SPEC §12.4).
- **Resettable.** The fork has `upstream` pointed at the Google original, so the `main` branch can be hard-reset to upstream baseline between Orion test runs without touching the upstream repo.
- **Non-trivial reliability surface.** Prior reliability fix-PRs (gRPC timeouts, PDBs, prompt-injection hardening) have been merged into the fork. Each test run finds whatever gaps remain at that moment.

## Hard rule: upstream is read-only

The `upstream` remote (`git@github.com:GoogleCloudPlatform/microservices-demo.git`) is **never** a target for any Orion-driven action.

- Orion's GitHub App MUST NOT be installed on `GoogleCloudPlatform/microservices-demo`.
- Orion's PR delivery MUST NOT target `GoogleCloudPlatform/...`.
- Reset operations MUST only force-push to `origin` (the `revelara-ai` fork), never to `upstream`.
- The `epic1_smoke.sh` script refuses to operate against any owner in `{GoogleCloudPlatform, googlecloudplatform}` and exits code `20` if asked to.

If you ever see Orion attempting to write to upstream, treat it as a `revelara:platform_critical` safety violation per SPEC §14.8.

## Layout

```
src/                # Per-service source trees (Go, Python, C#, Java, Node, ...)
  adservice/        # Java
  cartservice/      # C#
  checkoutservice/  # Go
  currencyservice/  # Node
  emailservice/     # Python
  frontend/         # Go
  loadgenerator/    # Python (Locust)
  paymentservice/   # Node
  productcatalogservice/   # Go
  recommendationservice/   # Python
  shippingservice/         # Go
  shoppingassistantservice/ # Python
helm-chart/         # Helm chart for the whole stack
istio-manifests/    # Istio config
kubernetes-manifests/ # Plain k8s
kustomize/          # Kustomize overlays
protos/             # Shared .proto files
```

Orion's reliability synthesis targets `src/**` only. `helm-chart/`, `istio-manifests/`, `kubernetes-manifests/`, `kustomize/`, and `protos/` are out of scope for the v1 patch synthesizer (SPEC §A pattern set is code-level reliability, not deployment-config).

## Reset runbook

Restore the fork to the upstream baseline before an Orion test run when you want a clean state.

**Pre-conditions:**
1. You have push access to `revelara-ai/microservices-demo` on `origin/main`.
2. `gh auth status` is healthy.
3. No open PR against the fork is authored by anyone other than the Orion GitHub App (so reset doesn't destroy in-flight human work). Check first:
   ```bash
   gh pr list --repo revelara-ai/microservices-demo --state open \
     --json number,title,author --jq '.[] | select(.author.login != "orion[bot]")'
   ```
   If that lists anything, **stop and resolve those PRs before resetting.**

**Reset commands** (run from the local clone of `revelara-ai/microservices-demo`):

```bash
cd ~/go/src/github.com/revelara-ai/microservices-demo

# Verify origin is the fork (not upstream)
git remote get-url origin   # expect: git@github.com:revelara-ai/microservices-demo.git

# Pull upstream baseline
git fetch upstream main

# Reset main to upstream baseline
git checkout main
git reset --hard upstream/main

# Force-push the fork's main, with-lease as a safety net
git push --force-with-lease origin main
```

A scripted reset (`reset-fixture.sh`) is intentionally deferred. The manual runbook keeps the operator in the loop on a destructive operation against a shared fork. A scripted version may be added in Epic 12 (production hardening) once the safety guards have been exercised manually enough times to trust automation.

## How Epic 1 uses the fixture

1. **`E1-1 GitHub App round-trip`** installs Orion's GitHub App on `revelara-ai/microservices-demo`, clones it into an ephemeral workspace, opens a hardcoded PR, and tears down the workspace. Proves the round-trip.
2. **`E1-2 rvl-cli detection`** scans the fork via `rvl-cli/internal/scanner` and emits structured findings. The exact gap count varies based on the current state of the fork; tests assert *that gaps are detected*, not that *exactly N* are detected.
3. **`E1-3..E1-7`** build the rest of the synthesis pipeline against the fork.
4. **`E1-8 orchestration glue`** runs `orion-cli run --repo=https://github.com/revelara-ai/microservices-demo --service=src/<svc>` (one service per run in v1).
5. **`E1-F smoke test`** invokes `test/acceptance/epic1_smoke.sh` against the fork after E1-8 has shipped a real PR. The script asserts SHAPE (PR exists with documented fields) rather than exact CONTENT (counts, paths).

## Acceptance shape (summary; see `test/acceptance/expected_pr_shape.json` for the canonical contract)

- PR is open against `revelara-ai/microservices-demo`.
- PR author is the Orion GitHub App (`orion[bot]` by default; override via `$ORION_APP_NAME`).
- PR has at least 1 commit.
- At least one commit modifies a file under `src/`.
- PR body contains: `Operating envelope`, `Confidence interval`, `Reproduction bundle`, `Polaris control`.

## Conventions

- Each Orion test run writes a PR with a branch named `orion/<run_id_short>-<issue_external_id_sanitized>` per SPEC §4.2.
- Reset between runs as needed (runbook above).
- Do not commit fixture-side code from outside Orion's pipeline; the fork is read-only as a baseline (only Orion's bot writes to it during normal use).
- If you need to update the fork's structure for testing purposes (e.g., add a new service variant), do it as a one-off PR you own, then reset from upstream when done.

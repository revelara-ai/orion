---
title: Seven Factors x Orion - comparing two control planes for agentic systems
type: research
origin: synthesis comparing the Seven Factors methodology (seven-factors repo) with the Orion Manifesto (read 2026-06-20)
created: 2026-06-20
last_updated: 2026-06-20
sources:
  - "The Seven Factors of the Agentic Control Plane" — seven-factors repo (README, introduction, factors I-VII)
  - "The Orion Manifesto: Reliable Software in the Age of Agentic Development" — orion/docs/MANIFESTO.md
  - "Google SRE AI practices — extrapolations for Orion" — orion/docs/research/Google-SRE-AI-practices-2026-06-18.md
---

# Seven Factors x Orion - comparing two control planes for agentic systems

Two documents, written from opposite ends of the agentic software lifecycle, independently converge on the same architectural conviction: an AI agent is a probabilistic, fallible component, and correctness must live in the deterministic machinery built *around* it — never in the model itself.

- **The Seven Factors of the Agentic Control Plane** governs what agents *do* to enterprise systems of record at run-time: a vendor-neutral methodology, in the Twelve-Factor lineage, for the deterministic layer between AI agents and CRMs, payment processors, ERPs, and clinical systems.
- **The Orion Manifesto** governs what agents *build* at build-time: an opinionated, proof-gated control plane for the software development lifecycle that drives a developer's coding agent and proves the result before it ships.

They are not competitors. They are two halves of the same bet, operating on different boundaries. This document grounds that comparison in verbatim source text, integrates Google SRE's independent corroboration, and closes with a concrete mapping of each of the Seven Factors onto the Orion gate that would prove a build satisfies it.

---

## The shared thesis (almost verbatim)

Both documents make the identical core move: **the AI is a probabilistic, fallible component; correctness must live in the deterministic architecture around it, not in the model.**

Orion states the bet in microservices terms:

> "Orion's reliability does not come from the model. Microservice architecture made reliable systems possible on cheap, unreliable commodity hardware — because making the hardware itself reliable was prohibitively expensive. Orion makes the same move one layer up: it produces reliable software on top of commodity, fallible code-generation models."
> — *Orion Manifesto, intro (line 12)*

> "Like microservices on commodity hardware, Orion treats the generation model as a cheap, fallible component and places the reliability in the architecture around it — independent proof, bounded steps, embedded reliability knowledge."
> — *Orion Manifesto, Thesis #10: "Reliability is the harness's job, not the model's." (lines 166–167)*

The Seven Factors states the same conviction as a layering law:

> "The **Seven Factors** is a methodology for building the control plane that sits between AI agents and enterprise systems: the deterministic layer that must be correct regardless of which model, framework, or agent architecture reasons above it. The reasoning layer owns intent. The control plane owns consequences."
> — *Seven Factors, README (line 5)*

> "These are not equal partners. The reasoning layer is probabilistic — it will sometimes misinterpret, retry unnecessarily, or hallucinate a recovery strategy. The control plane must be correct anyway."
> — *Seven Factors, introduction (line 9)*

Both center on a control plane for agents — Orion is one ("a control plane for agents, not an LLM client"); the Seven Factors is the methodology for building one ("a methodology for building the control plane that sits between AI agents and enterprise systems") — and both decouple a probabilistic reasoning layer from a deterministic execution/proof layer, treating the agent as adversarial or untrustworthy by default. Orion is, in its own one-line framing,

> "a control plane for agents, not an LLM client: it spawns and drives the developer's own coding agent (over the Agent Client Protocol) and gates everything it produces with independent, multi-modal proof — done means proven, never merely asserted."
> — *Orion Manifesto, "In one line" (line 16)*

The Seven Factors describes its subject the same way — "a methodology for building a deterministic control plane beneath these agents: the layer that must be correct regardless of which LLM, framework, or agent architecture reasons above it" (*introduction, line 5*).

### Independent third-party corroboration (Google SRE)

This is not a two-document echo chamber. Google SRE, describing how it is engineering reliable operations in the agentic era, independently arrives at the same load-bearing principle: decouple the probabilistic reasoner from the deterministic executor, and prove the output rather than asserting it. The Orion research note that captures the two Google SRE publications records the most pointed convergence verbatim:

> "By decoupling the AI's reasoning engine (AI Operator) from the execution engine (Actuation Agent), we ensure that no matter how rapidly AI models evolve, their ability to mutate production remains strictly governed by deterministic, human-controlled safety boundaries."
> — *Google SRE, quoted in Google-SRE-AI-practices-2026-06-18.md (line 24)*

That sentence is, structurally, both theses at once: it is the Seven Factors' "the reasoning layer owns intent; the control plane owns consequences," and it is Orion's "reliability is the harness's job, not the model's." Google SRE further insists the safety boundary is caller-agnostic — "underlying tools must be incapable of single-handedly taking down production, regardless of who or what is calling them … It does not care if the caller is an agent or a human" (*line 23*) — which is precisely the Seven Factors' position that governance is set "by the systems the operation touches — not by who or what initiates it" (*Factor I, line 7*). As Orion's manifesto puts it: "When the team operating planet-scale production reaches the same conclusions independently, the thesis is not speculative." (*Orion Manifesto, External Corroboration, line 242*)

### The difference is which boundary each governs

| Dimension | Seven Factors | Orion |
|---|---|---|
| Governs | What agents **do** to systems of record | What agents **build** (software) |
| Lifecycle phase | Run-time (production operations) | Build-time (the SDLC loop) |
| The agent is a… | operator/caller of enterprise systems | code generator |
| Artifact | a vendor-neutral methodology (Twelve-Factor style) | a product/architecture (an agentic chat + orchestrator) |
| Boundary shape | a single tool invocation at the system edge | a long, multi-step development loop |
| Central obsession | governing the boundary (authz, mutations, idempotency, audit) | proving the result ("done means proven, never asserted") |
| The adversary | the agent's unreliability + external prompt injection | the agent gaming its own verifier |
| Lineage cited | Wiggins' Twelve-Factor; positions against HumanLayer / Google agent patterns | Claude Code / Pi / Hermes; corroborated by Google SRE |

The Seven Factors is explicit that it occupies the *opposite* side of the boundary from agent frameworks: "Existing frameworks — HumanLayer's 12-Factor Agents, Google's agent design patterns — address the agent side of the boundary… We address the other side: the enterprise infrastructure that must be correct regardless of what sits above it." (*introduction, line 15*). Orion is explicit that it occupies the loop, not the model: it "is not an LLM client. It holds no API key and makes no inference calls; it spawns the developer's coding agent (which uses that agent's own auth — e.g. a Claude Max/Pro login) and drives it over the Agent Client Protocol." (*Manifesto, Part V, line 223*)

---

## Concept-by-concept mapping (Orion mechanism ↔ Seven Factor)

The two control planes solve structurally similar problems with structurally similar moves. Where Orion names a build-time mechanism (Part IV), the Seven Factors usually has a run-time counterpart.

| Concept | Orion | Seven Factors |
|---|---|---|
| Intent as a tamper-proof contract | Intent completeness gate → "a formal, executable specification that the generating agent cannot modify" (*Part IV, line 176*). Orion's gate fixes a *build-time* spec the agent cannot edit. | The Seven Factors make *run-time* intent the contract at the tool boundary — Factor III: an intent interface "names what the caller is trying to accomplish, accepts only identifiers the caller should possess" (*line 9*); Factor II: "the reasoning layer emits structured intent — a JSON command naming the operation and its business-level parameters" (*line 9*). Different lifecycle phase, same move: intent is explicit, structured, and beyond the agent's reach. |
| AI must not own irreversible state | Side-effect sandboxing + reversibility gates: destructive operations "execute in a sandbox and require a defined rollback path before they run" (*Part IV, line 200*); reasoning decoupled from execution | Factor II: "Every create, update, and delete against a system of record passes through the control plane" (*line 9*); "The LLM never sees a connection string, never constructs a query, never handles a raw API response." (*line 9*) |
| Irreversible-action incident class | "agents wiping production databases, running `terraform destroy` against years of production state" (*Part I, line 45*) | Factor II cites the same era of incident: "In July 2025, an AI coding agent deleted an entire production database during an explicit code freeze, then fabricated four thousand fake records to cover the gap." (*line 5*); Factor V: compensation "defined before the operation begins" (*line 11*) |
| Don't let the agent confabulate forward | Per-step confidence + circuit breaker: "Orion escalates to a human rather than compounding the error." (*Part IV, line 207*) | Factor VI: a typed recovery contract so "the reasoning layer never guesses at state" — otherwise "it fills the information vacuum with imagination — confabulating a recovery strategy" (*lines 3, 5*) |
| Reconstructable, not archaeological | Built-in reliability primitives (structured logs, traces, runbooks); "traces are the source of truth for what the system actually does" (*Part I, line 80*); the 3 a.m. test | Factor VII: an append-only, hash-linked audit record; "When it is absent, incident response becomes archaeology. The agent did its job. Nobody can prove it." (*line 13*) |
| Rigor calibrated to consequence | Goal #4: "Hold the highest reliability bar the project warrants" — "A throwaway tool and a payments pipeline do not get the same controls." (*lines 115–116*); the same calibration belief is stated as Thesis #7, "Reliability is calibrated to the project, not maximized blindly." (*line 157*) | Factor I: governance "depth is calibrated to what it protects" along "the consequence spectrum" (*lines 7, 9*) |
| Least privilege / blast radius | Least-privilege agent identity; dependency provenance; side-effect sandboxing flagging "Non-reversible actions with real blast radius" (*Part IV, line 201*) | Factor IV: structural namespace separation; "The blast radius of a compromised agent is exactly the tools in its namespace." (*line 9*) |
| No self-certification | "No agent grades its own homework." — validation "withheld from the generating subagents" (*Thesis #1, line 140; Part IV, line 182*). Correctness is enforced deterministically by an independent verifier. | The Seven Factors has no direct counterpart to Orion's "no agent grades its own homework" — there is no independent-verifier analog, because the control plane enforces correctness structurally rather than by re-checking the agent's work. The nearest relative is that architectural determinism: correctness is a property of the boundary, not a self-report. That asymmetry is itself worth stating. |

---

## Similarities (distilled)

1. **Same root diagnosis.** Both trace every failure to one structure: a system optimizing for a local signal while the true intent drifts or goes unmeasured — and a probabilistic component filling information vacuums with confident invention. Orion: "the development loop optimizes for a **local signal** — a passing test, a green CI, an agent's own confidence — while the **true goal** drifts, decays, or goes unmeasured." (*Part I, line 92*) Seven Factors (Factor VI): the reasoning layer "fills the information vacuum with imagination — confabulating a recovery strategy, retrying with modified parameters, or inventing a result." (*line 5*)
2. **Architecture over model.** Neither waits for a better model; both bet the model stays a fallible commodity. Orion: "It is the opposite bet: that the generation model will remain a fallible commodity, and that durable reliability must live in the loop that governs it — not in the component it governs." (*Part V, line 229*) Seven Factors (Factor IV): "This is not a deficiency to be solved by better models." (*line 7*)
3. **Intent is first-class and contractual.** Make it explicit, structured, and beyond the agent's reach. Orion: "Intent must be complete before code is written." (*Thesis #4, line 148*) Seven Factors: tool boundaries "follow intent, not implementation" (*Factor III principle*).
4. **Adversarial / zero-trust toward the AI.** The agent is assumed wrong, gameable, or hijackable. Orion: "Orion assumes its own agents will game the verifier." (*Thesis #2, line 142*) Seven Factors (Factor IV): "The model that follows the access control instruction is the same model that follows the injected instruction." (*line 7*)
5. **Calibrate to risk, don't maximize blindly.** Same dial — even the same payments-vs-toy framing. Orion: "Reliability is calibrated to the project, not maximized blindly." (*Thesis #7, line 157*) Seven Factors (Factor I): "its depth is calibrated to what it protects" (*line 7*).
6. **Reject reconstruction-by-investigation.** Understanding/auditability must be produced at execution time, structurally — not reconstructed from whatever a developer happened to log. Orion: "Understanding is a first-class output." (*Thesis #5, line 151*) Seven Factors (Factor VII): "Every agent action is reconstructable by architecture, not investigation." (*principle*)

---

## Differences (distilled)

1. **Layer (the big one).** Seven Factors = run-time governance of agents acting on enterprise systems. Orion = build-time governance of the loop producing software. One controls consequences in production; the other proves correctness before production. The Seven Factors' own framing is run-time and edge-shaped — "every create, update, and delete against a system of record passes through the control plane" (*Factor II, line 9*). Orion's is loop-shaped — it "does not run after the development loop; it governs the loop itself." (*Part V, line 219*)
2. **Proof vs. invariants.** Orion's heart is *multi-modal proof of correctness*: "Correctness is established only when independent lines of evidence converge: behavioral verification … empirical verification … and hazard verification … Convergence is the proof; any single green light is not." (*Thesis #3, line 146*) The Seven Factors has no proof machinery — its heart is enforcing *runtime invariants* at the boundary (authorization, transaction integrity, idempotency, audit), which Orion specifies only lightly.
3. **Single boundary vs. long horizon.** The Seven Factors treats each tool call as a discrete crossing, so it needs idempotency, compensation, and recovery contracts *per operation*. Orion fights the multiplicative-error math of a long chain — "At 95% per-step accuracy, a 10-step workflow succeeds only 60% of the time." (*Part I, line 27*) — so it needs context/memory engineering, drift detection, re-anchoring, bounded steps, and iteration budgets. Those concepts are absent from the Seven Factors because a single runtime invocation has no "trajectory" to drift.
4. **Threat model.** The Seven Factors weights *external prompt injection* heavily — tool descriptions and error channels as attack vectors: "The TIP exploitation paper achieved remote code execution on every tested agent-LLM pair through crafted tool descriptions and return values." (*Factor IV, line 7*) Orion weights *internal verifier-gaming* and the recursive trap that the orchestrator is itself an agentic workflow: "an orchestrator of subagents is itself an agentic workflow, and it inherits every failure mode above." (*Part I, line 94*)
5. **Static standard vs. learning system.** The Seven Factors is a (living but) static methodology — "a shared methodology for getting it right." (*README, line 9*) Orion is a learning loop: "every encountered failure mode enriches the shared knowledge that guards the next change, the next project, and every other developer on the platform." (*Thesis #9, line 164*)
6. **Spec vs. product.** The Seven Factors is vendor-neutral principles to quote and argue about — "Each is designed to be quoted, posted, and argued about." (*README, line 31*) Orion is a concrete system with named mechanisms, the Agent Client Protocol, and a deployment bar.

---

## The synthesis: they stack, they don't compete

The cleanest framing: **Orion is the build-time reliability layer; the Seven Factors are the run-time governance layer — and they compose.**

- When an Orion run is building an enterprise agent integration, the Seven Factors are exactly the reliability profile / hazard checklist that run should hold itself to. Orion's hazard verification ("STPA-derived analysis of the unsafe control actions a change could enable, with proof that each is controlled," *Part IV, line 188*), its "highest reliability bar the project warrants" (*Goal #4, line 115*), and its mandate to "Bring DevOps and SRE expertise to every change" (*Goal #7, line 124*) are the natural ingestion points. The Seven Factors become the *spec* of what a governed agentic integration must satisfy, and Orion's multi-modal proof becomes the *evidence* that the built control plane satisfies them.
- Conversely, the Seven Factors say nothing about how to *reliably build* a control plane — that gap is precisely Orion's job. The Seven Factors describe what a healthy control plane looks like; they do not describe the loop that produces one correctly.
- Orion reaches explicitly toward Polaris (Goal #8): it "plugs into the Polaris reliability platform: it reasons with Polaris's controls catalog, knowledge base, and risk register on every task." (*Goal #8, line 128*) The Seven Factors do not name Polaris, but are essentially a named controls taxonomy for the agentic-control-plane domain — the kind of artifact a controls catalog like Polaris's would hold. They could compose through Polaris as a shared substrate.

**In one sentence:** the Seven Factors tell you *what "governed" means* for an agent in production; Orion tells you *how to prove you built it that way* — and the cleanest fit is to feed the Factors into Orion as the contract its proof gates measure against.

The one spot where they genuinely do *not* map is also the most revealing. Orion's entire context/memory/drift apparatus — "a tiered, heat-managed memory (short-, mid-, and long-term) that bounds context, pins the original intent so it can never be evicted, summarizes rather than drops, and re-grounds before decay turns into divergence" (*Thesis #6, line 155*) — has no Seven-Factors analog. And the Seven Factors' regulatory-audit depth — "PCI-DSS 10.2… HIPAA 164.312(b)… The EU AI Act Article 12… SOX Section 802… FINRA's 2026 oversight report" (*Factor VII, line 11*) — has no Orion analog. A long-horizon builder and a single-crossing operator genuinely have different failure surfaces.

---

## Seven Factors as an Orion reliability profile

If the Seven Factors are the *contract* a governed agentic integration must satisfy, Orion is the loop that proves it. The table below maps each Factor I–VII to the concrete Orion gate(s) / hazard check(s) from Part IV that would enforce or prove a build holds it — turning the methodology into an executable reliability profile that an Orion run can measure against. Google SRE's parallel practice is noted where it independently reinforces the same gate.

| Factor | Principle | Orion gate(s) that enforce / prove it | Independent SRE echo |
|---|---|---|---|
| **I. Governed Operations** | *No enterprise concern delegates to an agentic protocol* | **The deployment bar and earned autonomy** + **Built-in reliability primitives** — Orion calibrates rigor to the project's "data sensitivity, concurrency exposure, blast radius, reversibility, regulated domain" (*Goal #4, line 116*) and applies the DevOps/SRE lens to every change, so authz/compliance depth is a deliverable, not an afterthought. Hazard verification proves the governance the protocol omitted is actually present. | "Zero-Trust, Safe-by-Default Actuation … regardless of who or what is calling them" (*SRE, line 23*) |
| **II. Deterministic Mutations** | *All state mutations belong to the control plane* | **Side-effect sandboxing and reversibility gates** — destructive writes "execute in a sandbox and require a defined rollback path before they run" (*Part IV, line 200*); the reasoning engine is decoupled from the execution engine, so the generated agent never owns the mutation path directly. Empirical proof ("shell-level probes … real I/O," *line 187*) confirms mutations went through the control plane, not around it. | "decoupling the AI's reasoning engine … from the execution engine" + mandatory `dry_run=true` contract (*SRE, lines 24, 33*) |
| **III. Intent-Based Communication** | *Tool boundaries follow intent, not implementation* | **Intent completeness gate** — produces "a formal, executable specification that the generating agent cannot modify" (*Part IV, line 176*), the ground truth Orion measures against. Behavioral verification (tests "written test-first against the spec," *line 186*) proves the built tool boundary expresses the intended business outcome rather than leaking `stripe_create_charge`-style implementation. | Oversight "shift[s] left … reviewing Designs, Intent, and Policies … before code generation" (*SRE, line 21*) |
| **IV. Bounded Access** | *Each caller sees only the capabilities its role requires* | **Side-effect sandboxing** (least-privilege agent identity, blast-radius flagging) + the **adversarial, independent validation** posture that "Orion assumes its own agents will game the verifier" (*Thesis #2, line 142*). Hazard verification (STPA) is the check that a compromised caller cannot reach capabilities outside its namespace — the architectural litmus test of Factor IV. | "No Ambient Access & Least Privilege … Agent identities must be distinct from human users, strongly authenticated, and granted access only on-demand" (*SRE, line 25*) |
| **V. Safe Retries** | *Every mutation is safely retried by a probabilistic caller* | **Per-step confidence and circuit breaker** + **iteration budget and degradation detection** — Orion bounds refinement loops and "escalates to a human rather than compounding the error" (*Part IV, line 207*), the build-time analog of deduplication-and-compensation. Behavioral + empirical proof would assert that a retried mutation produces exactly one consequence. | "Agentic Circuit Breakers … Any action performed by an agent must be highly interruptible" (*SRE, line 26*); "Red Button" pause-all (*line 41*) |
| **VI. Recovery Contracts** | *The reasoning layer never guesses at state* | **Per-step confidence and circuit breaker** — instead of confabulating forward on an ambiguous failure, the calibrated confidence signal trips and the loop escalates. **Drift detection and re-anchoring** keeps the loop on the deterministic path rather than inventing a recovery strategy — the build-time counterpart to a typed recovery envelope. | Real-time risk evaluation that can "auto-downgrade L3→L2 and route to a human when risk is elevated" (*SRE, line 47*) |
| **VII. Structural Observability** | *Every agent action is reconstructable by architecture, not investigation* | **Built-in reliability primitives** — "a component is not complete until it carries … structured logs, trace-context propagation, metrics … and a runbook. Orion validates these as artifacts." (*Part IV, line 192*) This is the gate that proves the built system emits the audit surface Factor VII demands at run-time, satisfying the 3 a.m. test rather than leaving incident response to archaeology. | "every autonomous agent action must be attributed to a unique agent principal with a complete, immutable record" (*SRE, line 25*) |

The mapping is asymmetric by design. Factors I–IV land on Orion's *gating and access* mechanisms (intent gate, sandboxing, deployment bar); Factors V–VII land on Orion's *resilience and operability* mechanisms (circuit breaker, drift detection, reliability primitives). The convergence with Google SRE in the rightmost column is the tell: three independent teams — a Twelve-Factor-style methodology, a proof-gated SDLC product, and the operators of planet-scale production — describe the same control plane from three different vantage points. When that happens, the thesis is not speculative.

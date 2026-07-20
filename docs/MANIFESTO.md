# The Orion Manifesto
## Reliable Software in the Age of Agentic Development

---

Agentic coding tools have arrived. They write code faster than any human can review it. They fill backlogs, unblock PRs, and ship features overnight. And they are quietly degrading the reliability of the software they produce.

This is not a condemnation of AI. It is an observation about architecture. The development loop — intend, generate, test, review, merge, deploy, operate — was designed for humans working at human speed and human scale. Agents break its load-bearing assumptions in ways that are silent, compounding, and consequential.

Orion is a new loop. It is an opinionated, agentic driver of the software development lifecycle whose single obsession is reliability. It takes a developer's intent — a one-line idea, a design document, or a mature backlog of issues; the developer chooses the starting point — and drives it to software that is **proven correct, operable at 3 a.m., and ready to deploy.** Where the intent is incomplete, Orion does not guess; it brings the human in to make the spec unambiguous before a line of code is written. Where the intent is clear, Orion delivers to it at the highest reliability standard the project warrants.

And Orion's reliability does not come from the model. Microservice architecture made reliable systems possible on cheap, unreliable commodity hardware — because making the hardware itself reliable was prohibitively expensive. Orion makes the same move one layer up: it produces reliable software on top of commodity, fallible code-generation models. We start on frontier models because they are the best available today, but the guarantees come from the loop, not the model.

Orion is the reliability layer of the agentic software development lifecycle.

**In one line:** *Orion is an agentic coding chat — in the lineage of Claude Code, Pi, and Hermes — driven by an opinionated, proof-gated workflow that turns a developer's intent into proven, operable software. It is a control plane for agents, not an LLM client: it spawns and drives the developer's own coding agent (over the Agent Client Protocol) and gates everything it produces with independent, multi-modal proof — done means proven, never merely asserted.*

---

## Part I: The Failure Modes

The research is unambiguous. Agentic development loops fail in three distinct but related ways: the workflow itself misbehaves, the code it produces carries defects humans wouldn't write, and the running system cannot be understood or operated when it breaks.

### The Workflow Fails

**1. Errors compound multiplicatively.**
Each step in an agent loop has a per-step success probability and they multiply. At 95% per-step accuracy, a 10-step workflow succeeds only 60% of the time. An agent can call a tool with subtly wrong parameters, receive a plausible result, and continue building on the error — each subsequent step deepening the flaw.

**2. Agents drift from the original intent.**
In long sessions, earlier context is compressed or dropped. The agent forgets decisions made earlier in the run and makes new ones on stale assumptions. Worse: agents can remember every decision and still drift, because each local choice reasonably extends the previous one while the cumulative path diverges from the spec. Facts stay correct; trajectory goes wrong.

**3. Agents game their own verifiers.**
When the oversight surface collapses to an automated test suite, agents optimize for passing tests rather than solving the problem. Concrete observed behaviors: hardcoding expected outputs, reading test fixtures directly, editing test files to auto-pass, producing solutions that satisfy visible tests while failing on held-out ones. The gap between visible and held-out pass rates grows by 28 percentage points for every tenfold increase in code size.

**4. Iterative refinement degrades security.**
"Keep prompting until it works" actively makes the artifact worse. Controlled research found a 37.6% increase in critical vulnerabilities after five iterations, with early security improvements offset by subtler vulnerabilities in later passes. The refinement loop is not monotonically improving.

**5. Batch size inflates until review collapses.**
AI makes it easy to write more code; more code means larger changesets; larger changesets introduce instability. The 2025 DORA report found roughly a 7.2% reduction in delivery stability for every 25% increase in AI adoption. The mechanism is not the model — it is that delivery systems haven't evolved to safely manage AI-accelerated volume. AI is an amplifier: it magnifies existing strengths and existing weaknesses alike.

**6. Humans stop reviewing.**
Developers using AI assistants submit more insecure code than those coding without — and express greater confidence in their submissions. The tool suppresses the scrutiny it most needs.

**7. Side effects don't reverse on cancellation.**
Tool calls that write to databases, send API requests, or modify object storage are not automatically reversed when a workflow fails or is cancelled. Agentic autonomy without reversibility guarantees creates a class of incident unique to this era: a wrong agent action that executes faster than a human can intervene. Documented incidents include agents wiping production databases, running `terraform destroy` against years of production state, and destroying project data in loops.

**8. Memory can be poisoned.**
Injected instructions can persist across sessions, propagating intent corruption autonomously and surviving restarts.

**9. The harness itself degrades under its own dependency.**
An agentic loop is a resilience wrapper around a stochastic, rate-limited, frequently-degraded remote dependency: the model API. Its reliability is dominated not by the model's answers but by how the loop behaves when the model misbehaves. Naive retry logic is the classic trap — a change that makes an exhaustion error look retriable, retried at every layer of the stack, turns one failed request into dozens and overwhelms the provider (or the harness itself). Provider-wide degradations are routine, not rare, and a harness sits directly on the API with no bulkhead. These are control-*systems* failures — retry feedback, missing concurrency limits, overload propagation — not "the model was wrong."

---

### The Code Fails

**1. The vulnerability rate is high and stable across model generations.**
45% of AI-generated code samples introduce OWASP Top 10 vulnerabilities. This rate has not improved significantly from GPT-4 through current generations. AI-authored PRs carry 2.74x more vulnerabilities than human-authored ones. Java tops out at 72% failure rate; XSS fails 86% of the time. Bigger, newer models do not fix this.

**2. Subtle correctness failures pass review.**
LLM-generated code lacks defensive programming constructs and contains subtly incorrect implementations of security-critical algorithms — the kind that pass code review, pass tests, and surface in production.

**3. Secrets leak at higher rates than human-written code.**
AI-generated repositories show a 6.4% secret leakage rate. The code frequently generates hardcoded API keys and plaintext credentials. 82% of exposed secrets remain active even after detection.

**4. Agents hallucinate package names at scale.**
20% of recommended packages in a 576,000-sample study didn't exist. 58% of hallucinated names recur across runs, making them repeatable artifacts attackers can pre-register. This is slopsquatting — the AI-hallucination variant of supply chain compromise.

**5. Template-level flaws propagate fleet-wide.**
When a generation pattern is wrong, every downstream app inherits the flaw. CVE-2025-48757: a Lovable-generated Supabase schema template omitted Row Level Security, leaving every authenticated user able to read or modify any other user's data — across more than 170 production applications. One bad pattern, fleet-wide blast radius.

**6. Tests pass without validating behavior.**
AI writes tests by reading the implementation and asserting what the code does, not what it should do. `expect(result).toBeDefined()` passes for any return value. High coverage with low mutation scores means the green build is lying. Once developers learn some failures are noise, they rerun instead of investigate — and over time teams ship despite red builds.

---

### The System Fails at Runtime

**1. Agent-generated code doesn't instrument itself.**
Agents emit functional code but rarely structured logs, distributed traces, metrics, or correlation IDs. You inherit a running system with no telemetry surface, and instrumenting after the fact is exactly the reactive mode you were trying to avoid.

**2. Code is no longer a reliable description of behavior.**
For agentic systems, traces are the source of truth for what the system actually does, as opposed to what the code says it should do. When a human didn't write the logic and the logic is partly non-deterministic, reading the source tells you less than it used to.

**3. Scaling assumptions are never stated.**
Localhost is one user, no concurrency, no malicious input. If no one explicitly specifies thread-safety, the model produces the minimal version that passes the demo — and crashes under concurrent load. Tightly coupled components, circular dependencies, missing database indexing: these aren't bugs to patch, they're structural and surface only at a traffic threshold.

**4. Nobody owns the code at 3 a.m.**
AI-generated systems carry zero institutional knowledge capture. When an incident hits, there is no author to page and no coherent design to reason about. 67% of developers now spend more time debugging AI-generated code than before they adopted AI tools. The 45 minutes spent switching between dashboards, console, logs, and a stale runbook — before a misconfiguration fixable in three minutes once identified — is what this failure mode costs in production.

---

### The Through-Line

These failure modes share a single structure. They all arise because the development loop optimizes for a **local signal** — a passing test, a green CI, an agent's own confidence — while the **true goal** drifts, decays, or goes unmeasured. The signal and the intent decouple. The verifier gets gamed by the loop, and the artifact is code that passes the verifier while being wrong.

And there is a recursive trap that any honest orchestrator must confront: **an orchestrator of subagents is itself an agentic workflow, and it inherits every failure mode above.** The agents building the scaffold will game the scaffold. An orchestrator that trusts its own subagents' green checkmarks has merely rebuilt "the green build is lying to you" one level higher. Orion's design begins from the assumption that it must defend against its own components.

Making the orchestrator the *one non-agentic component* — deterministic, holding no verdict authority its subagents can bend — removes the *gaming* risk. It does not remove the *correctness* risk. A deterministic state machine can still harbor a systemic defect — a wrong transition, a lease race, a queue deadlock — that no proof of the finished *product* can see, because each product was proven correct in isolation. So the orchestrator must not merely be *asserted* trustworthy; its own control-plane invariants must be *proven*, with the same class of tool Orion uses to prove everything else. The prover is itself proven.

The cost of all of this is not paid at write time. It is paid at comprehension time — and the comprehension bill comes due during an incident. Every failure mode above is some version of *the system now runs faster than anyone understands it.*

---

## Part II: The Mission and the Goals

**The mission:** make agent-produced software at least as reliable as the best human-engineered software — and ultimately more so — by governing the loop that produces it and proving the result before it ships.

Orion commits to the following goals. Everything in Parts III and IV exists to serve them.

**1. Deliver to the developer's intent.**
The unit of success is *the developer's actual intent*, not the literal prompt and not the agent's interpretation of it. Orion's job is to close the gap between what was meant and what was built.

**2. Make intent complete before building.**
When intent is ambiguous or underspecified, Orion does not guess and does not silently fill the gap. It drives an interactive elicitation with the human until the spec is complete enough to implement without ambiguity — and it captures that spec as the executable contract the rest of the loop is measured against.

**3. Prove correctness; never assert it.**
The development loop is complete only when functional correctness has been **independently proven** by non-agentic means. "The agent says it's done" and "the tests are green" are inputs, not verdicts. Proof is the right to ship.

**4. Hold the highest reliability bar the project warrants.**
Orion calibrates rigor to the project's real risk — data sensitivity, concurrency exposure, blast radius, reversibility, regulated domain. A throwaway tool and a payments pipeline do not get the same controls. Over-engineering toy projects and under-protecting critical ones are both failures.

**5. Produce operable software — the 3 a.m. test.**
Software is not done when it works; it is done when a developer paged at 3 a.m. has everything they need to understand, diagnose, and operate it. Instrumentation, runbooks, and operational documentation are deliverables, not afterthoughts.

**6. Master context and memory degradation.**
Context and memory are scarce, decaying resources. Orion treats their management as a core engineering discipline — keeping the right facts present, the original intent anchored, and drift detected and corrected before it compounds.

**7. Bring DevOps and SRE expertise to every change.**
Orion carries the domain knowledge of reliable operations — observability, scaling, failure handling, capacity, incident readiness — and applies it to every change, so reliability primitives are built in rather than bolted on.

**8. Learn continuously, and never repeat a known failure.**
Orion plugs into the Polaris reliability platform: it reasons with Polaris's controls catalog, knowledge base, and risk register on every task, and it feeds the failure modes it encounters back so the whole platform gets smarter. Every run should make the next run — and every other developer's run — better.

**9. Earn autonomous delivery through proof.**
The destination is software that ships without a human in the critical path. Orion gets there not by removing the human and hoping, but by raising the proof bar until autonomy is *earned*. When the bar is met, Orion delivers. When it cannot be met, Orion falls back to a proven, human-mergeable change and asks for the human's judgment.

---

## Part III: The Orion Thesis

From the failure modes and the goals, nine beliefs follow. They are not aspirations; they are the design constraints Orion is built to satisfy.

**1. Passing tests are not evidence of correctness.**
The test suite is an adversarial surface. Agents optimize for it. Orion's validation is independent of the generating agents and hidden from them. No agent grades its own homework.

**2. Orion assumes its own agents will game the verifier.**
This is not a warning about future risk. It is current, documented behavior. The adversarial assumption is structural, not optional — Orion is designed to remain correct *despite* the agents inside it.

**3. Correctness must be proven, and proof is multi-modal — and multi-phase.**
No single signal is trusted. Correctness is established only when independent lines of evidence converge: behavioral verification (tests that assert intended behavior, scored by their ability to catch faults), empirical verification (direct observation of the running system — does the port open, the file exist, the hash match, the request actually succeed), and hazard verification (the unsafe control actions have been identified and shown to be controlled). Convergence is the proof; any single green light is not.

Proof is also two-phased. The triad above proves the *product* — that the code does what the design meant. It cannot prove the *design itself* is coherent: that it has no race, no deadlock, no reachable unsafe state under concurrency, ordering, and failure. That is a separate question with a separate tool — a model checker — and it is cheapest to answer *before* any code is generated. The complete claim is two proofs, not one: the design is proven sound, then the product is proven to implement it.

**4. Intent must be complete before code is written.**
Natural-language specifications are lossy, and the loss accrues across every step of the loop. Ambiguity resolved up front costs a conversation; ambiguity discovered downstream costs a rebuild — or an incident. Orion makes intent concrete and contractual before execution begins.

Completeness and soundness are different properties, and the loop owes both. Completeness is the absence of *ambiguity* — every decision the spec left open has been made. Soundness is the absence of a *reachable unsafe state* — no ordering, race, or failure interleaving the design permits violates a safety or liveness invariant. Elicitation delivers the first. Where the design's shape warrants it — concurrency, ordering, shared state, a protocol — design-time formal verification delivers the second, before a line is written.

**5. Understanding is a first-class output.**
Code that works but cannot be operated is incomplete. The real cost of agentic development is comprehension at 3 a.m. Orion produces the telemetry, the runbooks, and the executable intent that make a system understandable to someone who did not build it.

**6. Context and memory are engineered, not free.**
Context erosion, drift, forgetting, and poisoning are all consequences of treating the context window as infinite and memory as trustworthy. Orion engineers what each agent sees and remembers — a tiered, heat-managed memory (short-, mid-, and long-term) that bounds context, pins the original intent so it can never be evicted, summarizes rather than drops, and re-grounds before decay turns into divergence. That same long-term memory lets the harness *evolve itself*: validated patterns, procedures, and refinements accumulate and make every future run better — without changing a line of code.

**7. Reliability is calibrated to the project, not maximized blindly.**
Rigor is a function of risk. Orion accepts a reliability profile as input and tunes its controls to the actual stakes. The right amount of reliability is a decision, not a default.

**8. Small, bounded steps with re-grounding.**
The compounding math is unforgiving. Orion keeps chains short, checkpoints frequently, re-anchors to the original intent, and detects drift before it accumulates. Batch size is a reliability lever, not just a velocity one.

**9. Reliability knowledge compounds across runs.**
A failure understood once should never recur silently. Orion is a learning system: every encountered failure mode enriches the shared knowledge that guards the next change, the next project, and every other developer on the platform.

**10. Reliability is the harness's job, not the model's.**
The vulnerability rate of generated code has held roughly flat across model generations; bigger and newer models have not made the output reliable. Orion therefore does not wait for a better model. Like microservices on commodity hardware, Orion treats the generation model as a cheap, fallible component and places the reliability in the architecture around it — independent proof, bounded steps, embedded reliability knowledge. We use frontier models today because they are the best components available, but nothing in Orion depends on a particular model. As models commoditize, this is the layer that turns inexpensive, fallible generation into dependable software — and the layer whose value grows as the components get cheaper.

---

## Part IV: How Orion Works

Orion is an orchestrator of specialized subagents that drives the development, testing, validation, and delivery of agent-produced software. It does not replace the generating agent. It governs the loop around it. Each mechanism below maps to a goal and a failure mode it is built to defeat.

**Intent completeness gate.**
Every task begins by establishing whether the intent is complete enough to implement without ambiguity. If it is not, Orion drives an interactive elicitation with the human — surfacing the specific decisions that are underspecified — until it is. The result is a formal, executable specification that the generating agent cannot modify. This is the ground truth Orion measures everything against. *(Counters: semantic-ambiguity accumulation, intent drift.)*

**Context and memory engineering.**
Orion actively manages what each agent sees and retains: bounding context to what the step needs, persisting a durable decision log, anchoring the original intent across long runs, and treating injected or stale memory as a threat. Re-anchoring is periodic and automatic. *(Counters: factual drift, alignment drift, memory poisoning.)*

**Adversarial, independent validation.**
Orion owns the validation suite and withholds it from the generating subagents. Validation agents exist to find failure, not to confirm success. Test quality is measured by mutation score — the ability of a test to catch an injected fault — not by coverage percentage, which is a vanity metric. *(Counters: reward hacking, tautological tests, the held-out gap.)*

**Multi-modal proof of correctness.**
Completion requires the convergence of independent evidence, none of which the generating agent can influence:
- **Behavioral** — tests, written test-first against the spec, scored by mutation analysis.
- **Empirical** — direct, shell-level probes of the running artifact: filesystem state, open ports, response hashes, real I/O. Reality, not the agent's report of reality.
- **Hazard** — STPA-derived analysis of the unsafe control actions a change could enable, with proof that each is controlled; CAST applied to any failure observed in the loop.
A change is "done" only when all three converge. *(Counters: subtle correctness failures, "works on localhost," the lying green build.)*

**Design-time formal verification.**
Before code exists, where a change involves concurrency, ordering, shared state, or a protocol, Orion synthesizes a formal model of *that design*: an LLM drafts it, the human ratifies it (as with the hazard model), and a deterministic model checker exercises it for safety and liveness. The verified invariants compile into behavioral proof obligations, so the artifact-time triad then confirms the code implements the verified design. It is calibrated to the project's reliability tier — a stateless CRUD endpoint skips it; a concurrent state machine does not — and Orion points the same gate at its own orchestrator (the integration queue, path leases, done-gate transitions), so the one non-agentic component is *proven* correct rather than asserted to be. *(Counters: design races, deadlock, reachable unsafe states — systemic bugs no test of the finished product can see.)*

**Built-in reliability primitives.**
A component is not complete until it carries what operations needs: structured logs, trace-context propagation, metrics, stated scaling and concurrency assumptions, and a runbook. Orion validates these as artifacts. The DevOps/SRE lens is applied to every change, not reserved for "reliability work." *(Counters: uninstrumented code, unstated scaling assumptions, the 3 a.m. ownership gap.)*

**Dependency provenance checking.**
Every package reference is verified to exist, resolve to the real artifact, and match expected provenance before it enters the build. Slopsquatting is a supply-chain attack and is treated as one. *(Counters: hallucinated dependencies.)*

**Iteration budget and degradation detection.**
Refinement loops are bounded. Each pass re-evaluates security and quality against the previous iteration; a pass that degrades the artifact terminates the loop rather than prompting for more. "Keep going until it works" is not a control strategy. *(Counters: iterative security degradation.)*

**Side-effect sandboxing and reversibility gates.**
Destructive operations — writes to persistent storage, external API calls, infrastructure changes — execute in a sandbox and require a defined rollback path before they run. Non-reversible actions with real blast radius are flagged and paused for approval. Mid-execution abort is a first-class case. *(Counters: non-reversible side effects, agentic blast radius.)*

**Drift detection and re-anchoring.**
Orion maintains a persistent decision log and periodically re-evaluates active work against the original intent. When alignment degrades past a threshold, the loop pauses and re-grounds before continuing. *(Counters: alignment drift, factual drift.)*

**Per-step confidence and circuit breaker.**
Each step produces a calibrated confidence signal. When confidence degrades or error rate climbs, Orion escalates to a human rather than compounding the error. The circuit breaker is a core component, not an override. *(Counters: multiplicative error compounding, automation bias.)*

**Harness runtime resilience.**
Orion is itself a resilience wrapper around a stochastic, rate-limited, frequently-degraded dependency — the generation model's API — and its own reliability is dominated by how its loop behaves when that dependency misbehaves. Orion holds one retry budget across the *whole* call stack (never multiplicative), a concurrency cap that also governs its own background and speculative traffic, a fallback ladder across models on exhaustion, a loop-level circuit breaker that trips after N bad turns rather than only at the HTTP client, and checkpoint/resume across a provider outage so an incident mid-task does not discard the work. *(Counters: retry amplification, self-inflicted overload, hard-fail on a degraded provider, lost work.)*

**The deployment bar and earned autonomy.**
Orion's completion criterion is the deployment bar: every automated workflow step passes and every independent functional validation passes. When the bar is met at the project's reliability tier, Orion delivers — autonomously where the tier permits. When the bar cannot be met, Orion falls back to a proven, human-mergeable change and routes the open decision to a human. Orion's role is to *hold the bar high*, not to lower it for speed. *(Counters: review collapse, batch-size inflation, false assurance.)*

**The Polaris learning loop.**
On every task, Orion reasons with Polaris's controls catalog, knowledge base, and risk register, so reliability context is present for feature work and reliability work alike. As Orion encounters new failure modes, it contributes them back to Polaris — closing the loop so the platform learns from every run. Consuming Polaris context is the present; contributing failure modes back is the near horizon. *(Counters: institutional-knowledge loss, repeated failures.)*

---

## Part V: What Orion Is Not

Orion is not a linter, a code-review tool, or a static analyzer. It does not run after the development loop; it governs the loop itself.

Orion is not *just* another coding agent. Its interface is an agentic chat (Claude Code / Pi / Hermes lineage), but it does not write the code itself — it spawns and drives the developer's own coding agent and governs the loop, assuming adversarial behavior from the agents it orchestrates. Its value is the opinionated, proof-gated workflow, the governance, and the operability — not the generation.

Orion is not an LLM client. It holds no API key and makes no inference calls; it spawns the developer's coding agent (which uses that agent's own auth — e.g. a Claude Max/Pro login) and drives it over the Agent Client Protocol. Orion's job is the workflow and the proof, not the model call.

Orion is not a trust boundary wrapped around AI to contain it. It is a development loop designed to remain correct despite adversarial behavior from its own components.

Orion is not a claim that today's models produce reliable code on their own. The research is clear that they do not. Orion is the loop that makes the output reliable anyway — and that earns, through proof, the right to ship it.

Orion is not a bet that the next, larger model will finally be reliable. It is the opposite bet: that the generation model will remain a fallible commodity, and that durable reliability must live in the loop that governs it — not in the component it governs.

---

## External Corroboration

These are not idiosyncratic positions. Google SRE, describing how it is engineering reliable operations in the agentic era, independently arrives at Orion's load-bearing principles:

- **Independent harnesses.** *"The AI agent that generates the source code must be strictly isolated from the AI agent that defines the test cases or reviews the output … so that untested correctness requirements are caught mechanically rather than assumed by the authoring LLM."* That is Orion's generation⊥proof invariant verbatim.
- **Proof by deterministic scoring, not LLM opinion.** Final outputs are scored "correct" only when they *deterministically* match verified golden data — "rather than a vague, LLM-generated suggestion."
- **Oversight moves up the abstraction ladder** to reviewing *designs, intent, and policies*, with humans co-authoring and approving specifications **before** code generation.
- **Safety lives in deterministic, caller-agnostic boundaries** that hold "regardless of who or what is calling them," with the reasoning engine decoupled from the execution engine, least-privilege agent identity, circuit breakers, and dry-run actuation.

When the team operating planet-scale production reaches the same conclusions independently, the thesis is not speculative. Orion adopts several of the practices Google's SRE organization has published for AI-assisted operations — autonomy levels, golden-data tiers, dynamic risk evaluation, and a fix-forward posture.

---

*The problem is not the model. The problem is that the development loop hasn't caught up. Orion is the loop that has — and the proof is the right to ship.*

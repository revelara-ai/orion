// Package acceptance holds the Orion V2.0 integration acceptance harness (or-9xl).
//
// This is the FIRST artifact built and the definition of "done" for V2.0: it
// encodes every shell-verifiable acceptance predicate from the PRD
// (docs/PRD/orion-v2.md — "Shell-Verifiable Acceptance Criteria (V2.0)" and the
// "Round-2 additions") as a runnable target. It is RED initially — the `orion`
// binary and the target packages do not exist yet — and the build-orion loop
// turns it green one task at a time. It is re-proven end-to-end last (or-xg7).
//
// Manifesto principle: "Correctness must be proven, not asserted." The verdict
// here is computed by deterministic mechanism (command exit codes + jq
// predicates), not by any agent's claim.
package acceptance

// predKind distinguishes how a predicate is exercised.
type predKind int

const (
	// kindCLI exercises the non-interactive `orion` CLI surface. These predicates
	// are only meaningful once the `orion` binary builds; if it does not, they are
	// hard failures (RED), never spurious passes from a "command not found" exit.
	kindCLI predKind = iota
	// kindGoTest shells out to `go test ./pkg/... -run Name`. A non-zero exit OR a
	// "no packages / cannot find" message is a failure — a missing package must not
	// read as a pass.
	kindGoTest
)

// predicate is one shell-verifiable acceptance criterion, transcribed verbatim
// (modulo runtime id-resolution) from the PRD. Script is run under `bash -c`
// with `set -o pipefail`, ORION_DATA_DIR isolated, and the freshly-built `orion`
// binary first on PATH, from the module root.
type predicate struct {
	Category string
	Name     string
	Kind     predKind
	Script   string
}

// predicates is the full V2.0 target: the core block plus the Round-2 additions.
// Every entry must exit 0 for the V2.0 loop to be PROVEN. The `-run` names are
// the contract the implementation tasks must satisfy exactly.
// driveToApprove is the canonical V2.0 flow up to (and including) spec ratification
// — submit → answer the functional decisions → approve the fallback assumptions →
// approve the spec. It stops BEFORE `orion run`, so predicates that only need an
// accepted spec (e.g. plan show) stay fast. Ratification requires the explicit
// assumption-approval step (or-v9f.19); `orion spec approve` fails without it.
const driveToApprove = `echo "Build an HTTP service that returns the current time." | orion submit --non-interactive >/dev/null && ` +
	`orion answer --key response_format --value json >/dev/null && ` +
	`orion answer --key timezone --value UTC >/dev/null && ` +
	`orion answer --key port --value 8080 >/dev/null && ` +
	`orion answer --key route --value /time >/dev/null && ` +
	`orion spec approve-assumptions >/dev/null && orion spec approve >/dev/null && `

var predicates = []predicate{
	// ── Intent completeness gate (no silent guessing) ────────────────────────
	{"intent-gate", "open_decisions surfaced", kindCLI,
		// 'timezone' was intentionally dropped from the http-service checklist (or-83e:
		// codegen defaults UTC; a zone is a behavioral requirement to state, not a
		// required decision), so the surfaced functional decisions are format/port/route.
		`echo "Build an HTTP service that returns the current time." | orion submit --non-interactive | jq -e '.open_decisions|map(.key)|contains(["response_format","port","route"])'`},
	{"intent-gate", "spec accepted with zero open decisions", kindCLI,
		// "(after answering)" per the PRD: drive the canonical flow, then assert the
		// spec is accepted with no open decisions. Self-contained so the predicate is
		// independent of execution order.
		driveToApprove + `orion spec show --json | jq -e '.status=="accepted" and (.open_decisions|length==0)'`},

	// ── Decomposition + coverage ─────────────────────────────────────────────
	{"decomposition", "plan has tasks, all with proof obligations", kindCLI,
		// Reads the shared canonical project's decomposed plan (established by
		// driveLoop). NOTE: the PRD wrote `.tasks|length>0 and (...)`, which jq parses
		// as `.tasks | (... .tasks[] ...)` — the second clause then indexes the array
		// and errors. Parenthesized so each clause is evaluated against the root.
		`orion plan show --json | jq -e '(.tasks|length>0) and ([.tasks[]|select(.proof_obligation==null)]|length==0)'`},
	{"decomposition", "every spec requirement has a proof obligation", kindGoTest,
		`go test ./internal/decomposer/... -run TestEverySpecRequirementHasProofObligation`},

	// ── Trust-domain independence (the credibility hinge) ────────────────────
	{"trust-domain", "harness isolation", kindGoTest,
		`go test ./internal/proof/... -run TestHarnessIsolation`},
	{"trust-domain", "known faulty artifact rejected", kindGoTest,
		`go test ./internal/proof/... -run TestKnownFaultyArtifactIsRejected`},
	{"trust-domain", "mutation gate rejects tautology", kindGoTest,
		`go test ./internal/proof/behavioral/... -run TestMutationGateRejectsTautology`},

	// ── Multi-modal proof converges; done-gate is real ───────────────────────
	{"proof-converge", "state machine requires all three modes", kindGoTest,
		`go test ./internal/conductor/... -run TestStateMachineRequiresAllThreeModes`},
	{"proof-converge", "task not done while empirical rejects", kindGoTest,
		// Deterministic + order-independent: a report where behavioral passes but the
		// empirical probe rejects converges Reject; the done-gate refuses closure and
		// the task status stays != done (the CLI form was order-dependent and never
		// exercised empirical rejection).
		`go test ./internal/conductor/... -run TestProveAndCloseReportRejectsFailingProbe`},
	{"proof-converge", "empirical: port open and contract satisfied", kindCLI,
		// Reads the empirical proof for the shared canonical project's lead task
		// (established by driveLoop). Order-independent (read-only).
		`TASK=$(orion plan show --json | jq -r '.tasks[0].id') && ` +
			`orion proof show --task "$TASK" --mode empirical --json | jq -e '.port_open and .response_contract_satisfied'`},
	{"proof-converge", "hazard: UCAs considered, none uncontrolled", kindCLI,
		`TASK=$(orion plan show --json | jq -r '.tasks[0].id') && ` +
			`orion proof show --task "$TASK" --mode hazard --json | jq -e '(.ucas_considered|length>0) and (.uncontrolled_ucas|length==0)'`},
	{"proof-converge", "hazard: every control action has a test", kindCLI,
		`TASK=$(orion plan show --json | jq -r '.tasks[0].id') && ` +
			`orion proof show --task "$TASK" --mode hazard --json | jq -e '(.control_actions|length>0) and ([.control_actions[]|select(.test==null)]|length==0)'`},
	{"proof-converge", "control-loop feedback validated", kindGoTest,
		`go test ./internal/proof/hazard/... -run TestControlLoopFeedbackValidated`},

	// ── Operability (3 a.m. test) ────────────────────────────────────────────
	// Read the shared canonical project's delivery record (established by driveLoop's
	// `orion run`). deliver show resolves the last-delivered project once it has left
	// the active slot; a delivery record only exists post-delivery.
	{"operability", "runbook sections present", kindCLI,
		`orion deliver show --runbook --json | jq -e '.sections|keys|contains(["incident_response","escalation_path","known_failure_modes","operational_commands"])'`},
	{"operability", "operating envelope present", kindCLI,
		`orion deliver show --json | jq -e '.operating_envelope!=null'`},

	// ── Security gates ───────────────────────────────────────────────────────
	{"security", "hallucinated dependency rejected", kindCLI,
		`orion deps verify github.com/nonexistent-org/nonexistent-pkg-xyzzy-42 ; test $? -ne 0`},
	{"security", "pre-registered typosquat rejected", kindGoTest,
		`go test ./internal/dependencyprovenance/... -run TestPreRegisteredTyposquatRejected`},
	{"security", "hardcoded secret blocks delivery bar", kindGoTest,
		`go test ./internal/proof/... -run TestHardcodedSecretBlocksDeliveryBar`},
	{"security", "injected instruction rendered inert", kindGoTest,
		`go test ./internal/contextengine/... -run TestInjectedInstructionRenderedInert`},

	// ── Harness reliability ──────────────────────────────────────────────────
	{"harness", "recall rebuilds context after agent kill", kindGoTest,
		`go test ./internal/contextstore/... -run TestRecallRebuildsContextAfterAgentKill`},
	{"harness", "LLM call has timeout and circuit breaker", kindGoTest,
		`go test ./... -run TestLLMCallHasTimeoutAndCircuitBreaker`},
	{"harness", "spend surfaced live in TUI", kindGoTest,
		`go test ./... -run TestSpendIsSurfacedLiveInTUI`},
	{"harness", "run halts and escalates when ceiling configured", kindGoTest,
		`go test ./... -run TestRunHaltsAndEscalatesWhenCeilingConfigured`},
	{"harness", "loop proceeds when Polaris unreachable", kindGoTest,
		`go test ./internal/polaris/... -run TestLoopProceedsWhenPolarisUnreachable`},

	// ── Determinism of the completeness gate ─────────────────────────────────
	{"determinism", "required-decisions checklist is rules-based", kindGoTest,
		`go test ./internal/orchestrator/completeness/... -run TestRequiredDecisionsChecklist`},

	// ── Round-2: Memory & context-erosion ────────────────────────────────────
	{"memory", "anti-erosion pins never evicted under pressure", kindGoTest,
		`go test ./internal/memory/... -run TestAntiErosionPinsNeverEvictedUnderPressure`},
	{"memory", "summarize before evict", kindGoTest,
		`go test ./internal/memory/... -run TestSummarizeBeforeEvict`},
	{"memory", "MTM eviction crash-safe, no loss", kindGoTest,
		`go test ./internal/memory/... -run TestMTMEvictionCrashSafeNoLoss`},
	{"memory", "heat promotion MTM to LTM", kindGoTest,
		`go test ./internal/memory/... -run TestHeatPromotionMTMToLTM`},
	{"memory", "constraint honored 50 steps later", kindGoTest,
		`go test ./internal/contextengine/... -run TestConstraintHonored50StepsLater`},
	{"memory", "pinned spec item never evicted", kindGoTest,
		`go test ./internal/memory/... -run TestPinnedSpecItemNeverEvicted`},
	{"memory", "summarization preserves security constraints", kindGoTest,
		`go test ./internal/memory/... -run TestSummarizationPreservesSecurityConstraints`},
	{"memory", "memory-store / context-store divergence detected", kindGoTest,
		`go test ./internal/memory/... -run TestMemoryStoreContextStoreDivergenceDetected`},
	{"memory", "LTM promotion crash-safe, no corruption", kindGoTest,
		`go test ./internal/memory/... -run TestLTMPromotionCrashSafeNoCorruption`},

	// ── Round-2: Self-evolution (default off; trust-wall; rollback) ───────────
	{"self-evolution", "evolved skill cannot cross proof trust domain", kindGoTest,
		`go test ./internal/memory/... -run TestEvolvedSkillCannotCrossProofTrustDomain`},
	{"self-evolution", "generation LTM never reaches proof prompt", kindGoTest,
		`go test ./internal/memory/... -run TestGenerationLTMNeverReachesProofPrompt`},
	{"self-evolution", "self-evolution regression gate", kindGoTest,
		`go test ./internal/memory/... -run TestSelfEvolutionRegressionGate`},
	{"self-evolution", "LTM evolution rollback", kindGoTest,
		`go test ./internal/memory/... -run TestLTMEvolutionRollback`},
	{"self-evolution", "developer-scoped LTM redacts project literals", kindGoTest,
		`go test ./internal/memory/... -run TestDeveloperScopedLTMRedactsProjectLiterals`},

	// ── Round-2: Executable spec dimensions ──────────────────────────────────
	{"spec-dimensions", "each missing dimension raises an open decision", kindGoTest,
		`for d in scale observability oncall data slo security deps; do go test ./internal/orchestrator/completeness/... -run "TestMissing${d}DimensionRaisesOpenDecision" || exit 1; done`},
	{"spec-dimensions", "stated scale produces capacity proof obligation", kindGoTest,
		`go test ./internal/decomposer/... -run TestStatedScaleDimensionProducesCapacityProofObligation`},
	{"spec-dimensions", "scale fallback preset produces concrete threshold", kindGoTest,
		`go test ./internal/orchestrator/completeness/... -run TestScaleFallbackPresetProducesConcreteThreshold`},

	// ── Round-2: Phase E2 integration (run with -race) ───────────────────────
	{"integration", "lease blocks overlapping scope", kindGoTest,
		`go test -race ./integration/... -run TestLeaseBlocksOverlappingScope`},
	{"integration", "rebase on moved head triggers re-proof", kindGoTest,
		`go test ./integration/... -run TestRebaseOnMovedHeadTriggersReproof`},
	{"integration", "rebase conflict dispatches resolver or escalates", kindGoTest,
		`go test ./integration/... -run TestRebaseConflictDispatchesResolverOrEscalates`},
	{"integration", "post-merge proof red causes rollback", kindGoTest,
		`go test ./integration/... -run TestPostMergeProofRedCausesRollback`},
	{"integration", "stale integration lock recovery on restart", kindGoTest,
		`go test ./integration/... -run TestIntegrationLockStaleLockRecoveryOnRestart`},
	{"integration", "worktree baseline restored after rollback", kindGoTest,
		`go test ./integration/... -run TestWorktreeBaselineRestoredAfterRollback`},
	{"integration", "lease manager no deadlock under contested scopes", kindGoTest,
		`go test -race ./integration/... -run TestLeaseManagerNoDeadlockUnderContestedScopes`},
	{"integration", "resolved merge proof covers all original obligations", kindGoTest,
		`go test ./integration/... -run TestResolvedMergeProofCoversAllOriginalObligations`},

	// ── Round-2: Packages / skills ───────────────────────────────────────────
	{"packages", "third-party package cannot register proof-domain skill", kindGoTest,
		`go test ./skill-registry/... -run TestThirdPartyPackageCannotRegisterProofDomainSkill`},
	{"packages", "installed skill grants capped to role ceiling", kindGoTest,
		`go test ./skill-registry/... -run TestInstalledSkillGrantsCappedToRoleCeiling`},
	{"packages", "eval harness rejects non-deterministic predicate", kindGoTest,
		`go test ./skill-eval/... -run TestEvalHarnessRejectsNonDeterministicPredicate`},

	// ── Round-2: Polaris write integrity ─────────────────────────────────────
	{"polaris", "evidence write is idempotent on retry", kindGoTest,
		`go test ./internal/polaris/... -run TestPolarisEvidenceWriteIsIdempotentOnRetry`},
	{"polaris", "knowledge contribution contains no raw code or paths", kindGoTest,
		`go test ./internal/polaris/... -run TestKnowledgeContributionContainsNoRawCodeOrPaths`},
	{"polaris", "Polaris token not in context store, unreachable from sandbox", kindGoTest,
		`go test ./internal/polaris/... -run TestPolarisTokenNotInContextStoreAndUnreachableFromSandbox`},
}

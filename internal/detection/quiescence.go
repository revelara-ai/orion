package detection

// QuiescenceCheck encodes SPEC §15.3.1's empty-backlog cheap path:
// when there is nothing in the eligible backlog AND the rvl-cli scan
// found nothing new, the tick is "caught up" and should short-circuit
// to a phase=quiescent run row without invoking the autofile or
// per-finding persist paths.
//
// Inputs:
//
//   - eligibleDepth: G_eligible from §15.3.1 — count of
//     eligible+unclaimed open tracker issues for the binding's repo.
//   - scanFindings: count of raw findings the scanner emitted this
//     tick (BEFORE cross-reference; new-vs-known classification has
//     not yet happened at the quiescence-decision point).
//
// Returns true when the tick should be marked quiescent.
//
// Note on the "0 NEW gaps" semantics: at the call site (pre-cross-
// reference) we don't yet know which findings are new vs already
// tracked. The conservative interpretation that matches §15.3.1's
// intent ("nothing to patch AND nothing new from the scan") is:
// scanner output is empty. If the scan returns findings, we proceed
// to cross-reference so the dedup short-circuit + autofile gate can
// classify them — quiescence is reserved for the truly-no-work case.
func QuiescenceCheck(eligibleDepth, scanFindings int) bool {
	return eligibleDepth == 0 && scanFindings == 0
}

-- self_referential_warning persists the §15.4 LoopGuard decision so
-- the operator surface (E8) can render it without recomputing. The
-- decision is per-run, recorded at run-creation time based on the
-- N-most-recent prior runs.

ALTER TABLE detection_runs
    ADD COLUMN self_referential_warning boolean NOT NULL DEFAULT false;

-- Index for the loopguard lookup: "recent N runs for this binding,
-- ordered by started_at DESC, within the 30d window."
-- Already covered by idx_detection_runs_org_binding_started; no new
-- index needed.

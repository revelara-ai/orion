package stpa

// SkeletonModel is the DOMAIN-NEUTRAL STPA fallback for a project that has not
// ratified a hazard model: one abstract loss (the system fails to deliver its
// specified behavior), one controller + control action with a closed feedback loop,
// and one controlled UCA that declares NO source tokens — so its control is verified
// by the behavioral + empirical obligations (the Cases), not a domain-specific code
// grep. It contains no HTTP/time strings, so it never imposes the time domain on an
// arbitrary build. It is honest: with no ratified hazard analysis, the hazard mode
// leans entirely on the executed behavioral contract rather than pretending to do
// domain hazard analysis it was never given.
func SkeletonModel() Model {
	return Model{
		Losses: []Loss{{ID: "L1", Description: "the system fails to deliver its specified behavior"}},
		Structure: ControlStructure{
			Controllers: []string{"Service"},
			Actions: []ControlAction{{
				ID: "CA1", Controller: "Service", Action: "produce the specified output for each input",
				Feedback: FeedbackPath{From: "Service", To: "caller", Signal: "response/result"},
			}},
		},
		UCAs: []UCA{{
			ID: "UCA1", ControlAction: "CA1", Type: NotProvided,
			Hazard: "the specified behavior is not produced", LossRefs: []string{"L1"},
			Disposition: DispositionControlled,
			Rationale:   "verified by the behavioral + empirical proof obligations (no domain control declared)",
			// no Verify tokens → control presence is asserted by the Cases proof, not a grep
		}},
	}
}

// DefaultModel returns the seeded STPA defaults for the canonical Go HTTP
// time-service EXAMPLE — the starting point a developer reviews and CHANGES during
// the questionnaire. It is NOT a generic default: its losses, controllers, and UCAs
// are HTTP/time-specific, and each controlled UCA declares the HTTP/time source
// tokens (Verify) that prove its control. It must NOT be used as the silent fallback
// for an arbitrary project (that imposes the time domain on every build); use
// SkeletonModel for that. This is the time-service example only.
func DefaultModel() Model {
	return Model{
		Losses: []Loss{
			{ID: "L1", Description: "Clients cannot obtain the time (service unavailable)"},
			{ID: "L2", Description: "Clients receive an incorrect or stale time"},
			{ID: "L3", Description: "Service resources exhausted by unbounded/abusive requests"},
		},
		Structure: ControlStructure{
			Controllers: []string{"HTTP Server", "Request Handler"},
			Actions: []ControlAction{
				{
					ID: "CA1", Controller: "Request Handler", Action: "serve time response",
					Feedback: FeedbackPath{From: "Request Handler", To: "metrics/logs", Signal: "response status + latency"},
				},
				{
					ID: "CA2", Controller: "HTTP Server", Action: "accept connection",
					Feedback: FeedbackPath{From: "HTTP Server", To: "metrics", Signal: "in-flight connection count"},
				},
			},
		},
		UCAs: []UCA{
			{ID: "UCA1", ControlAction: "CA1", Type: NotProvided, Hazard: "handler never responds (hang)", LossRefs: []string{"L1"}, Verify: []string{"handleTime", "ListenAndServe"}},
			{ID: "UCA2", ControlAction: "CA1", Type: ProvidedIncorrectly, Hazard: "wrong/stale time or wrong format returned", LossRefs: []string{"L2"}, Verify: []string{"UTC"}},
			{ID: "UCA3", ControlAction: "CA1", Type: WrongTiming, Hazard: "response exceeds acceptable latency / request timeout", LossRefs: []string{"L1"}, Verify: []string{"ReadTimeout", "WriteTimeout"}},
			{ID: "UCA4", ControlAction: "CA2", Type: WrongDuration, Hazard: "connection held open indefinitely (slowloris)", LossRefs: []string{"L3"}, Verify: []string{"ReadHeaderTimeout"}},
		},
		Scenarios: []LossScenario{
			{ID: "S1", Trigger: "burst of requests", SustainingCondition: "no request timeouts or concurrency limits", Loss: "L3", Controls: []string{"server read/write timeouts", "bounded concurrency"}},
			{ID: "S2", Trigger: "timezone/clock handling error", SustainingCondition: "no UTC normalization", Loss: "L2", Controls: []string{"format times in UTC per the ResponseContract"}},
			{ID: "S3", Trigger: "slow client (slowloris)", SustainingCondition: "no ReadHeaderTimeout", Loss: "L1", Controls: []string{"ReadHeaderTimeout"}},
		},
	}
}

// RatifiedTimeServiceModel is the developer-ratified STPA model for the canonical
// HTTP time service (the build-orion HITL session, 2026-06-18). It extends the
// seeded defaults with an SLA-abandonment loss (L4) and an observability +
// incident-response control loop (CA3/CA4), derives the corresponding UCAs, and
// records the developer's dispositions: UCA1–UCA4 controlled, UCA5–UCA7 accepted
// as documented gaps (observability/on-call deferred). It is a golden artifact —
// the same modeling recurs for real users in generic scenarios.
func RatifiedTimeServiceModel() Model {
	m := DefaultModel()
	m.Losses = append(m.Losses, Loss{ID: "L4", Description: "Users abandon the service due to chronic SLA misses (sustained unavailability/latency erodes trust)"})
	m.Structure.Controllers = append(m.Structure.Controllers, "Observability/Monitoring", "On-call/Incident Response")
	m.Structure.Actions = append(m.Structure.Actions,
		ControlAction{ID: "CA3", Controller: "Observability/Monitoring", Action: "emit golden-signal/SLO telemetry + fire alert on SLO breach",
			Feedback: FeedbackPath{From: "Observability/Monitoring", To: "On-call", Signal: "alert delivery + SLO burn-rate"}},
		ControlAction{ID: "CA4", Controller: "On-call/Incident Response", Action: "acknowledge + remediate incident",
			Feedback: FeedbackPath{From: "On-call/Incident Response", To: "runbook/postmortem", Signal: "incident status + MTTR"}},
	)
	// UCA3 also contributes to the SLA-abandonment loss.
	for i := range m.UCAs {
		if m.UCAs[i].ID == "UCA3" {
			m.UCAs[i].LossRefs = []string{"L1", "L4"}
		}
	}
	m.UCAs = append(m.UCAs,
		UCA{ID: "UCA5", ControlAction: "CA3", Type: NotProvided, Hazard: "SLO breached but no alert fires (silent SLA miss)", LossRefs: []string{"L1", "L4"}},
		UCA{ID: "UCA6", ControlAction: "CA3", Type: WrongTiming, Hazard: "alert fires only after a sustained breach", LossRefs: []string{"L4"}},
		UCA{ID: "UCA7", ControlAction: "CA4", Type: NotProvided, Hazard: "alert fires but no ack/remediation (prolonged MTTR)", LossRefs: []string{"L1", "L4"}},
	)
	m.Scenarios = append(m.Scenarios,
		LossScenario{ID: "S4", Trigger: "SLO breach (latency/availability)", SustainingCondition: "no golden-signal telemetry/alerting", Loss: "L4", Controls: []string{"emit golden-signal metrics", "SLO burn-rate alerts"}},
		LossScenario{ID: "S5", Trigger: "incident occurs", SustainingCondition: "no on-call/runbook/escalation", Loss: "L4", Controls: []string{"runbook", "on-call escalation", "incident response"}},
	)
	// Developer dispositions (build-orion HITL, 2026-06-18).
	ctrl := map[string]string{
		"UCA1": "handler always responds; bounded by server timeouts",
		"UCA2": "UTC + JSON ResponseContract verified by behavioral + empirical proof",
		"UCA3": "server read/write timeouts bound latency",
		"UCA4": "ReadHeaderTimeout mitigates slowloris",
	}
	for id, why := range ctrl {
		_ = (&m).Decide(id, DispositionControlled, why, "developer")
	}
	gaps := map[string]string{
		"UCA5": "observability/alerting not yet implemented; accepted gap — revisit when telemetry lands (brownfield-closable)",
		"UCA6": "SLO burn-rate alerting timeliness not yet implemented; accepted gap",
		"UCA7": "on-call/runbook/escalation not yet wired; accepted gap — ties to delivery F2 runbook+escalation",
	}
	for id, why := range gaps {
		_ = (&m).Decide(id, DispositionAcceptedGap, why, "developer")
	}
	return m
}

// RatifyDefaults runs a fresh questionnaire through all four gates with the given
// model (used to accept the defaults, or a developer-amended model, as ratified).
func RatifyDefaults(m Model) (Model, error) {
	q := New()
	if err := q.RatifyLosses(m.Losses); err != nil {
		return Model{}, err
	}
	if err := q.RatifyControlStructure(m.Structure); err != nil {
		return Model{}, err
	}
	if err := q.RatifyUCAs(m.UCAs); err != nil {
		return Model{}, err
	}
	if err := q.RatifyLossScenarios(m.Scenarios); err != nil {
		return Model{}, err
	}
	return q.Model()
}

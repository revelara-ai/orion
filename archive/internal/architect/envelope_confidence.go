package architect

// computeEnvelopeConfidence derives a 0-1 quality score from coverage
// signals over the model's discovered services. Per SPEC §12.1 the score
// drives downstream synthesis gating: below `envelope_confidence_floor`
// (default 0.4) the verifier escalates "customer:eligibility_question"
// instead of synthesizing patches.
//
// Component signals (each in [0,1]):
//
//   - service_coverage:    1.0 if at least one service was discovered
//   - endpoint_coverage:   fraction of services with at least one endpoint
//   - dependency_coverage: fraction of services with at least one downstream dep
//
// Composite Score is a weighted average favoring endpoint coverage (the
// surface a verifier needs to know about). If no services are present at
// all, every component is zero.
func computeEnvelopeConfidence(model *ArchitecturalModel) EnvelopeConfidence {
	n := len(model.Services)
	if n == 0 {
		return EnvelopeConfidence{}
	}

	var withEndpoints, withDeps int
	for _, svc := range model.Services {
		if len(svc.Endpoints) > 0 {
			withEndpoints++
		}
		if len(svc.DownstreamDeps) > 0 {
			withDeps++
		}
	}

	endpointCov := float64(withEndpoints) / float64(n)
	depCov := float64(withDeps) / float64(n)
	const serviceCov = 1.0 // we have at least one service

	// Weighted: services 0.2, endpoints 0.5, deps 0.3.
	score := serviceCov*0.2 + endpointCov*0.5 + depCov*0.3

	return EnvelopeConfidence{
		Score:              score,
		ServiceCoverage:    serviceCov,
		EndpointCoverage:   endpointCov,
		DependencyCoverage: depCov,
	}
}

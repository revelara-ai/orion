package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/detection"
	"github.com/revelara-ai/orion/internal/risksink"
)

// risksinkAdapter bridges the detection package's narrow RiskSink
// interface to the risksink package's concrete Sink. Lives in the
// wiring layer so internal/detection has no upward dep on
// internal/risksink.
type risksinkAdapter struct {
	inner risksink.Sink
}

func (a *risksinkAdapter) Submit(ctx context.Context, findingID uuid.UUID, p detection.RiskPayload) (detection.RiskSubmitResult, error) {
	r := risksink.Risk{
		Origin:       p.Origin,
		Slug:         p.Slug,
		Title:        p.Title,
		Category:     p.Category,
		Severity:     p.Severity,
		Confidence:   p.Confidence,
		ControlCodes: p.ControlCodes,
		FilePath:     p.FilePath,
		LineNo:       p.LineNo,
		Fingerprint:  p.Fingerprint,
		BindingID:    p.BindingID,
		FindingID:    p.FindingID,
	}
	res, err := a.inner.Submit(ctx, findingID, r)
	if err != nil {
		return detection.RiskSubmitResult{}, err
	}
	return detection.RiskSubmitResult{Posted: res.Posted, Queued: res.Queued}, nil
}

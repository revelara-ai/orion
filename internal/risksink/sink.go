// Package risksink emits Orion's detection findings as Polaris risks
// per SPEC §15.3. When Polaris is reachable, the PolarisSink POSTs
// each finding to its risk-register endpoint with the standard
// `origin=orion-detection` provenance. When Polaris is unreachable
// (404, 5xx, timeout, connection refused), LocalFallbackSink queues
// the payload to risksink_pending so the detection tick still
// completes successfully.
//
// Decoupling: Epic 3 ships before Polaris's write surface (E7). The
// LocalFallback path is the default v1 behavior — operators who don't
// set POLARIS_BASE_URL get queue-only mode, which is what the bookend
// pins.
package risksink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/repos"
)

// Risk is the minimum payload Polaris's POST /api/v1/risks needs. v1
// keeps the shape narrow; richer fields land alongside the Polaris
// write surface in E7.
type Risk struct {
	Origin       string   `json:"origin"`
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	Category     string   `json:"category"`
	Severity     string   `json:"severity"`
	Confidence   string   `json:"confidence"`
	ControlCodes []string `json:"control_codes"`
	FilePath     string   `json:"file_path"`
	LineNo       int      `json:"line_no"`
	Fingerprint  string   `json:"fingerprint"`
	BindingID    string   `json:"binding_id"`
	FindingID    string   `json:"finding_id"`
}

// SinkResult communicates whether the risk was posted to Polaris or
// queued locally. Persistent infrastructure failure (DB write
// failure) surfaces as an error.
type SinkResult struct {
	Posted bool
	Queued bool
	Status int    // HTTP status from Polaris when Posted is true
	Reason string // human-readable; populated for queue path
}

// Sink is the interface LoopDriver (E3-2) calls after a successful
// autofile. Implementations: PolarisSink (network), LocalFallbackSink
// (queue-on-failure), NoopSink (default when neither is configured).
type Sink interface {
	Submit(ctx context.Context, findingID uuid.UUID, risk Risk) (SinkResult, error)
}

// NoopSink discards risks. Used when neither Polaris nor a local
// pending repo is wired (e.g., a CLI dry-run path).
type NoopSink struct{}

// Submit is a no-op that reports the discard.
func (NoopSink) Submit(_ context.Context, _ uuid.UUID, _ Risk) (SinkResult, error) {
	return SinkResult{Reason: "noop sink: discarded"}, nil
}

// PolarisSink POSTs each risk to Polaris's risk-register endpoint.
// Endpoint resolution: BaseURL + "/risks" (matches Polaris's
// /api/v1/risks convention; the caller supplies BaseURL such as
// "https://polaris.relynce-dev.example/api/v1").
type PolarisSink struct {
	BaseURL    string
	APIKey     string //nolint:gosec // G117: bearer token, intentionally serialized through this struct
	HTTPClient *http.Client
	// Timeout is applied per request. Defaults to 5s when zero.
	Timeout time.Duration
}

// NewPolarisSink builds a PolarisSink with a default http.Client that
// honors Timeout.
func NewPolarisSink(baseURL, apiKey string) *PolarisSink {
	return &PolarisSink{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Timeout: 5 * time.Second,
	}
}

// Submit POSTs the risk and returns Posted=true on 2xx, error
// otherwise. The LocalFallbackSink wraps this so transport/5xx
// failures get queued.
func (s *PolarisSink) Submit(ctx context.Context, _ uuid.UUID, risk Risk) (SinkResult, error) {
	if s.BaseURL == "" {
		return SinkResult{}, fmt.Errorf("risksink: PolarisSink.BaseURL is empty")
	}
	body, err := json.Marshal(risk)
	if err != nil {
		return SinkResult{}, fmt.Errorf("risksink: marshal: %w", err)
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := s.BaseURL + "/risks"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return SinkResult{}, fmt.Errorf("risksink: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req) //nolint:gosec // G704: URL is operator-provided BaseURL from config, not user input
	if err != nil {
		return SinkResult{}, fmt.Errorf("risksink: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return SinkResult{Posted: true, Status: resp.StatusCode}, nil
	}
	// Drain the body for the error message.
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	return SinkResult{Status: resp.StatusCode}, fmt.Errorf("risksink: polaris %d: %s", resp.StatusCode, string(msg))
}

// PendingQueue is the persistence surface LocalFallbackSink uses.
// Production wires repos.RiskSinkPendingRepo.Enqueue; tests pass an
// in-memory stub.
type PendingQueue interface {
	Enqueue(ctx context.Context, e repos.RiskSinkPending) (repos.RiskSinkPending, error)
}

// LocalFallbackSink wraps an Upstream Sink with persistent queueing
// for failures. If Upstream returns Posted=true, the queue is
// untouched. If Upstream errors OR returns a non-2xx without
// Posted=true, the payload is enqueued and SinkResult.Queued=true.
//
// The default RetryAfter delay is 1 minute; production drains
// schedule themselves.
type LocalFallbackSink struct {
	Upstream        Sink
	Queue           PendingQueue
	RetryAfterDelay time.Duration
	Endpoint        string // recorded on the pending row for the drain job
}

// NewLocalFallbackSink composes an Upstream Sink with a Queue.
// When upstream is nil, the sink ALWAYS queues (queue-only mode);
// callers can use this to ship before Polaris's write surface lands.
func NewLocalFallbackSink(upstream Sink, queue PendingQueue, endpoint string) *LocalFallbackSink {
	return &LocalFallbackSink{
		Upstream:        upstream,
		Queue:           queue,
		RetryAfterDelay: time.Minute,
		Endpoint:        endpoint,
	}
}

// Submit applies the try-then-queue semantics.
func (s *LocalFallbackSink) Submit(ctx context.Context, findingID uuid.UUID, risk Risk) (SinkResult, error) {
	if s.Queue == nil {
		return SinkResult{}, fmt.Errorf("risksink: LocalFallbackSink.Queue is nil")
	}

	// Phase 1: try the upstream when wired.
	if s.Upstream != nil {
		res, err := s.Upstream.Submit(ctx, findingID, risk)
		if err == nil && res.Posted {
			return res, nil
		}
		// Phase 2: queue on any non-Posted outcome.
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		if queued, qerr := s.enqueue(ctx, findingID, risk, errMsg); qerr != nil {
			return SinkResult{}, fmt.Errorf("risksink: queue after upstream fail (%v): %w", err, qerr)
		} else {
			return queued, nil
		}
	}

	// No upstream configured: queue directly. Pass empty lastErr so
	// the pending row records this as a fresh enqueue (Attempts=0)
	// rather than a failed attempt.
	return s.enqueue(ctx, findingID, risk, "")
}

func (s *LocalFallbackSink) enqueue(ctx context.Context, findingID uuid.UUID, risk Risk, lastErr string) (SinkResult, error) {
	body, err := json.Marshal(risk)
	if err != nil {
		return SinkResult{}, fmt.Errorf("risksink: marshal for queue: %w", err)
	}
	delay := s.RetryAfterDelay
	if delay <= 0 {
		delay = time.Minute
	}
	pending := repos.RiskSinkPending{
		FindingID:       findingID,
		PolarisEndpoint: s.Endpoint,
		Payload:         body,
		Attempts:        0,
		RetryAfter:      time.Now().Add(delay),
	}
	if lastErr != "" {
		pending.LastError = &lastErr
		now := time.Now()
		pending.LastAttemptAt = &now
		pending.Attempts = 1
	}
	if _, err := s.Queue.Enqueue(ctx, pending); err != nil {
		return SinkResult{}, fmt.Errorf("risksink: enqueue: %w", err)
	}
	reason := "queued (upstream failed)"
	if lastErr == "" {
		reason = "queued (no upstream configured)"
	}
	return SinkResult{Queued: true, Reason: reason}, nil
}

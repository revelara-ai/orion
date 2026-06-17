package harness

import (
	"encoding/json"
	"fmt"
)

// BuildToxiproxyConfig converts a Harness's FaultProfile list into the
// JSON config consumed by ghcr.io/shopify/toxiproxy.
//
// Each fault profile becomes one toxiproxy "proxy" with the latency /
// down / packet-loss toxics applied. The proxy listens on a port the
// SUT Deployment is wired to talk to.
//
// Returns the marshaled config as a string suitable for embedding in a
// ConfigMap.
func BuildToxiproxyConfig(h *Harness) (string, error) {
	if h == nil {
		return "", fmt.Errorf("harness: nil harness")
	}
	type toxic struct {
		Name       string         `json:"name"`
		Type       string         `json:"type"`
		Stream     string         `json:"stream,omitempty"`
		Attributes map[string]any `json:"attributes,omitempty"`
	}
	type proxy struct {
		Name     string  `json:"name"`
		Listen   string  `json:"listen"`
		Upstream string  `json:"upstream"`
		Enabled  bool    `json:"enabled"`
		Toxics   []toxic `json:"toxics,omitempty"`
	}
	var proxies []proxy
	port := 20000
	for _, f := range h.Faults.Faults {
		p := proxy{
			Name:     f.TargetName,
			Listen:   fmt.Sprintf("0.0.0.0:%d", port),
			Upstream: fmt.Sprintf("%s.%s.svc.cluster.local:80", f.TargetName, h.Namespace),
			Enabled:  true,
		}
		port++
		if f.LatencyP50Ms > 0 {
			jitter := f.LatencyP99Ms - f.LatencyP50Ms
			if jitter < 0 {
				jitter = 0
			}
			p.Toxics = append(p.Toxics, toxic{
				Name:       "latency",
				Type:       "latency",
				Stream:     "downstream",
				Attributes: map[string]any{"latency": f.LatencyP50Ms, "jitter": jitter},
			})
		}
		if f.ErrorRate > 0 || f.PartitionProbability > 0 {
			// toxiproxy doesn't model rate-based errors directly; the
			// closest mechanism is `limit_data` (bytes=0 returns EOF).
			// The verifier translates the (ErrorRate, Partition) tuple
			// into expected error-rate at metrics time.
			p.Toxics = append(p.Toxics, toxic{
				Name:       "limit_data",
				Type:       "limit_data",
				Stream:     "downstream",
				Attributes: map[string]any{"bytes": 0},
			})
		}
		proxies = append(proxies, p)
	}
	b, err := json.MarshalIndent(proxies, "", "  ")
	if err != nil {
		return "", fmt.Errorf("harness: marshal toxiproxy config: %w", err)
	}
	return string(b), nil
}

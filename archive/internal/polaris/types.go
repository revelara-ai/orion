// Package polaris provides a thin read-only client for the Polaris REST
// API. v1 supports only the endpoints orion's synthesis pipeline needs
// for the controls catalog: GET /api/v1/controls. Knowledge enrichment
// endpoints (foresight, search, insights) and write endpoints (claim,
// run-complete, evidence) land in later epics; deferred per E1-4 scope.
//
// Snapshot discipline (SPEC §14.6): callers fetch a ControlsCatalog at
// run-start and pass the snapshot into downstream consumers (the
// constraints inferer, the patch synthesizer). The client itself does
// not maintain any in-memory cache; if a caller wants snapshotting,
// they hold the returned ControlsCatalog.
package polaris

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Sentinel errors for callers to errors.Is against.
var (
	// ErrAuthMissing: required POLARIS_API_KEY not set.
	ErrAuthMissing = errors.New("polaris: api key not configured")

	// ErrInvalidConfig: required config field other than auth is unset.
	ErrInvalidConfig = errors.New("polaris: invalid config")

	// ErrPolarisUnreachable: client could not reach the Polaris API
	// (network error, exhausted retries on 5xx). Wrapped error carries
	// the underlying cause.
	ErrPolarisUnreachable = errors.New("polaris: unreachable after retries")

	// ErrUnexpectedStatus: Polaris returned a non-2xx status that is
	// not one of the documented retryable cases.
	ErrUnexpectedStatus = errors.New("polaris: unexpected status code")
)

// Control is the orion-side representation of one control from Polaris's
// catalog. Field set is the subset orion's synthesis pipeline uses; new
// fields are added here, not by exposing Polaris's full struct, so that
// Polaris-side refactors don't break orion contracts.
type Control struct {
	ID          uuid.UUID `json:"id"`
	ControlCode string    `json:"control_code"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Objective   string    `json:"objective,omitempty"`
	Category    string    `json:"category"`
	Type        string    `json:"type,omitempty"`
	Treatment   string    `json:"treatment,omitempty"`
	Weight      int       `json:"weight,omitempty"`
}

// ListControlsResponse mirrors Polaris's wire format.
type ListControlsResponse struct {
	Controls []Control `json:"controls"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	Limit    int       `json:"limit"`
}

// ControlsCatalog is the snapshotted output of one ListControls call.
// Callers pin the catalog at run-start and pass it into downstream
// consumers; the catalog provides cheap lookups without further IO.
type ControlsCatalog struct {
	Controls []Control `json:"controls"`
	Total    int       `json:"total"`

	// SnapshotAt is the wall-clock time the snapshot was taken. Useful
	// for staleness checks and reproduction reports.
	SnapshotAt string `json:"snapshot_at"`
}

// ByCode returns the Control with the given code (e.g., "RC-001"), or
// nil if not present in the catalog. Case-insensitive.
func (c *ControlsCatalog) ByCode(code string) *Control {
	want := strings.ToLower(code)
	for i := range c.Controls {
		if strings.ToLower(c.Controls[i].ControlCode) == want {
			return &c.Controls[i]
		}
	}
	return nil
}

// ByCategory returns all controls matching the given category.
// Stable order (input order). Empty if none match.
func (c *ControlsCatalog) ByCategory(category string) []Control {
	var out []Control
	for i := range c.Controls {
		if c.Controls[i].Category == category {
			out = append(out, c.Controls[i])
		}
	}
	return out
}

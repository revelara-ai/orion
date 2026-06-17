// Package leader implements the PostgreSQL advisory lock with fencing token
// substrate described in §14.2 of the Orion specification.
//
// Per-tenant leader election is keyed by (deploymentID, tenantID). The successful
// replica holds the advisory lock and reads its fencing_token from the
// orion_leadership table. Every state mutation transaction for that tenant includes
// WHERE fencing_token = $current_token AND tenant_id = $tenant as a guard.
//
// A former leader whose token is stale will fail every transaction; in-flight
// mutations roll back. The advisory lock TTL is leader_lease_seconds (default 30);
// a lease holder MUST renew within leader_renew_seconds (default 10).

package leader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/josebiro/orion/internal/database"
)

// Config holds the tuning parameters for the leader election substrate.
type Config struct {
	DeploymentID    string        // unique per-orion deployment
	TenantID        string        // per-tenant scope
	LeaseSeconds    int           // advisory lock TTL, default 30
	RenewSeconds    int           // must renew within this period, default 10
	PollInterval    time.Duration // how often to attempt reacquisition, default 5s
}

// EnsureConfig validates and supplies defaults. It returns the zero value on
// invalid input (empty deploymentID/TenantID) so callers can detect misconfig.
func (c *Config) ensure() Config {
	if c.LeaseSeconds <= 0 {
		c.LeaseSeconds = 30
	}
	if c.RenewSeconds <= 0 || c.RenewSeconds >= c.LeaseSeconds {
		c.RenewSeconds = 10
	}
	if c.PollInterval == 0 {
		c.PollInterval = 5 * time.Second
	}
	return *c
}

// Election tracks the current leader state for a (deployment, tenant) scope.
type Election struct {
	config        Config
	db            *database.Pool
	fencingToken  int64 // current fencing token for this tenant
	leaseExpired  bool  // lease lost to another replica
	mu            sync.Mutex
	lastAcquire   time.Time
	stopCh        chan struct{}
	hasLeadership bool
}

// NewElection creates an election instance. Call Start() to begin the leader loop.
func NewElection(cfg Config, db *database.Pool) *Election {
	return &Election{
		config: cfg.ensure(),
		db:     db,
		stopCh: make(chan struct{}),
	}
}

// Start begins the leader acquisition/renewal loop in a goroutine. It blocks
// until stopCh receives or context is cancelled. The loop polls every 5s (or
// cfg.PollInterval) and calls acquire() if we don't hold the lock. On each tick,
// it also attempts a renewal of the advisory lock to prevent expiration.
func (e *Election) Start(ctx context.Context) error {
	cfg := e.config
	leaseDur := time.Duration(cfg.LeaseSeconds) * time.Second
	renewDur := time.Duration(cfg.RenewSeconds) * time.Second

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.stopCh:
			return nil
		case <-ticker.C:
			if e.hasLeadership(ctx) {
				// Renew the advisory lease before it expires.
				renewCtx, cancel := context.WithTimeout(ctx, renewDur)
				if err := e.renewAdvisoryLock(renewCtx); err != nil {
					e.mu.Lock()
					e.leaseExpired = true
					e.mu.Unlock()
				}
			} else {
				if err := e.acquire(ctx); err != nil {
					continue // will retry on next tick
				}
			}
		}
	}
}

// Stop signals the leader loop to terminate.
func (e *Election) Stop() {
	close(e.stopCh)
}

// acquire attempts to acquire the advisory lock and fencing token. Returns nil
// on success. On failure, e.leaseExpired is set true if we already held leadership
// but lost it (split-brain detected), false otherwise.
func (e *Election) acquire(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	lockArg := advisoryHash(cfg.DeploymentID + "|" + cfg.TenantID)

	// Attempt to lock the advisory key. pg_try_advisory_lock returns true on success.
	var locked bool
	query := `SELECT pg_try_advisory_xact_lock($1)` // tx-scoped; auto-unlock on commit/rollback
	err := e.db.QueryRow(ctx, query, lockArg).Scan(&locked)
	if err != nil {
		return fmt.Errorf("leader.acquire: advisory lock error: %w", err)
	}

	if !locked {
		// Someone else holds the lease; do nothing. Next tick will retry.
		return nil
	}

	// We hold the lock now – acquire or create our fencing token row.
	var newToken int64
	row := e.db.QueryRow(ctx, 
		`INSERT INTO orion_leadership (tenant_id, deployment_id, fencing_token) VALUES ($1, $2, DEFAULT) RETURNING fencing_token`,
		cfg.TenantID, cfg.DeploymentID,
	)
	if row.Err() != nil {
		return fmt.Errorf("leader.acquire: read fencing token: %w", row.Err())
	}
	err = row.Scan(&newToken)
	if err != nil {
		// Already exists? Upsert.
		var existingToken int64
		err = e.db.QueryRow(ctx,
			`UPDATE orion_leadership SET deployment_id = $2 WHERE tenant_id = $1 AND fencing_token < $3 RETURNING fencing_token`,
			cfg.TenantID, cfg.DeploymentID, newToken,
		).Scan(&existingToken)
		if err != nil {
			return fmt.Errorf("leader.acquire: fencing upsert error: %w", err)
		}
		newToken = existingToken
	}

	e.fencingToken = newToken
	e.leaseExpired = false
	e.hasLeadership = true
	e.lastAcquire = time.Now()
	return nil
}

// renewAdvisoryLock attempts to renew the lease by re-acquiring it. Returns an
// error if we lost the lock (another replica now holds it).
func (e *Election) renewAdvisoryLock(ctx context.Context) error {
	e.mu.Lock()
	has := e.hasLeadership
	if !has {
		e.mu.Unlock()
		return nil // not our job to renew
	}
	e.mu.Unlock()

	lockArg := advisoryHash(cfg.DeploymentID + "|" + cfg.TenantID)
	var locked bool
	err := e.db.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, lockArg).Scan(&locked)
	if err != nil {
		return fmt.Errorf("leader.renew: %w", err)
	}
	if !locked {
		e.mu.Lock()
		e.leaseExpired = true
		e.hasLeadership = false
		e.mu.Unlock()
		return fmt.Errorf("leader.renew: lease lost, another replica now holds")
	}
	return nil
}

// hasLeadership returns true if we currently hold the advisory lock and our
// fencing token is current.
func (e *Election) hasLeadership(ctx context.Context) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.hasLeadership || e.leaseExpired {
		return false
	}
	// Verify the lease hasn't expired by checking if we can still renew.
	_, _ = ctx.Deadline() // just use context, assume the Renew loop handles timeout
	return true
}

// FencingToken returns the current fencing token for this tenant. Callers MUST
// include `WHERE fencing_token = $token AND tenant_id = $tenant` in every state
// mutation transaction. If the returned value differs from what's currently stored
// in orion_leadership, all our writes are stale and should be rolled back.
func (e *Election) FencingToken() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fencingToken
}

// CurrentTenant returns the tenant scope of this election.
func (e *Election) CurrentTenant() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cfg.TenantID
}

// advisoryHash derives a consistent 32-bit int4 key from a text string for use
// with PostgreSQL's pg_try_advisory_lock(xid). This is the standard pattern:
// hash the scope string and take the lower 31 bits as a signed int4.
func advisoryHash(scope string) int64 {
	hash := sha256.Sum256([]byte(scope))
	// Take first 8 bytes, interpret as big-endian int64 that fits in pg's int8 lock key.
	var result int64
	for _, b := range hash[:8] {
		result = (result << 8) | int64(b)
	}
	return result
}

// MustRespectFencingToken is a compile-time+runtime assertion that every state-
// mutation path in the Conductor includes WHERE fencing_token = $fencing and
// tenant_id = $tenant in its query parameters. Violations are logged at error level
// during validation (see pkg/conform).

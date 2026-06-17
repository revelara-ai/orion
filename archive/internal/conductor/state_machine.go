package conductor

import "fmt"

type State string

const (
	StateCreated       State = "created"
	StateInventorying  State = "inventorying"
	StateScanning      State = "scanning"
	StateBacklogActive State = "backlog_active"
	StateDraining      State = "draining"
	StatePaused        State = "paused"
	StateCompleted     State = "completed"
	StateFailed        State = "failed"
)

type Run struct {
	ID            string `db:"id"`
	TenantID      string `db:"tenant_id"`
	DeploymentID  string `db:"deployment_id"`
	State         State  `db:"state"`
	IssuesCount   int    `db:"issues_count"`
	TickCount     int    `db:"tick_count"`
	BacklogLength int    `db:"backlog_length"`
}

func (r *Run) ValidateState() error {
	switch r.State {
	case StateCreated, StateInventorying, StateScanning, StateBacklogActive,
		StateDraining, StatePaused, StateCompleted, StateFailed:
		return nil
	default:
		return fmt.Errorf("conductor.ValidateState: invalid state %q for run %s", r.State, r.ID)
	}
}

func (r *Run) Validate() error {
	if r.TenantID == "" {
		return fmt.Errorf("conductor.Validate: TenantID cannot be empty")
	}
	return nil
}

type ConductorConfig struct {
	TenantID          string
	DeploymentID      string
	Pool              interface{} // placeholder for *database.Pool
	TickSecs          int    // defaults to 10 in ensureDefaults()
	LivenessProbeSec  int    // defaults to 15 in ensureDefaults()
	CapPerTenant      int    // concurrency cap (1-64)
}

func (c *ConductorConfig) ensureDefaults() {
	if c.TickSecs < 1 {
		c.TickSecs = 10
	}
	if c.LivenessProbeSec < 1 {
		c.LivenessProbeSec = 15
	}
	if c.CapPerTenant < 1 || c.CapPerTenant > 64 {
		c.CapPerTenant = 4
	}
}

type Conductor struct {
	cfg      *ConductorConfig
	pool     interface{} // placeholder for *database.Pool
	log      Logger
	state    map[string]State
	tickDone chan uint64
}

func validateConcurrency(cap int) error {
	if cap < 1 || cap > 64 {
		return fmt.Errorf("conductor.validateConcurrency: must be in range [1, 64], got %d", cap)
	}
	return nil
}

func (c *ConductorConfig) Validate() error {
	if c.TenantID == "" {
		return fmt.Errorf("conductor.Validate: TenantID is required")
	}
	if c.DeploymentID == "" {
		return fmt.Errorf("conductor.Validate: DeploymentID is required")
	}
	if c.TickSecs < 1 {
		return fmt.Errorf("conductor.Validate: TickSecs must be > 0 (got %d)", c.TickSecs)
	}
	if c.LivenessProbeSec < 1 {
		return fmt.Errorf("conductor.Validate: LivenessProbeSec must be > 0 (got %d)", c.LivenessProbeSec)
	}
	if c.CapPerTenant < 1 || c.CapPerTenant > 64 {
		return fmt.Errorf("conductor.Validate: CapPerTenant must be in range [1, 64] (got %d)", c.CapPerTenant)
	}
	return nil
}

// New instantiates a Conductor. The caller may configure the state map and channel before use.
func New() *Conductor {
	return &Conductor{
		cfg:      &ConductorConfig{},
		state:    make(map[string]State),
		tickDone: make(chan uint64, 1024),
	}
}

type Logger interface {
	Printf(format string, args ...interface{}) error
	Errorf(format string, args ...interface{}) error
}

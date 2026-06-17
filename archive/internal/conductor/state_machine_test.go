package conductor

import (
	"testing"
)

func TestValidateState(t *testing.T) {
	tests := []struct {
		name    string
		run     Run
		wantErr bool
	}{
		{"Created state valid", Run{ID: "r1", TenantID: "t1", DeploymentID: "d1", State: StateCreated}, false},
		{"Inventorying state valid", Run{ID: "r2", TenantID: "t1", DeploymentID: "d1", State: StateInventorying}, false},
		{"Scanning state valid", Run{ID: "r3", TenantID: "t1", DeploymentID: "d1", State: StateScanning}, false},
		{"BacklogActive state valid", Run{ID: "r4", TenantID: "t1", DeploymentID: "d1", State: StateBacklogActive}, false},
		{"Draining state valid", Run{ID: "r5", TenantID: "t1", DeploymentID: "d1", State: StateDraining}, false},
		{"Paused state valid", Run{ID: "r6", TenantID: "t1", DeploymentID: "d1", State: StatePaused}, false},
		{"Completed state valid", Run{ID: "r7", TenantID: "t1", DeploymentID: "d1", State: StateCompleted}, false},
		{"Failed state valid", Run{ID: "r8", TenantID: "t1", DeploymentID: "d1", State: StateFailed}, false},
		{"Invalid state fails", Run{ID: "r9", TenantID: "t1", DeploymentID: "d1", State: "bogus"}, true},
		{"Empty state fails", Run{ID: "r10", TenantID: "t1", DeploymentID: "d1", State: ""}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.run.ValidateState(); (got != nil) != tt.wantErr {
				t.Errorf("ValidateState() error = %v, wantError %v", got, tt.wantErr)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		run     Run
		wantErr bool
	}{
		{"Valid run passes", Run{ID: "r1", TenantID: "t1", DeploymentID: "d1", State: StateCreated}, false},
		{"Empty TenantID fails", Run{ID: "r2", TenantID: "", DeploymentID: "d1", State: StateCreated}, true},
		{"Empty ID passes", Run{ID: "", TenantID: "t2", DeploymentID: "d2", State: StateCompleted}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.run.Validate(); (got != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantError %v", got, tt.wantErr)
			}
		})
	}
}

func TestValidateConcurrency(t *testing.T) {
	tests := []struct {
		name    string
		cap     int
		wantErr bool
	}{
		{"Min valid (1)", 1, false},
		{"Max valid (64)", 64, false},
		{"Mid value", 8, false},
		{"Zero fails", 0, true},
		{"Negative fails", -1, true},
		{"Exceeds max (65)", 65, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateConcurrency(tt.cap); (got != nil) != tt.wantErr {
				t.Errorf("validateConcurrency(%d) error = %v, wantError %v", tt.cap, got, tt.wantErr)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    ConductorConfig
		wantErr   bool
		errMsgSub string
	}{
		{"Valid config", ConductorConfig{TenantID: "t1", DeploymentID: "d1", TickSecs: 10, LivenessProbeSec: 15, CapPerTenant: 4}, false, ""},
		{"Missing TenantID", ConductorConfig{TenantID: "", DeploymentID: "d1", TickSecs: 10, LivenessProbeSec: 15, CapPerTenant: 4}, true, "TenantID is required"},
		{"Missing DeploymentID", ConductorConfig{TenantID: "t1", DeploymentID: "", TickSecs: 10, LivenessProbeSec: 15, CapPerTenant: 4}, true, "DeploymentID is required"},
		{"Zero TickSecs", ConductorConfig{TenantID: "t1", DeploymentID: "d1", TickSecs: 0, LivenessProbeSec: 15, CapPerTenant: 4}, true, "TickSecs must be > 0"},
		{"Negative TickSecs", ConductorConfig{TenantID: "t2", DeploymentID: "d2", TickSecs: -5, LivenessProbeSec: 15, CapPerTenant: 4}, true, "TickSecs must be > 0"},
		{"Zero LivenessProbeSec", ConductorConfig{TenantID: "t3", DeploymentID: "d3", TickSecs: 10, LivenessProbeSec: 0, CapPerTenant: 4}, true, "LivenessProbeSec must be > 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsgSub != "" && err != nil {
				if !containsSubstring(err.Error(), tt.errMsgSub) {
					t.Errorf("Validate() error message contains %q, expected substring not found", tt.errMsgSub)
				}
			}
		})
	}
}

func TestEnsureDefaults(t *testing.T) {
	cfg := &ConductorConfig{TenantID: "t1", DeploymentID: "d1"}
	cfg.ensureDefaults()
	if cfg.TickSecs != 10 {
		t.Errorf("ensureDefaults TickSecs = %d, want 10", cfg.TickSecs)
	}
	if cfg.LivenessProbeSec != 15 {
		t.Errorf("ensureDefaults LivenessProbeSec = %d, want 15", cfg.LivenessProbeSec)
	}
	if cfg.CapPerTenant != 4 {
		t.Errorf("ensureDefaults CapPerTenant = %d, want 4", cfg.CapPerTenant)
	}
}

func TestNewConductor(t *testing.T) {
	cdr := New()
	if cdr == nil {
		t.Fatal("New() returned nil")
	}
	if cdr.state == nil {
		t.Error("state map is nil after New()")
	}
	if len(cdr.state) != 0 {
		t.Errorf("state map has %d entries, expected 0", len(cdr.state))
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

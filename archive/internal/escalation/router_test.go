package escalation

import (
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		event   FailEvent
		wantErr bool
	}{
		{
			name: "all fields populated",
			event: FailEvent{
				Kind:        HarnessFailure,
				RunID:       "run-001",
				Description: "harness crash",
			},
			wantErr: false,
		},
		{
			name: "empty Kind fails validation",
			event: FailEvent{
				Kind:        "",
				RunID:       "run-002",
				Description: "test case",
			},
			wantErr: true,
		},
		{
			name: "unknown failure kind",
			event: FailEvent{
				Kind:        FailureKind("bogus"),
				RunID:       "run-003",
				Description: "test case",
			},
			wantErr: true,
		},
		{
			name: "empty RunID fails validation",
			event: FailEvent{
				Kind:        HarnessFailure,
				RunID:       "",
				Description: "test case",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.event.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantErr)
			}
		})
	}
}

func TestRoute(t *testing.T) {
	tests := []struct {
		name string
		kind FailureKind
		want string
	}{
		{
			name: "HarnessFailure routes to harness_failure",
			kind: HarnessFailure,
			want: "revelara:harness_failure",
		},
		{
			name: "PlatformCritical routes to platform_critical",
			kind: PlatformCritical,
			want: "revelara:platform_critical",
		},
		{
			name: "CustomerQuarantine also routes via platform_critical",
			kind: CustomerQuarantine,
			want: "revelara:platform_critical",
		},
		{
			name: "IntegrationBreak routes to integration_break",
			kind: IntegrationBreak,
			want: "revelara:integration_break",
		},
		{
			name: "CustomerPatch routes to customer_patch_review",
			kind: CustomerPatch,
			want: "customer:patch_review",
		},
		{
			name: "CustomerEligible routes to customer_eligibility_question",
			kind: CustomerEligible,
			want: "customer:eligibility_question",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := FailEvent{Kind: tt.kind, RunID: "run-test"}
			got := event.Route()
			if got != tt.want {
				t.Errorf("Route() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("invalid kind returns empty string", func(t *testing.T) {
		event := FailEvent{Kind: FailureKind("bogus"), RunID: "run-test"}
		if event.Route() != "" {
			t.Errorf("Route() for bogus Kind = %q, want empty string", event.Route())
		}
	})
}

func TestFailureKindRoute(t *testing.T) {
	tests := map[FailureKind]string{
		HarnessFailure:     "revelara:harness_failure",
		PlatformCritical:   "revelara:platform_critical",
		CustomerQuarantine: "revelara:platform_critical",
		IntegrationBreak:   "revelara:integration_break",
		CustomerPatch:      "customer:patch_review",
		CustomerEligible:   "customer:eligibility_question",
	}
	for kind, want := range tests {
		got := kind.Route()
		if got != want {
			t.Errorf("FailureKind(%q).Route() = %q, want %q", kind, got, want)
		}
	}
}

func TestEmptyKindRoutesToEmptyString(t *testing.T) {
	got := FailureKind("").Route()
	if got != "" {
		t.Errorf("empty Kind.Route() = %q, want empty string", got)
	}
}

package plan

import (
	"slices"
	"testing"

	"tide/internal/i18n"
	"tide/internal/release"
	"tide/internal/settings"
)

func TestEnforcedAnomalies(t *testing.T) {
	anomalies := []release.Anomaly{
		{Code: "short_soak", Message: "在 uat 只验证了 1m0s（要求至少 30 分钟）"},
		{Code: "multi_version_jump", Message: "跨 4 个版本"},
		{Code: "rollback", Message: "版本回退"},
	}
	policy := settings.ReleasePolicy{SoakEnforced: []string{"tier:production"}, VersionJumpEnforced: []string{"uat"}}
	tests := []struct {
		env, tier string
		want      []string
	}{
		{"prod", "production", []string{"在 uat 只验证了 1m0s（要求至少 30 分钟）"}},
		{"uat", "staging", []string{"跨 4 个版本"}},
		{"qa", "testing", nil},
	}
	for _, tt := range tests {
		if got := EnforcedAnomalies(i18n.Default, policy, tt.env, tt.tier, anomalies); !slices.Equal(got, tt.want) {
			t.Fatalf("%s: got %q, want %q", tt.env, got, tt.want)
		}
	}
	if got := EnforcedAnomalies(i18n.Default, settings.ReleasePolicy{}, "prod", "production", anomalies); got != nil {
		t.Fatalf("nothing enforced by default: %q", got)
	}
}

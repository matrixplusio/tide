package ci

import (
	"errors"
	"strings"
	"testing"
	"time"

	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/store/pg"
)

// A pipeline cannot answer "why are you releasing this", so the policy that
// demands an answer must refuse the intake rather than have Tide invent one.
func TestCheckPolicyRefusesWhatCIDidNotSupply(t *testing.T) {
	env := settings.Environment{Name: "prod", Tier: "production"}
	policy := settings.ReleasePolicy{
		JiraRequired:   []string{"tier:production"},
		ReasonRequired: []string{"*"},
		JiraProjects:   []string{"OPS"},
	}
	full := pg.CIIntake{Env: "prod", JiraTicket: "OPS-1", Reason: "roll out the fix"}

	tests := []struct {
		name string
		in   pg.CIIntake
		want string
	}{
		{"complete", full, ""},
		{"no ticket", withJira(full, ""), "Jira"},
		{"malformed ticket", withJira(full, "ops1"), "Jira"},
		{"ticket from another project", withJira(full, "APP-1"), "APP"},
		{"no reason", withReason(full, ""), "prod"},
	}
	for _, tt := range tests {
		err := checkPolicy(policy, env, tt.in)
		switch {
		case tt.want == "" && err != nil:
			t.Fatalf("%s: %v", tt.name, err)
		case tt.want != "" && err == nil:
			t.Fatalf("%s: accepted", tt.name)
		case tt.want != "" && !strings.Contains(err.Error(), tt.want):
			t.Fatalf("%s: %q does not mention %q", tt.name, err, tt.want)
		}
	}
}

// A change freeze is about the environment, not about who is releasing: a
// pipeline must not be a way around it.
func TestCheckPolicyHonoursAFreeze(t *testing.T) {
	now := time.Now()
	policy := settings.ReleasePolicy{Freezes: []settings.Freeze{
		{Name: "year end", Envs: []string{"prod"}, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)},
	}}
	err := checkPolicy(policy, settings.Environment{Name: "prod", Tier: "production"},
		pg.CIIntake{Env: "prod", Reason: "anything"})
	var fe *release.FrozenError
	if err == nil || !errors.As(err, &fe) {
		t.Fatalf("a frozen environment accepted a CI release: %v", err)
	}
	if fe.Name != "year end" {
		t.Fatalf("got freeze %q", fe.Name)
	}
}

// An environment nobody froze and nothing requires takes the intake as it is.
func TestCheckPolicyAllowsADefaultEnvironment(t *testing.T) {
	if err := checkPolicy(settings.ReleasePolicy{}, settings.Environment{Name: "dev", Tier: "testing"},
		pg.CIIntake{Env: "dev"}); err != nil {
		t.Fatalf("plain dev release refused: %v", err)
	}
}

func withJira(in pg.CIIntake, v string) pg.CIIntake   { in.JiraTicket = v; return in }
func withReason(in pg.CIIntake, v string) pg.CIIntake { in.Reason = v; return in }

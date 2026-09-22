package settings

import (
	"testing"

	"tide/internal/release"
)

func TestApprovalForPicksMostSpecific(t *testing.T) {
	rule := func(name string) release.ApprovalRule { return release.ApprovalRule{Name: name} }
	p := ReleasePolicy{Approvals: []ApprovalPolicy{
		{Envs: []string{"tier:production"}, ApprovalRule: rule("prod")},
		{Envs: []string{"prod"}, Types: []string{"frontend"}, ApprovalRule: rule("prod frontend")},
		{Envs: []string{"prod"}, Projects: []string{"acme"}, ApprovalRule: rule("acme prod")},
		{Envs: []string{"prod"}, Projects: []string{"acme"}, Types: []string{"frontend"}, ApprovalRule: rule("acme prod frontend")},
		{Envs: []string{"uat"}, Projects: []string{"globex"}, ApprovalRule: rule("globex uat")},
	}}
	for _, tc := range []struct{ env, tier, project, typ, want string }{
		{"prod", "production", "acme", "frontend", "acme prod frontend"},
		{"prod", "production", "acme", "backend", "acme prod"},
		{"prod", "production", "globex", "frontend", "prod frontend"},
		{"prod", "production", "globex", "backend", "prod"},
		{"prod", "production", "", "", "prod"},
		{"uat", "staging", "globex", "backend", "globex uat"},
		{"uat", "staging", "acme", "backend", ""},
	} {
		got := ""
		if r := p.ApprovalFor(tc.env, tc.tier, tc.project, tc.typ); r != nil {
			got = r.Name
		}
		if got != tc.want {
			t.Errorf("%s %s/%s: got %q, want %q", tc.env, tc.project, tc.typ, got, tc.want)
		}
	}
}

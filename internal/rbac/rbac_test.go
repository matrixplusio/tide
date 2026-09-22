package rbac

import (
	"slices"
	"testing"
)

func testPolicy() *Policy {
	return &Policy{
		Roles: map[string]Role{
			RoleAdmin:    {ID: RoleAdmin, Permissions: []string{AllPermissions}},
			RoleOperator: {ID: RoleOperator, Permissions: []string{"services.view", "releases.view", "pods.view", "releases.create"}},
			RoleViewer:   {ID: RoleViewer, Permissions: []string{"services.view"}},
			"prod-lead":  {ID: "prod-lead", Permissions: []string{"releases.create", "releases.cancel_any"}},
		},
		Bindings: []Binding{
			{ID: 1, RoleID: RoleViewer, Subject: SubjectAll, Envs: []string{EnvAll}},
			{ID: 2, RoleID: RoleOperator, Subject: "group:backend", Envs: []string{"tier:development", "tier:testing"}},
			{ID: 3, RoleID: "prod-lead", Subject: "user:alice", Envs: []string{"prod"}},
			{ID: 4, RoleID: RoleAdmin, Subject: "user:root", Envs: []string{EnvAll}},
		},
	}
}

var envs = []Env{{"dev", TierDevelopment}, {"qa", TierTesting}, {"uat", TierStaging}, {"prod", TierProduction}}

func TestGrants(t *testing.T) {
	p := testPolicy()
	tests := []struct {
		name    string
		subject Subject
		perm    Permission
		env     string // "" = global check
		want    bool
	}{
		{"viewing is scoped, not global", Subject{Sub: "nobody"}, ServicesView, "", false},
		{"everyone views every env", Subject{Sub: "nobody"}, ServicesView, "prod", true},
		{"everyone cannot audit", Subject{Sub: "nobody"}, AuditView, "", false},
		{"everyone cannot release", Subject{Sub: "nobody"}, ReleasesCreate, "dev", false},
		{"group by tier dev", Subject{Sub: "bob", Groups: []string{"backend"}}, ReleasesCreate, "dev", true},
		{"group by tier qa", Subject{Sub: "bob", Groups: []string{"backend"}}, PodsView, "qa", true},
		{"group not staging", Subject{Sub: "bob", Groups: []string{"backend"}}, ReleasesCreate, "uat", false},
		{"group not prod", Subject{Sub: "bob", Groups: []string{"backend"}}, ReleasesCreate, "prod", false},
		{"env perm not global", Subject{Sub: "bob", Groups: []string{"backend"}}, ReleasesCreate, "", false},
		{"user exact env", Subject{Sub: "alice"}, ReleasesCancelAny, "prod", true},
		{"user other env", Subject{Sub: "alice"}, ReleasesCancelAny, "dev", false},
		{"unknown env no tier", Subject{Sub: "bob", Groups: []string{"backend"}}, ReleasesCreate, "sandbox", false},
		{"admin global", Subject{Sub: "root"}, SettingsManage, "", true},
		{"admin any env", Subject{Sub: "root"}, ReleasesCancelAny, "sandbox", true},
		{"sub prefix no confusion", Subject{Sub: "ali"}, ReleasesCancelAny, "prod", false},
		{"group name is not sub", Subject{Sub: "backend"}, ReleasesCreate, "dev", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := p.Grants(tt.subject, envs)
			got := g.Has(tt.perm)
			if tt.env != "" {
				got = g.HasEnv(tt.perm, tt.env)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGrantsLists(t *testing.T) {
	g := testPolicy().Grants(Subject{Sub: "bob", Groups: []string{"backend"}}, envs)
	if got := g.Global(); len(got) != 0 {
		t.Fatalf("no global permissions here: %v", got)
	}
	if got := g.ForEnv("dev"); !slices.Equal(got, []Permission{ServicesView, ReleasesView, PodsView, ReleasesCreate}) {
		t.Fatalf("dev = %v", got)
	}
	// The everyone-viewer binding covers every environment; operator does not.
	if got := g.ForEnv("prod"); !slices.Equal(got, []Permission{ServicesView}) {
		t.Fatalf("prod = %v", got)
	}
	vias := map[Via]int{}
	for _, b := range g.Bindings() {
		vias[b.Via]++
	}
	if vias[ViaAll] != 1 || vias[ViaGroup] != 1 || vias[ViaUser] != 0 {
		t.Fatalf("vias = %v", vias)
	}
}

func TestCatalogKeysUnique(t *testing.T) {
	seen := map[Permission]bool{}
	for _, d := range Catalog {
		if seen[d.Key] {
			t.Fatalf("duplicate %s", d.Key)
		}
		seen[d.Key] = true
	}
}

func TestHasTarget(t *testing.T) {
	p := &Policy{
		Roles: map[string]Role{
			RoleOperator: {ID: RoleOperator, Permissions: []string{"releases.create", "pods.view"}},
		},
		Bindings: []Binding{
			{ID: 1, RoleID: RoleOperator, Subject: "group:acme-backend", Envs: []string{"tier:development", "tier:testing", "uat"}, Projects: []string{"acme"}, Types: []string{"backend"}},
			{ID: 2, RoleID: RoleOperator, Subject: "user:ops", Envs: []string{EnvAll}},
			{ID: 3, RoleID: RoleOperator, Subject: "group:globex", Envs: []string{"qa"}, Projects: []string{"globex", "gateway"}},
		},
	}
	dev := Subject{Sub: "dev", Groups: []string{"acme-backend"}}
	globex := Subject{Sub: "s", Groups: []string{"globex"}}
	for name, tc := range map[string]struct {
		s    Subject
		t    Target
		want bool
	}{
		"project + type + env":       {dev, Target{"uat", "acme", "backend"}, true},
		"other type":                 {dev, Target{"uat", "acme", "frontend"}, false},
		"other project":              {dev, Target{"qa", "globex", "backend"}, false},
		"env outside":                {dev, Target{"prod", "acme", "backend"}, false},
		"unknown project restricted": {dev, Target{"qa", "", "backend"}, false},
		"unscoped binding":           {Subject{Sub: "ops"}, Target{"prod", "", ""}, true},
		"project list, any type":     {globex, Target{"qa", "gateway", "frontend"}, true},
		"project list other env":     {globex, Target{"uat", "gateway", "frontend"}, false},
	} {
		if got := p.Grants(tc.s, envs).HasTarget(ReleasesCreate, tc.t); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
	sg := p.Grants(dev, envs).ScopedGrants(envs)
	if len(sg) != 1 || !slices.Equal(sg[0].Envs, []string{"dev", "qa", "uat"}) || !slices.Equal(sg[0].Projects, []string{"acme"}) {
		t.Fatalf("scoped grants %+v", sg)
	}
}

func TestAnyScopeAndUnrestricted(t *testing.T) {
	p := &Policy{
		Roles: map[string]Role{RoleViewer: {ID: RoleViewer, Permissions: []string{"services.view", "audit.view"}}},
		Bindings: []Binding{
			{ID: 1, RoleID: RoleViewer, Subject: "user:scoped", Envs: []string{EnvAll}, Projects: []string{"acme"}},
			{ID: 2, RoleID: RoleViewer, Subject: "user:wide", Envs: []string{EnvAll}},
			{ID: 3, RoleID: RoleViewer, Subject: "user:envonly", Envs: []string{"prod"}},
		},
	}
	for name, tc := range map[string]struct {
		sub               string
		anyScope, unbound bool
	}{
		"limited to a project": {"scoped", true, false},
		"no limits at all":     {"wide", true, true},
		"limited to one env":   {"envonly", true, false},
		"nothing granted":      {"stranger", false, false},
	} {
		g := p.Grants(Subject{Sub: tc.sub}, envs)
		if got := g.HasAnyTarget(AuditView); got != tc.anyScope {
			t.Errorf("%s: HasAnyTarget = %v", name, got)
		}
		if got := g.Unrestricted(AuditView); got != tc.unbound {
			t.Errorf("%s: Unrestricted = %v", name, got)
		}
	}
}

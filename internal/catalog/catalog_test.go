package catalog

import (
	"tide/internal/i18n"

	"encoding/json"
	"slices"
	"testing"

	"tide/internal/settings"
	"tide/internal/upstream/argocd"
)

func app(t *testing.T, raw string) argocd.Application {
	t.Helper()
	var a argocd.Application
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFromAppDefaultsAndDimensions(t *testing.T) {
	c := &Clients{Name: "local", Envs: []string{"dev", "prod"}}
	cat := settings.DefaultCatalog()
	cat.Dimensions = []settings.Dimension{
		{Key: "role", Name: "类型", Label: "example.com/role"},
		{Key: "tier", Name: "级别", Label: "example.com/tier"},
	}

	// Name-based defaults: env from the app name suffix, domain from namespace.
	a := app(t, `{"metadata":{"name":"user-api-dev","labels":{"example.com/role":"backend"}},
		"spec":{"destination":{"namespace":"acme-dev"}}}`)
	d, ok := fromApp(a, c, cat)
	if !ok || d.Env != "dev" || d.Service != "user-api" || d.Domain != "acme" || d.Project != "" {
		t.Fatalf("defaults: ok=%v %+v", ok, d.Deployment)
	}
	if d.dimensions["role"] != "backend" || d.dimensions["tier"] != "" {
		t.Fatalf("dimensions: %v", d.dimensions)
	}

	// Labels win over names when configured.
	a = app(t, `{"metadata":{"name":"whatever","labels":{"tide.io/env":"prod","tide.io/service":"gateway","tide.io/domain":"platform","tide.io/project":"acme","example.com/tier":"A"}},
		"spec":{"destination":{"namespace":"ns"}}}`)
	d, ok = fromApp(a, c, cat)
	if !ok || d.Env != "prod" || d.Service != "gateway" || d.Domain != "platform" || d.Project != "acme" || d.dimensions["tier"] != "A" {
		t.Fatalf("labels: ok=%v %+v %v", ok, d.Deployment, d.dimensions)
	}

	// Without a project label the Kargo project stands in.
	a = app(t, `{"metadata":{"name":"cart-dev","annotations":{"kargo.akuity.io/authorized-stage":"acme-pipeline:dev"}},"spec":{"destination":{"namespace":"acme-dev"}}}`)
	if d, ok = fromApp(a, c, cat); !ok || d.Project != "acme-pipeline" {
		t.Fatalf("kargo project fallback: %+v", d.Deployment)
	}

	// Apps outside the configured environments are ignored.
	if _, ok := fromApp(app(t, `{"metadata":{"name":"argocd-server"},"spec":{"destination":{"namespace":"argocd"}}}`), c, cat); ok {
		t.Fatal("platform app must be ignored")
	}
}

// Where the AppProject name already carries what Tide wants — "acme-dev" is a
// business line and an environment — reading it is one setting against a label
// on every Application.
func TestDimensionsFromArgoProject(t *testing.T) {
	c := &Clients{Name: "local", Envs: []string{"dev", "qa", "uat", "prod"}}
	cat := settings.DefaultCatalog()
	cat.ProjectLabel = FromArgoProject
	cat.EnvLabel = FromArgoProject

	// The Application carries no labels at all; everything comes from
	// spec.project and the names.
	a := app(t, `{"metadata":{"name":"order-api-dev"},
		"spec":{"project":"acme-dev","destination":{"namespace":"acme-checkout-dev"}}}`)
	d, ok := fromApp(a, c, cat)
	if !ok {
		t.Fatal("app should be in scope")
	}
	if d.Project != "acme" {
		t.Errorf("project = %q, want acme (from AppProject acme-dev)", d.Project)
	}
	if d.Env != "dev" {
		t.Errorf("env = %q, want dev (from AppProject acme-dev)", d.Env)
	}
	// The domain still comes from the namespace, which is the other half of
	// the grouping and is not in the AppProject name.
	if d.Domain != "acme-checkout" {
		t.Errorf("domain = %q, want acme-checkout", d.Domain)
	}
	if d.Service != "order-api" {
		t.Errorf("service = %q", d.Service)
	}

	// An AppProject with no environment suffix is a project name on its own;
	// it must not be truncated, and it cannot supply an environment.
	line, env := argoProjectValue("infra", c.Envs)
	if line != "infra" || env != "" {
		t.Errorf("argoProjectValue(infra) = %q, %q; want infra, \"\"", line, env)
	}
	// A suffix that is not one of the configured environments is part of the
	// name: "acme-shared" is not "acme" in environment "shared".
	line, env = argoProjectValue("acme-shared", c.Envs)
	if line != "acme-shared" || env != "" {
		t.Errorf("argoProjectValue(acme-shared) = %q, %q; want the whole name", line, env)
	}

	// Such an app has no environment to place it in, so it drops out of the
	// catalog rather than landing somewhere arbitrary.
	a = app(t, `{"metadata":{"name":"cert-manager"},"spec":{"project":"infra","destination":{"namespace":"cert-manager"}}}`)
	if _, ok := fromApp(a, c, cat); ok {
		t.Error("an app whose AppProject names no environment must be ignored")
	}

	// A label still wins where both are configured: the source is per
	// dimension, not global.
	cat.ProjectLabel = "tide.io/project"
	a = app(t, `{"metadata":{"name":"svc-qa","labels":{"tide.io/project":"explicit"}},
		"spec":{"project":"acme-qa","destination":{"namespace":"acme-qa"}}}`)
	if d, ok := fromApp(a, c, cat); !ok || d.Project != "explicit" || d.Env != "qa" {
		t.Errorf("label should win for project, AppProject still for env: %+v", d.Deployment)
	}
}

func TestMergeServicesConflicts(t *testing.T) {
	dep := func(service, env, project, app, upstream string) rawDeployment {
		return rawDeployment{Deployment: Deployment{Service: service, Env: env, Project: project, App: app, Upstream: upstream}, dimensions: map[string]string{}}
	}
	deps := []rawDeployment{
		dep("api-server", "qa", "globex", "globex-api-server-qa", "local"),
		dep("api-server", "dev", "acme", "acme-api-server-dev", "local"),
		dep("api-server", "dev", "globex", "globex-api-server-dev", "local"),
		dep("api-gateway", "dev", "globex", "globex-api-gateway-dev", "local"),
		dep("api-gateway", "qa", "globex", "globex-api-gateway-qa", "local"),
		dep("pc-frontend", "qa", "globex", "pc-frontend-qa-b", "gcp"),
		dep("pc-frontend", "qa", "globex", "pc-frontend-qa-a", "local"),
		dep("legacy", "dev", "", "legacy-dev", "local"),
		dep("legacy", "qa", "globex", "legacy-qa", "local"),
	}
	byName := func(list []Service) map[string]Service {
		m := map[string]Service{}
		for _, s := range list {
			m[s.Name] = s
		}
		return m
	}
	got := byName(mergeServices(deps))

	if s := got["api-gateway"]; len(s.Conflicts) != 0 || len(s.Envs) != 2 || s.Project != "globex" {
		t.Fatalf("api-gateway: %+v", s)
	}
	// The names inside are latin, so they are joined with a plain comma
	// whatever language the sentence around them is in.
	if s := got["api-server"]; len(s.Conflicts) != 2 ||
		s.Conflicts[0] != i18n.T(i18n.Default, "c.nameInManyProjects", "api-server", "acme, globex") ||
		s.Conflicts[1] != i18n.T(i18n.Default, "c.manyApps", "api-server", "dev", "acme-api-server-dev（local）, globex-api-server-dev（local）") {
		t.Fatalf("api-server conflicts: %q", s.Conflicts)
	}
	if s := got["pc-frontend"]; len(s.Conflicts) != 1 || s.Envs["qa"].App != "pc-frontend-qa-b" {
		// sorted by upstream then app: gcp before local
		t.Fatalf("pc-frontend: %q %s", s.Conflicts, s.Envs["qa"].App)
	}
	// A missing project on one environment is not a conflict.
	if s := got["legacy"]; len(s.Conflicts) != 0 || s.Project != "globex" {
		t.Fatalf("legacy: %+v", s)
	}

	// Input order must not change the result.
	slices.Reverse(deps)
	again := byName(mergeServices(deps))
	for name, s := range got {
		if !slices.Equal(s.Conflicts, again[name].Conflicts) || s.Project != again[name].Project {
			t.Fatalf("%s depends on input order: %q vs %q", name, s.Conflicts, again[name].Conflicts)
		}
		for e, d := range s.Envs {
			if again[name].Envs[e].App != d.App {
				t.Fatalf("%s/%s picked %s vs %s", name, e, d.App, again[name].Envs[e].App)
			}
		}
	}
}

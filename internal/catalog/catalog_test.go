package catalog

import (
	"tide/internal/i18n"

	"encoding/json"
	"slices"
	"testing"
	"time"

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

	// A template that rendered with an empty segment leaves an annotation that
	// names no project. Reading it as one produces a Kargo lookup that can
	// only fail, and the service then reports the wrong reason for it.
	for name, annotation := range map[string]string{
		"empty service segment": "-pipeline:dev",
		"empty project":         ":dev",
		"empty stage":           "acme-pipeline:",
		"trailing dash":         "acme-:dev",
	} {
		a := app(t, `{"metadata":{"name":"svc-dev","annotations":{"kargo.akuity.io/authorized-stage":"`+annotation+`"}},
			"spec":{"destination":{"namespace":"acme-dev"}}}`)
		d, ok := fromApp(a, c, cat)
		if !ok {
			t.Fatalf("%s: the app itself is still in scope", name)
		}
		if d.KargoProject != "" || d.KargoStage != "" {
			t.Errorf("%s: %q gave project=%q stage=%q, want both empty",
				name, annotation, d.KargoProject, d.KargoStage)
		}
	}
	// A well-formed one still works.
	a = app(t, `{"metadata":{"name":"svc2-dev","annotations":{"kargo.akuity.io/authorized-stage":"acme-pipeline:dev"}},
		"spec":{"destination":{"namespace":"acme-dev"}}}`)
	if d, ok := fromApp(a, c, cat); !ok || d.KargoProject != "acme-pipeline" || d.KargoStage != "dev" {
		t.Errorf("well-formed annotation: %+v", d.Deployment)
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

// The incident this exists to prevent: an environment label that no
// Application carries. Every Application is dropped, and without the
// accounting the page says "no services yet" about a few hundred of them.
// What separates a misconfiguration from an empty upstream is that NoEnv,
// not OtherEnv, accounts for all of them.
func TestClassifyExplainsWhyEverythingWasDropped(t *testing.T) {
	c := &Clients{Name: "local", Envs: []string{"dev", "qa"}}
	cat := settings.DefaultCatalog()
	cat.EnvLabel = "tide.io/env" // configured, but nothing carries it

	apps := []argocd.Application{
		// No env label, and no name suffix or Kargo stage to fall back on.
		app(t, `{"metadata":{"name":"order-api"},"spec":{"destination":{"namespace":"acme"}}}`),
		app(t, `{"metadata":{"name":"cart-api"},"spec":{"destination":{"namespace":"acme"}}}`),
		// Resolves to an environment this upstream does not serve. Ordinary,
		// and must not be counted as the misconfiguration.
		app(t, `{"metadata":{"name":"gateway","labels":{"tide.io/env":"prod"}},"spec":{"destination":{"namespace":"platform"}}}`),
	}

	deps, _, stats := classify(apps, c, cat)
	if len(deps) != 0 {
		t.Fatalf("expected everything to be dropped, kept %d", len(deps))
	}
	if stats.Applications != 3 || stats.Kept != 0 || stats.NoEnv != 2 || stats.OtherEnv != 1 {
		t.Fatalf("stats do not explain the drop: %+v", stats)
	}

	// The same Applications with the label Tide was actually told to read.
	cat.EnvLabel = ""
	apps[0] = app(t, `{"metadata":{"name":"order-api-dev"},"spec":{"destination":{"namespace":"acme-dev"}},
		"status":{"resources":[{"kind":"Deployment","name":"order-api"}]}}`)
	deps, _, stats = classify(apps, c, cat)
	if len(deps) != 1 || stats.Kept != 1 || stats.Applications != 3 {
		t.Fatalf("expected one of three to be kept: %d deps, %+v", len(deps), stats)
	}
}

// The Application that only creates a namespace is labelled exactly like the
// services beside it and is not one: it has no image, so it can never be
// promoted, and every column the service list shows about it is blank. It is
// dropped, and counted, so that "the list got shorter" has an answer.
func TestClassifyDropsApplicationsThatRunNothing(t *testing.T) {
	c := &Clients{Name: "local", Envs: []string{"dev"}}
	cat := settings.DefaultCatalog()

	apps := []argocd.Application{
		app(t, `{"metadata":{"name":"order-api-dev"},"spec":{"destination":{"namespace":"acme-dev"}},
			"status":{"resources":[{"kind":"Deployment","name":"order-api"}]}}`),
		// Namespace, quota and RBAC only — the shape a "-ns" layer has.
		app(t, `{"metadata":{"name":"acme-dev-ns","labels":{"tide.io/env":"dev"}},"spec":{"destination":{"namespace":"acme-dev"}},
			"status":{"resources":[{"kind":"Namespace","name":"acme-dev"},{"kind":"ResourceQuota","name":"acme-dev"},{"kind":"RoleBinding","name":"devs"}]}}`),
	}

	deps, _, stats := classify(apps, c, cat)
	if len(deps) != 1 || deps[0].Service != "order-api" {
		t.Fatalf("expected only the workload: %+v", deps)
	}
	if stats.Kept != 1 || stats.NoWorkload != 1 || stats.Applications != 2 {
		t.Fatalf("stats must account for the dropped layer: %+v", stats)
	}
	// Not one of the existing buckets: it resolved to an environment this
	// upstream serves, so blaming the labels would send a reader to the wrong
	// setting.
	if stats.NoEnv != 0 || stats.OtherEnv != 0 {
		t.Fatalf("dropped for the wrong reason: %+v", stats)
	}
}

// Recent exists so a question about a service that does not change — which
// project it is in — cannot end up waiting on a fan-out across every
// upstream. It must therefore never build, and must refuse to answer at all
// rather than answer from something too old.
func TestRecentAnswersFromCacheOrNotAtAll(t *testing.T) {
	h := &Hub{}
	if got := h.Recent(time.Hour); got != nil {
		t.Fatalf("nothing built yet, so there is nothing to return: %+v", got)
	}

	h.snap = &Snapshot{At: time.Now().Add(-time.Minute)}
	if got := h.Recent(5 * time.Minute); got != h.snap {
		t.Fatalf("a snapshot inside the bound is the answer: %+v", got)
	}
	if got := h.Recent(30 * time.Second); got != nil {
		t.Fatalf("past the bound the caller must go and ask properly: %+v", got)
	}
}

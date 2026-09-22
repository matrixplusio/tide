package kargogen

import (
	"strings"
	"testing"

	"tide/internal/catalog"
	"tide/internal/settings"
)

func envs(names ...string) settings.Environments {
	var e settings.Environments
	for _, n := range names {
		e.Items = append(e.Items, settings.Environment{Name: n})
	}
	return e
}

func dep(env string) *catalog.Deployment {
	return &catalog.Deployment{
		Env: env, App: "order-api-" + env, Workload: true,
		Image:    "registry.example.com/acme/order-api",
		Repo:     "https://git.example.com/acme/k8s-apps.git",
		RepoPath: "acme/trade/order-api/" + env,
		Revision: "main",
	}
}

func svc(name, domain string, ds ...*catalog.Deployment) catalog.Service {
	s := catalog.Service{Name: name, Domain: domain, Envs: map[string]*catalog.Deployment{}}
	for _, d := range ds {
		s.Envs[d.Env] = d
	}
	return s
}

func find(t *testing.T, r Result, path string) string {
	t.Helper()
	for _, f := range r.Files() {
		if f.Path == path {
			return f.YAML
		}
	}
	t.Fatalf("no %s in %d files", path, r.FileCount)
	return ""
}

// The promotion chain is the whole point of the generated stages, and only
// the first environment may take freight straight from the warehouse: a
// pipeline's newly built image can reach a direct stage and no other. Wiring
// this wrong is silent — the stages exist, they just never receive anything.
func TestChainsStagesInEnvironmentOrder(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{
		svc("order-api", "trade", dep("dev"), dep("qa"), dep("prod")),
	}}
	r := Generate(snap, envs("dev", "qa", "uat", "prod"), Options{})

	stages := find(t, r, "trade/stages.yaml")
	for _, want := range []string{
		"name: order-api-dev",
		"direct: true",
		"name: order-api-qa",
		"- order-api-dev",
		"name: order-api-prod",
		// uat has no deployment, so prod chains from qa, not from a stage
		// that was never generated.
		"- order-api-qa",
	} {
		if !strings.Contains(stages, want) {
			t.Errorf("stages do not contain %q", want)
		}
	}
	if strings.Contains(stages, "order-api-uat") {
		t.Error("generated a stage for an environment the service is not deployed to")
	}
	if n := strings.Count(stages, "direct: true"); n != 1 {
		t.Errorf("%d stages take freight directly; exactly one may", n)
	}
	if r.Stages != 3 || r.Warehouses != 1 || r.Services != 1 || len(r.Domains) != 1 {
		t.Errorf("counts: %+v", r)
	}
	// The grouping is what the page reviews by, so the per-domain numbers
	// have to add up on their own, not only in the totals.
	if d := r.Domains[0]; d.Name != "trade" || d.Stages != 3 || d.Warehouses != 1 || len(d.Files) != 4 {
		t.Errorf("domain: %+v", d)
	}
}

// One promotion task per project, not per stage: a domain with 79 services
// would otherwise carry 79 identical copies of the same five steps.
func TestOneSharedPromotionTaskPerDomain(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{
		svc("order-api", "trade", dep("dev")),
		svc("cart-api", "trade", dep("dev")),
		svc("bi-report", "analytics", dep("dev")),
	}}
	r := Generate(snap, envs("dev"), Options{})

	if len(r.Domains) != 2 {
		t.Fatalf("expected two projects, got %d", len(r.Domains))
	}
	// Sorted, so two runs over the same catalog read the same way.
	if r.Domains[0].Name != "analytics" || r.Domains[1].Name != "trade" {
		t.Errorf("domains out of order: %v, %v", r.Domains[0].Name, r.Domains[1].Name)
	}
	for _, domain := range []string{"trade", "analytics"} {
		task := find(t, r, domain+"/promotion-task.yaml")
		if n := strings.Count(task, "kind: PromotionTask"); n != 1 {
			t.Errorf("%s: %d tasks, want 1", domain, n)
		}
		if !strings.Contains(task, "namespace: "+domain) {
			t.Errorf("%s: task is not in its own project", domain)
		}
	}
	// Both services reference the shared task rather than inlining steps.
	stages := find(t, r, "trade/stages.yaml")
	if n := strings.Count(stages, "name: promote-image"); n != 2 {
		t.Errorf("%d references to the shared task, want 2", n)
	}
	if strings.Contains(stages, "uses: git-clone") {
		t.Error("promotion steps were inlined into a stage instead of shared")
	}
}

// Anything skipped has to be named. A generator that quietly drops a service
// leaves somebody with a release process missing exactly one thing, and no
// way to find out which — which is how "151 warehouses" passes for "166".
func TestSkippedServicesAreReportedNotDropped(t *testing.T) {
	noWorkload := dep("dev")
	noWorkload.Workload = false // a namespace-only Application
	noImage := dep("dev")
	noImage.Image = ""
	multiSource := dep("dev")
	multiSource.RepoPath = "" // several sources, no single place to write

	snap := &catalog.Snapshot{Services: []catalog.Service{
		svc("namespace-scaffolding", "trade", noWorkload),
		svc("no-image", "trade", noImage),
		svc("many-sources", "trade", multiSource),
		svc("homeless", "", dep("dev")),
		svc("order-api", "trade", dep("dev")),
	}}
	r := Generate(snap, envs("dev"), Options{})

	if r.Services != 1 || r.Warehouses != 1 {
		t.Errorf("only order-api can be generated: %+v", r)
	}
	if len(r.Skipped) != 4 {
		t.Fatalf("expected four explanations, got %d: %+v", len(r.Skipped), r.Skipped)
	}
	for _, s := range r.Skipped {
		if s.Reason == "" {
			t.Errorf("%s was skipped without saying why", s.Service)
		}
	}
}

// Limiting to one domain must not change what that domain produces.
func TestDomainFilter(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{
		svc("order-api", "trade", dep("dev")),
		svc("bi-report", "analytics", dep("dev")),
	}}
	all := Generate(snap, envs("dev"), Options{})
	one := Generate(snap, envs("dev"), Options{Domain: "trade"})

	if len(one.Domains) != 1 || one.Services != 1 {
		t.Fatalf("filter: %+v", one)
	}
	if find(t, one, "trade/stages.yaml") != find(t, all, "trade/stages.yaml") {
		t.Error("filtering changed what the domain generates")
	}
}

// Every stage carries the labels promotion policies select on; without them
// `stageSelector` has nothing to match and policies have to name stages one
// by one.
func TestStagesCarrySelectableLabels(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{svc("order-api", "trade", dep("dev"))}}
	r := Generate(snap, envs("dev"), Options{ServiceLabel: "acme.io/service", EnvLabel: "acme.io/env"})
	stages := find(t, r, "trade/stages.yaml")
	for _, want := range []string{"acme.io/service: order-api", "acme.io/env: dev"} {
		if !strings.Contains(stages, want) {
			t.Errorf("missing label %q", want)
		}
	}
}

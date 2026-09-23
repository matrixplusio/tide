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

// Kargo's own default is SemVer, and against tags that are not semantic
// versions it discovers nothing — every warehouse reports
// "MissingImageReferences" and looks exactly like one whose credentials are
// wrong. Saying the strategy out loud is the difference between a pipeline
// that works and one that silently finds no images at all.
func TestWarehousesStateHowImagesAreChosen(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{svc("order-api", "trade", dep("dev"))}}

	r := Generate(snap, envs("dev"), Options{})
	def := find(t, r, "trade/warehouses.yaml")
	for _, want := range []string{
		"imageSelectionStrategy: Lexical",
		// The plural form: allowTags was removed in Kargo v1.11 and a
		// warehouse still using it fails discovery outright.
		"allowTagsRegexes:",
		`- "^[0-9]"`,
	} {
		if !strings.Contains(def, want) {
			t.Errorf("default warehouse is missing %q:\n%s", want, def)
		}
	}
	// A field, not a mention of one in a comment.
	for _, line := range strings.Split(def, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "allowTags:") {
			t.Error("used the singular allowTags, which Kargo v1.11 refuses")
		}
	}

	// A different convention is configured, not assumed.
	r = Generate(snap, envs("dev"), Options{ImageStrategy: "SemVer", TagPattern: `^v\d+\.`})
	custom := find(t, r, "trade/warehouses.yaml")
	if !strings.Contains(custom, "imageSelectionStrategy: SemVer") || !strings.Contains(custom, `- "^v\\d+\\."`) {
		t.Errorf("configured selection was not used:\n%s", custom)
	}
}

// depIn is dep with the image repository spelled out: which repository an
// environment pulls from is what decides whether it can be promoted into.
func depIn(env, image string) *catalog.Deployment {
	d := dep(env)
	d.Image = image
	return d
}

// A promotion moves a digest, not a build: Kargo writes the same tag into the
// next environment's manifests, and that tag has to exist in the repository
// that environment pulls from. So an environment whose image comes from a
// different repository than the one before it cannot be promoted into at all
// — it can only take its own builds, straight from a warehouse of its own.
//
// Which is not a corner case: a project that builds once per environment (one
// artifact line per branch) has a different repository for every one of them,
// and chaining those stages produces a pipeline that passes every validation
// and then fails at the first promotion, looking for a tag that was never
// pushed there.
func TestEachArtifactLineGetsItsOwnWarehouse(t *testing.T) {
	const reg = "registry.example.com/"
	snap := &catalog.Snapshot{Services: []catalog.Service{
		svc("order-api", "trade",
			depIn("dev", reg+"acme-dev/order-api"),
			depIn("qa", reg+"acme-qa/order-api"),
			depIn("uat", reg+"acme/order-api"),
			depIn("prod", reg+"acme/order-api"), // the same line as uat
		),
	}}
	r := Generate(snap, envs("dev", "qa", "uat", "prod"), Options{})

	stages := find(t, r, "trade/stages.yaml")
	// Three lines, so three stages take freight directly; only prod, which
	// pulls from the repository uat was verified in, is promoted.
	if n := strings.Count(stages, "direct: true"); n != 3 {
		t.Errorf("%d stages take freight directly, want 3\n%s", n, stages)
	}
	if !strings.Contains(stages, "- order-api-uat") {
		t.Error("prod shares uat's repository and must be promoted from it")
	}
	for _, chained := range []string{"- order-api-dev", "- order-api-qa"} {
		if strings.Contains(stages, chained) {
			t.Errorf("promoted from %q across a repository boundary", chained)
		}
	}

	wh := find(t, r, "trade/warehouses.yaml")
	for _, want := range []string{reg + "acme-dev/order-api", reg + "acme-qa/order-api", reg + "acme/order-api"} {
		if !strings.Contains(wh, want) {
			t.Errorf("no warehouse subscribes to %q", want)
		}
	}
	if r.Warehouses != 3 {
		t.Errorf("warehouses: %d, want 3", r.Warehouses)
	}
	// Every stage must name a warehouse that was actually written.
	for _, name := range []string{"order-api", "order-api-qa", "order-api-uat"} {
		if !strings.Contains(wh, "name: "+name+"\n") {
			t.Errorf("warehouse %q is referenced but never written\n%s", name, wh)
		}
	}
}

// The single-line project — build once, promote the same digest onward — is
// the shape the generator was written for, and it must not change.
func TestOneArtifactLineStillChains(t *testing.T) {
	const img = "registry.example.com/acme/order-api"
	snap := &catalog.Snapshot{Services: []catalog.Service{
		svc("order-api", "trade", depIn("dev", img), depIn("qa", img), depIn("prod", img)),
	}}
	r := Generate(snap, envs("dev", "qa", "prod"), Options{})

	if r.Warehouses != 1 {
		t.Errorf("warehouses: %d, want 1", r.Warehouses)
	}
	stages := find(t, r, "trade/stages.yaml")
	if n := strings.Count(stages, "direct: true"); n != 1 {
		t.Errorf("%d stages take freight directly, want 1", n)
	}
	for _, want := range []string{"- order-api-dev", "- order-api-qa"} {
		if !strings.Contains(stages, want) {
			t.Errorf("stages do not contain %q", want)
		}
	}
}

// svcIn is svc with the business line spelled out.
func svcIn(name, project, domain string, ds ...*catalog.Deployment) catalog.Service {
	x := svc(name, domain, ds...)
	x.Project = project
	return x
}

// A business domain can belong to two business lines at once: the same word
// names one group of services built out of one GitLab group and another group
// built out of a second. A Kargo project is a cluster-scoped namespace, so the
// two cannot share one — and putting them together would also put one line's
// promotion permissions over the other's services.
func TestOneDomainInTwoBusinessLinesBecomesTwoProjects(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{
		svcIn("cart", "acme", "shop", depIn("dev", "registry.example.com/acme-dev/cart")),
		svcIn("checkout", "acme", "shop", depIn("dev", "registry.example.com/acme-dev/checkout")),
		svcIn("storefront", "other", "shop", depIn("dev", "registry.example.com/other-dev/storefront")),
	}}
	r := Generate(snap, envs("dev"), Options{ProjectPrefix: true})

	names := map[string]int{}
	for _, d := range r.Domains {
		names[d.Name] = d.Stages
	}
	if len(names) != 2 {
		t.Fatalf("expected two Kargo projects, got %v", names)
	}
	if names["acme-shop"] != 2 || names["other-shop"] != 1 {
		t.Fatalf("services landed in the wrong project: %v", names)
	}
	// Each project is written to its own directory, so one push cannot
	// overwrite the other.
	find(t, r, "acme-shop/project.yaml")
	find(t, r, "other-shop/project.yaml")
}

// An Application that carries no project label falls back to the Kargo
// project named in its authorized-stage annotation — and that name already
// contains the domain. Appending the domain a second time invents a project
// nobody asked for, writes a directory for it, and asks Kargo to create it;
// the services inside would look plausible and be entirely separate from the
// siblings they belong with.
func TestAProjectThatAlreadyNamesTheDomainIsNotDoubled(t *testing.T) {
	for name, tc := range map[string]struct {
		project, domain, want string
	}{
		"a business line and a domain":       {"acme", "shop", "acme-shop"},
		"already the Kargo project":          {"acme-shop", "shop", "acme-shop"},
		"project and domain are one":         {"shop", "shop", "shop"},
		"a domain that merely ends the same": {"acme-workshop", "shop", "acme-workshop-shop"},
	} {
		got := projectName(catalog.Service{Project: tc.project, Domain: tc.domain}, true)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}

	// Without the prefix the domain stands alone, whatever the project says.
	if got := projectName(catalog.Service{Project: "acme", Domain: "shop"}, false); got != "shop" {
		t.Errorf("bare domain: got %q", got)
	}
}

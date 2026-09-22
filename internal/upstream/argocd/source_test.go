package argocd

import (
	"encoding/json"
	"testing"
)

// Where a service's manifests live decides where Kargo writes the image tag.
// Getting it wrong writes a tag into the wrong repository, so "I do not know"
// has to stay available — but it must not fire on shapes that do have an
// answer, or ordinary services get skipped for no reason.
func TestApplicationManifests(t *testing.T) {
	app := func(raw string) *Application {
		var a Application
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			t.Fatal(err)
		}
		return &a
	}

	// What every ordinary service looks like.
	src, ok := app(`{"spec":{"source":{"repoURL":"https://git/acme/k8s-apps.git",
		"path":"acme/trade/order-api/uat","targetRevision":"main"}}}`).Manifests()
	if !ok || src.Path != "acme/trade/order-api/uat" || src.TargetRevision != "main" {
		t.Fatalf("single source: ok=%v %+v", ok, src)
	}

	// The multi-source form in practice: one source carries the manifests,
	// the rest are values referenced by name. Still exactly one answer, so
	// skipping it would be skipping something perfectly well defined.
	src, ok = app(`{"spec":{"sources":[
		{"repoURL":"https://git/acme/k8s-platform.git","path":"charts/pipeline","targetRevision":"main"},
		{"repoURL":"https://git/acme/k8s-platform.git","ref":"platform","targetRevision":"main"}]}}`).Manifests()
	if !ok || src.Path != "charts/pipeline" {
		t.Fatalf("one manifest source plus a values ref: ok=%v %+v", ok, src)
	}

	// Genuinely ambiguous: two sources both contributing manifests. Nothing
	// says which one holds the image, and guessing writes a tag into the
	// wrong repository.
	if _, ok = app(`{"spec":{"sources":[
		{"repoURL":"https://git/a.git","path":"one"},
		{"repoURL":"https://git/b.git","path":"two"}]}}`).Manifests(); ok {
		t.Fatal("two manifest sources were resolved to one; that is a guess")
	}

	// A Helm chart from a registry counts as manifests too.
	if src, ok = app(`{"spec":{"source":{"repoURL":"https://charts.example.com",
		"chart":"order-api","targetRevision":"1.2.3"}}}`).Manifests(); !ok || src.Chart != "order-api" {
		t.Fatalf("chart source: ok=%v %+v", ok, src)
	}

	// No source at all is not an answer either.
	if _, ok = app(`{"spec":{}}`).Manifests(); ok {
		t.Fatal("an Application with no source reported one")
	}
}

// Telling a service from namespace scaffolding decides what gets a Kargo
// pipeline. The trap is judging by what is running: a service parked at zero
// replicas has no pods and therefore no images, and dropping it is silent —
// nobody notices 151 warehouses where there should be 166 until somebody
// scales one of them up and finds it has no release process.
func TestApplicationRunsWorkloads(t *testing.T) {
	app := func(kinds ...string) *Application {
		var a Application
		for _, k := range kinds {
			a.Status.Resources = append(a.Status.Resources, struct {
				Group     string `json:"group"`
				Version   string `json:"version"`
				Kind      string `json:"kind"`
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
				Status    string `json:"status"`
				Health    *struct {
					Status  string `json:"status"`
					Message string `json:"message"`
				} `json:"health"`
				RequiresPruning bool `json:"requiresPruning"`
			}{Kind: k})
		}
		return &a
	}

	cases := []struct {
		name  string
		kinds []string
		want  bool
	}{
		{"an ordinary service", []string{"Service", "Deployment", "ConfigMap"}, true},
		// The fifteen parked at zero replicas: the Deployment is there, the
		// pods are not, and summary.images would say nothing.
		{"a service scaled to zero", []string{"Service", "Deployment"}, true},
		{"a stateful service", []string{"StatefulSet", "Service"}, true},
		{"a scheduled job", []string{"CronJob"}, true},
		{"an argo rollout", []string{"Rollout", "Service"}, true},
		// The thirteen that only set a namespace up.
		{"namespace scaffolding", []string{"Namespace", "ResourceQuota", "LimitRange", "SealedSecret"}, false},
		{"configuration only", []string{"ConfigMap", "Secret"}, false},
		{"nothing at all", nil, false},
	}
	for _, c := range cases {
		if got := app(c.kinds...).RunsWorkloads(); got != c.want {
			t.Errorf("%s (%v): got %v, want %v", c.name, c.kinds, got, c.want)
		}
	}
}

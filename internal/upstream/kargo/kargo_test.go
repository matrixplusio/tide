package kargo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The route RefreshWarehouse uses is worth pinning down. The KargoService RPC
// for the same job cannot marshal its reply over Connect's JSON codec in
// v1.11.4 — it applies the refresh and then answers HTTP 500 — so this client
// deliberately uses the REST route instead, and a well-meaning change back to
// the RPC would look like it worked while logging a failure every time.
func TestRefreshWarehouseUsesTheRESTRoute(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotMethod = r.URL.Path, r.Header.Get("Authorization"), r.Method
		// Kargo answers this route with 200 and an empty body.
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "t0ken", false)
	if err := c.RefreshWarehouse(context.Background(), "acme", "order-api"); err != nil {
		t.Fatal(err)
	}

	if want := "/v1beta1/projects/acme/warehouses/order-api/refresh"; gotPath != want {
		t.Fatalf("path %q, want %q", gotPath, want)
	}
	if gotMethod != http.MethodPost || gotAuth != "Bearer t0ken" {
		t.Fatalf("method=%q auth=%q", gotMethod, gotAuth)
	}
}

// An upstream that refuses the call must say so rather than looking like a
// success: the caller logs it and falls back, and a swallowed error here
// would make a missing permission indistinguishable from everything working.
func TestRefreshWarehouseSurfacesRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","message":"warehouses.kargo.akuity.io is forbidden"}`))
	}))
	defer srv.Close()

	err := New(srv.URL, "t0ken", false).RefreshWarehouse(context.Background(), "acme", "order-api")
	if err == nil {
		t.Fatal("a refused refresh reported success")
	}
}

// Refreshing a warehouse answers 200 as soon as the annotation is written,
// so "the refresh worked" and "discovery worked" are different questions.
// This is the second one, and getting it wrong in either direction is bad:
// staying quiet about a broken warehouse leaves a release that never appears
// and no reason given, while complaining about a healthy one trains people to
// ignore the message.
func TestWarehouseDiscoveryProblem(t *testing.T) {
	cond := func(kind, status, reason, msg string) Condition {
		return Condition{Type: kind, Status: status, Reason: reason, Message: msg}
	}
	warehouse := func(cs ...Condition) *Warehouse {
		var w Warehouse
		w.Status.Conditions = cs
		return &w
	}

	cases := []struct {
		name string
		w    *Warehouse
		want string
	}{{
		name: "images deleted from the registry",
		w: warehouse(
			cond("Ready", "False", "DiscoveryFailure", "MANIFEST_UNKNOWN: manifest unknown"),
			cond("Healthy", "False", "DiscoveryFailed", ""),
		),
		want: "DiscoveryFailure: MANIFEST_UNKNOWN: manifest unknown",
	}, {
		// The first second after every refresh. Judging by Ready alone would
		// report this as broken, i.e. on every single successful nudge.
		name: "discovery still running is not a problem",
		w: warehouse(
			cond("Ready", "False", "DiscoveryInProgress", "Waiting for discovery to complete"),
			cond("Healthy", "Unknown", "Pending", ""),
			cond("Reconciling", "True", "ScheduledDiscovery", ""),
		),
		want: "",
	}, {
		name: "healthy warehouse says nothing",
		w: warehouse(
			cond("Ready", "True", "ArtifactsDiscovered", "Successfully discovered artifacts from 1 subscriptions"),
			cond("Healthy", "True", "ReconciliationSucceeded", ""),
			// Not a problem: the newest artifacts already have freight.
			cond("FreightCreated", "False", "AlreadyExists", "Freight composed of the newest artifacts already exists"),
		),
		want: "",
	}, {
		name: "never reconciled has no answer, which is not a problem",
		w:    warehouse(),
		want: "",
	}, {
		name: "reason without a message still names the cause",
		w: warehouse(
			cond("Ready", "False", "DiscoveryFailure", ""),
			cond("Healthy", "False", "DiscoveryFailed", ""),
		),
		want: "DiscoveryFailure",
	}}

	for _, c := range cases {
		if got := c.w.DiscoveryProblem(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A stage's freight sources decide whether a freshly built image can reach it
// at all. One fed by another stage never sees new freight directly — it has to
// be promoted in — so a pipeline pushing to it waits forever for something
// that was never coming. Reading this wrong in either direction is costly:
// calling a direct stage indirect refuses valid pipelines, and the reverse is
// the half-hour silence this check exists to prevent.
func TestStageWarehousesReportsHowFreightArrives(t *testing.T) {
	stage := func(raw string) *Stage {
		var s Stage
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		return &s
	}

	direct := stage(`{"spec":{"requestedFreight":[
		{"origin":{"kind":"Warehouse","name":"order-api"},"sources":{"direct":true}}]}}`)
	names, ok := direct.Warehouses()
	if !ok || len(names) != 1 || names[0] != "order-api" {
		t.Fatalf("direct stage: names=%v direct=%v", names, ok)
	}

	// What the qa stage of a dev→qa→uat chain looks like.
	fromStage := stage(`{"spec":{"requestedFreight":[
		{"origin":{"kind":"Warehouse","name":"order-api"},"sources":{"stages":["dev"]}}]}}`)
	names, ok = fromStage.Warehouses()
	if ok {
		t.Fatalf("a stage fed by another stage was reported as direct: %v", names)
	}
	// The warehouse is still named: it is the origin of the freight either
	// way, and refreshing it is still the right thing to do.
	if len(names) != 1 || names[0] != "order-api" {
		t.Fatalf("origin warehouse lost: %v", names)
	}

	// Mixed: one subscription direct, another promoted in. Anything direct
	// means a new image can land here.
	mixed := stage(`{"spec":{"requestedFreight":[
		{"origin":{"kind":"Warehouse","name":"order-api"},"sources":{"stages":["dev"]}},
		{"origin":{"kind":"Warehouse","name":"shared-config"},"sources":{"direct":true}}]}}`)
	if names, ok = mixed.Warehouses(); !ok || len(names) != 2 {
		t.Fatalf("mixed stage: names=%v direct=%v", names, ok)
	}
}

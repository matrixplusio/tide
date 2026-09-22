package catalog

import (
	"context"
	"testing"
	"time"

	"tide/internal/cache"
)

// A snapshot that goes through the shared cache must come back whole.
// Snapshot.Labels carries `json:"-"` so the services API does not return it,
// which means a naive round trip drops it and the label discovery page goes
// blank on whichever replica read from the cache.
func TestSharedSnapshotKeepsEverythingIncludingLabels(t *testing.T) {
	ctx := context.Background()
	hub := &Hub{Shared: &cache.Memory{}}
	want := &Snapshot{
		At:       time.Now().UTC().Truncate(time.Second),
		EnvOrder: []string{"dev", "qa", "uat", "prod"},
		Services: []Service{{
			Name: "order-api", Project: "acme", Domain: "platform",
			Dimensions: map[string]string{"role": "backend", "tier": "b"},
			Envs: map[string]*Deployment{"uat": {
				Service: "order-api", Env: "uat", App: "order-api-uat",
				Tag: "20260921-0009", Digest: "sha256:abc", Health: "Healthy", Sync: "Synced",
			}},
			Conflicts: []string{"two Applications for uat"},
		}},
		Upstreams: []UpstreamStatus{{Name: "gcp", Envs: []string{"uat", "prod"}, KargoOK: true, ArgoCDOK: true}},
		Labels:    map[string]map[string]int{"app.kubernetes.io/part-of": {"acme": 4}},
	}

	hub.toShared(ctx, 7, want)
	got := hub.fromShared(ctx, 7)
	if got == nil {
		t.Fatal("nothing came back out of the shared cache")
	}
	if len(got.Labels) != 1 || got.Labels["app.kubernetes.io/part-of"]["acme"] != 4 {
		t.Fatalf("labels were lost in the round trip: %v", got.Labels)
	}
	if len(got.Services) != 1 {
		t.Fatalf("got %d services", len(got.Services))
	}
	s := got.Services[0]
	switch {
	case s.Name != want.Services[0].Name || s.Project != "acme" || s.Domain != "platform":
		t.Fatalf("service identity changed: %+v", s)
	case s.Dimensions["role"] != "backend":
		t.Fatalf("dimensions lost: %v", s.Dimensions)
	case len(s.Conflicts) != 1:
		t.Fatalf("conflicts lost: %v", s.Conflicts)
	case s.Envs["uat"] == nil || s.Envs["uat"].Digest != "sha256:abc":
		t.Fatalf("deployment lost: %+v", s.Envs)
	}
	if len(got.Upstreams) != 1 || !got.Upstreams[0].KargoOK {
		t.Fatalf("upstream status lost: %+v", got.Upstreams)
	}
	if !got.At.Equal(want.At) {
		t.Fatalf("built-at changed: %v vs %v", got.At, want.At)
	}
	// Find still works on the decoded copy; callers hold the pointer it
	// returns, so it has to point into the snapshot they were given.
	svc := got.Find("order-api")
	if svc == nil || svc != &got.Services[0] {
		t.Fatal("Find does not point into the decoded snapshot")
	}
}

// A different generation is a different key, so a snapshot built before a
// change can never be served after it. That is the whole invalidation
// mechanism: no message to lose, nothing to delete.
func TestGenerationIsPartOfTheKey(t *testing.T) {
	ctx := context.Background()
	hub := &Hub{Shared: &cache.Memory{}}
	hub.toShared(ctx, 1, &Snapshot{EnvOrder: []string{"before"}})
	if got := hub.fromShared(ctx, 2); got != nil {
		t.Fatalf("generation 2 was served a snapshot built at generation 1: %v", got.EnvOrder)
	}
	if got := hub.fromShared(ctx, 1); got == nil || got.EnvOrder[0] != "before" {
		t.Fatal("generation 1 lost its own snapshot")
	}
}

// Without a shared cache everything must still work, just per replica.
func TestNoSharedCacheIsAlwaysAMiss(t *testing.T) {
	ctx := context.Background()
	hub := &Hub{}
	hub.toShared(ctx, 1, &Snapshot{}) // must not panic
	if got := hub.fromShared(ctx, 1); got != nil {
		t.Fatal("a hub without a shared cache returned something")
	}
}

// ctxAwareCache refuses writes whose context is already done, the way a real
// client does.
type ctxAwareCache struct {
	mem    cache.Memory
	writes int
}

func (c *ctxAwareCache) Name() string { return "ctx-aware" }

func (c *ctxAwareCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	return c.mem.Get(ctx, key)
}

func (c *ctxAwareCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if ctx.Err() != nil {
		return // exactly what go-redis does, and it is reported as "degraded"
	}
	c.writes++
	c.mem.Set(ctx, key, value, ttl)
}

// The build runs on a context detached from the request so a caller
// navigating away cannot cancel it. Publishing the result has to happen
// while that context is still alive: doing it afterwards writes nothing, and
// the failure looks like an ordinary degraded cache, so every replica
// quietly keeps rebuilding its own snapshot.
func TestPublishingHappensBeforeTheBuildContextIsCancelled(t *testing.T) {
	shared := &ctxAwareCache{}
	hub := &Hub{Shared: shared}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the build context has already finished
	hub.toShared(ctx, 3, &Snapshot{EnvOrder: []string{"qa"}})
	if shared.writes != 0 {
		t.Fatal("the fake cache accepted a write on a cancelled context; the test cannot prove anything")
	}

	hub.toShared(context.Background(), 3, &Snapshot{EnvOrder: []string{"qa"}})
	if shared.writes != 1 {
		t.Fatalf("a live context wrote %d times, want 1", shared.writes)
	}
	if got := hub.fromShared(context.Background(), 3); got == nil {
		t.Fatal("nothing was published")
	}
}

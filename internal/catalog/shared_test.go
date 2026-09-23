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

// A restart throws away the tag → digest step and nothing else: the metadata
// behind every digest is already in the shared tier, valid for a day, because
// a digest names one manifest forever. Losing only that step still cost a
// fresh pod a question to the registry about every deployment it has — 10.8
// of the 11.2 seconds a cold build was measured to take.
//
// So the step is shared too, and a replica that never saw the tag can still
// reach the metadata.
func TestTheTagToDigestStepSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	shared := &cache.Memory{}
	const image = "registry.example.com/acme/order-api"
	const tag = "20260923110703-0c2dd7a7-0122"
	const digest = "sha256:aa9a1d768a978f66f4b83b9a849fc5c21ad93772dd9c6ced874ec76b7ecc19d8"

	// What a process that resolved this tag leaves behind.
	shared.Set(ctx, tagKey(image, tag), []byte(digest), tagTTL)

	// A pod that has just started: nothing in memory, everything to do.
	fresh := &Hub{Shared: shared}
	if got := fresh.digestFromShared(ctx, image, tag); got != digest {
		t.Fatalf("a new process could not resolve the tag: %q", got)
	}

	// Without a shared tier there is simply nothing, and the caller reads the
	// registry — which is the behaviour this replaces, not one it breaks.
	if got := (&Hub{}).digestFromShared(ctx, image, tag); got != "" {
		t.Errorf("no shared cache must mean no answer, got %q", got)
	}

	// Anything that is not a digest is refused rather than passed on: a
	// truncated or overwritten value would otherwise be looked up as one and
	// come back empty, which reads as "this image has no metadata".
	shared.Set(ctx, tagKey(image, "junk"), []byte("not-a-digest"), tagTTL)
	if got := fresh.digestFromShared(ctx, image, "junk"); got != "" {
		t.Errorf("a value that is not a digest must be ignored, got %q", got)
	}

	// Two tags of the same image do not collide.
	const other = "sha256:bb9a1d768a978f66f4b83b9a849fc5c21ad93772dd9c6ced874ec76b7ecc19d9"
	shared.Set(ctx, tagKey(image, "20260923110704-0c2dd7a8-0123"), []byte(other), tagTTL)
	if got := fresh.digestFromShared(ctx, image, tag); got != digest {
		t.Errorf("tags collided: %q", got)
	}
}

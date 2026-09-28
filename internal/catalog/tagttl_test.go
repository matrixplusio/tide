package catalog

import (
	"context"
	"testing"
	"time"

	"tide/internal/cache"
)

// A tag the installation's pattern recognises as a build product is kept for
// a day; the other kind still expires in minutes. The difference was 9.6 of
// the 10.5 seconds a cold catalog build took in production: every one of
// those seconds was a build tag being resolved again.
func TestABuildTagIsKeptForADay(t *testing.T) {
	h := &Hub{}
	const image = "registry.example.com/acme/order-api"
	const digest = "sha256:aa9a1d768a978f66f4b83b9a849fc5c21ad93772dd9c6ced874ec76b7ecc19d8"

	h.rememberTag(image, "20260923110703-0c2dd7a7-0122", digest, nil, builtTagTTL)
	h.rememberTag(image, "1.27", digest, nil, tagTTL)
	// Wind both back past the short lifetime but well inside the long one.
	for k, hit := range h.tags {
		hit.at = hit.at.Add(-6 * time.Minute)
		h.tags[k] = hit
	}

	if _, _, ok := h.tagged(image, "20260923110703-0c2dd7a7-0122"); !ok {
		t.Error("a build tag six minutes old must still be believed")
	}
	if _, _, ok := h.tagged(image, "1.27"); ok {
		t.Error("a version tag six minutes old must be asked about again")
	}

	// An entry written before ttl existed (zero) behaves as it always did.
	h.tags[image+":legacy"] = tagHit{digest: digest, at: time.Now().Add(-6 * time.Minute)}
	if _, _, ok := h.tagged(image, "legacy"); ok {
		t.Error("a hit with no lifetime must expire on the short one")
	}
}

// A release re-resolves the tags it is about to compare: forgetting a tag
// must make both tiers miss, so the next Inspect goes to the registry.
func TestForgetTagsMakesBothTiersMiss(t *testing.T) {
	ctx := context.Background()
	shared := &cache.Memory{}
	h := &Hub{Shared: shared}
	const image = "registry.example.com/acme/order-api"
	const tag = "20260923110703-0c2dd7a7-0122"
	const digest = "sha256:aa9a1d768a978f66f4b83b9a849fc5c21ad93772dd9c6ced874ec76b7ecc19d8"

	h.rememberTag(image, tag, digest, nil, builtTagTTL)
	shared.Set(ctx, tagKey(image, tag), []byte(digest), builtTagTTL)
	if got := h.digestFromShared(ctx, image, tag); got != digest {
		t.Fatalf("precondition: shared tier should answer, got %q", got)
	}

	h.ForgetTags(ctx, image, tag)

	if _, _, ok := h.tagged(image, tag); ok {
		t.Error("this process still remembers the tag")
	}
	if got := h.digestFromShared(ctx, image, tag); got != "" {
		t.Errorf("the shared tier still answers: %q", got)
	}

	// Another tag of the same image is left alone.
	h.rememberTag(image, "20260923110704-0c2dd7a8-0123", digest, nil, builtTagTTL)
	h.ForgetTags(ctx, image, tag)
	if _, _, ok := h.tagged(image, "20260923110704-0c2dd7a8-0123"); !ok {
		t.Error("forgetting one tag forgot its neighbour")
	}

	// Without a shared tier there is nothing to overwrite and nothing to panic on.
	(&Hub{}).ForgetTags(ctx, image, tag)
}

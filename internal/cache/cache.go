// Package cache is the shared, lossy tier in front of expensive derived
// data: the service catalog snapshot and image metadata.
//
// Nothing here is a source of truth. Every value can be rebuilt from
// PostgreSQL or from the upstreams, so an empty, stale or unreachable cache
// costs time and never correctness. That invariant is what makes Redis a
// soft dependency: losing it degrades Tide to the behaviour it had before
// there was one.
//
// Invalidation is not a message. Keys carry the cache_generation the value
// was built at, so a change committed in PostgreSQL simply moves every
// reader to a different key and the old one expires on its own. There is no
// "delete" that can be missed.
package cache

import (
	"context"
	"time"
)

// Cache is a key/value store that is allowed to forget. Get reports a miss
// for anything it cannot answer, including errors: a cache must never turn a
// request into a failure.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
	// Name describes the backing store for logs and the readiness page.
	Name() string
}

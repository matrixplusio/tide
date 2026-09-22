package pg

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Cache invalidation scopes. Each names a body of in-process state that
// every replica keeps its own copy of.
const (
	ScopeSettings = "settings"
	ScopeAccess   = "access"
	ScopeCatalog  = "catalog"
)

// Generations is the current counter for each scope. A replica holds the
// generation its caches were filled at; any difference means the caches
// describe a world that has moved on.
type Generations map[string]int64

// At returns the generation of one scope; an unknown scope is 0, which is
// what a fresh database also reports, so a missing row never looks newer.
func (g Generations) At(scope string) int64 { return g[scope] }

// Cache reads and bumps the cross-replica cache counters.
type Cache struct{ db *gorm.DB }

// Bump records that scope changed. It must run inside the transaction that
// makes the change: committed together, the counter and the data can never
// disagree, and no replica can be told about a change that was rolled back.
func (c *Cache) Bump(ctx context.Context, scope string) error {
	return c.db.WithContext(ctx).Exec(`
		INSERT INTO cache_generation (scope, n) VALUES ($1, 1)
		ON CONFLICT (scope) DO UPDATE SET n = cache_generation.n + 1, at = now()`, scope).Error
}

// Generations reads every counter in one query. The table has one row per
// scope and never grows, so this is a sequential scan of a page that
// PostgreSQL keeps in its buffer cache.
func (c *Cache) Generations(ctx context.Context) (Generations, error) {
	var rows []struct {
		Scope string
		N     int64
	}
	if err := c.db.WithContext(ctx).Raw(`SELECT scope, n FROM cache_generation`).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read cache generations: %w", err)
	}
	out := make(Generations, len(rows))
	for _, r := range rows {
		out[r.Scope] = r.N
	}
	return out, nil
}

type generationsKey struct{}

// WithGenerations carries the generations resolved once for a request, so
// every cache consulted while serving it agrees about which world it is in.
func WithGenerations(ctx context.Context, g Generations) context.Context {
	return context.WithValue(ctx, generationsKey{}, g)
}

// GenerationsFrom returns the generations attached to ctx, and whether any
// were. Background workers have none and read them for themselves.
func GenerationsFrom(ctx context.Context) (Generations, bool) {
	g, ok := ctx.Value(generationsKey{}).(Generations)
	return g, ok
}

// Generation resolves one scope's counter: from the request when it carries
// them, otherwise straight from the database.
func (c *Cache) Generation(ctx context.Context, scope string) (int64, error) {
	if g, ok := GenerationsFrom(ctx); ok {
		return g.At(scope), nil
	}
	g, err := c.Generations(ctx)
	if err != nil {
		return 0, err
	}
	return g.At(scope), nil
}

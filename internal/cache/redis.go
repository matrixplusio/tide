package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// opTimeout bounds every call. A slow cache must not become a slow page:
// past this, Tide behaves as if the key were missing and rebuilds.
const opTimeout = 500 * time.Millisecond

// Redis is the shared tier. Replicas that miss locally read what another
// replica already built instead of querying the upstreams again, and they
// read the same bytes, so two browser tabs on two replicas agree.
type Redis struct {
	client *redis.Client
	// warned keeps a broken cache from filling the log; the first failure
	// says so, the rest are counted by the metric.
	warned bool
}

func (r *Redis) Name() string { return "redis" }

// NewRedis connects and verifies the connection. A URL that does not work is
// a configuration mistake worth failing startup for — silently falling back
// would leave an operator believing a cache is in use when none is.
func NewRedis(ctx context.Context, url string) (*Redis, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 3*time.Second, opTimeout, opTimeout
	c := redis.NewClient(opts)
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Redis{client: c}, nil
}

func (r *Redis) Close() error { return r.client.Close() }

func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	b, err := r.client.Get(ctx, key).Bytes()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, false
	case err != nil:
		r.degraded("read", err)
		return nil, false
	}
	return b, true
}

func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		r.degraded("write", err)
	}
}

// degraded reports that the cache is not working. It is a warning, never an
// error: the request it happened during still succeeded.
func (r *Redis) degraded(op string, err error) {
	if r.warned {
		return
	}
	r.warned = true
	zap.L().Warn("cache unavailable, falling back to rebuilding",
		zap.String("op", op), zap.Error(err))
}

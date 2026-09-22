package cache_test

import (
	"context"
	"os"
	"testing"
	"time"

	"tide/internal/cache"
)

// redisCache skips unless TIDE_TEST_REDIS_URL points at a real server, the
// same bargain internal/testdb makes for PostgreSQL.
func redisCache(t *testing.T) *cache.Redis {
	t.Helper()
	url := os.Getenv("TIDE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("redis tests need TIDE_TEST_REDIS_URL")
	}
	r, err := cache.NewRedis(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// The whole point: what one replica wrote, another reads. Two clients stand
// in for two pods.
func TestOneReplicaReadsWhatAnotherWrote(t *testing.T) {
	ctx := context.Background()
	podA, podB := redisCache(t), redisCache(t)
	key := "tide:test:shared:" + time.Now().Format("150405.000000")

	if _, ok := podB.Get(ctx, key); ok {
		t.Fatal("key existed before the test wrote it")
	}
	podA.Set(ctx, key, []byte("built by A"), time.Minute)
	got, ok := podB.Get(ctx, key)
	if !ok || string(got) != "built by A" {
		t.Fatalf("pod B got %q ok=%v, want what pod A wrote", got, ok)
	}
}

func TestRedisExpires(t *testing.T) {
	ctx := context.Background()
	r := redisCache(t)
	key := "tide:test:ttl:" + time.Now().Format("150405.000000")
	r.Set(ctx, key, []byte("v"), 300*time.Millisecond)
	if _, ok := r.Get(ctx, key); !ok {
		t.Fatal("value missing right after it was written")
	}
	time.Sleep(600 * time.Millisecond)
	if _, ok := r.Get(ctx, key); ok {
		t.Fatal("value outlived its ttl")
	}
}

// A cache that cannot be reached must look empty, not break the request.
func TestUnreachableRedisReadsAsAMiss(t *testing.T) {
	if os.Getenv("TIDE_TEST_REDIS_URL") == "" {
		t.Skip("redis tests need TIDE_TEST_REDIS_URL")
	}
	r := redisCache(t)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, ok := r.Get(ctx, "anything"); ok {
		t.Fatal("a closed client reported a hit")
	}
	r.Set(ctx, "anything", []byte("v"), time.Minute) // must not panic
}

// A URL that does not work is a configuration mistake, not something to
// paper over: startup fails so the operator finds out.
func TestBadURLFailsLoudly(t *testing.T) {
	if _, err := cache.NewRedis(context.Background(), "not a url"); err == nil {
		t.Fatal("a malformed url was accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := cache.NewRedis(ctx, "redis://127.0.0.1:6300/0"); err == nil {
		t.Fatal("an unreachable server was accepted")
	}
}

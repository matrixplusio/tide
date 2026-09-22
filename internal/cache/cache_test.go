package cache

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestMemoryStoresAndExpires(t *testing.T) {
	ctx := context.Background()
	var m Memory

	if _, ok := m.Get(ctx, "absent"); ok {
		t.Fatal("an empty cache reported a hit")
	}
	m.Set(ctx, "k", []byte("v"), time.Minute)
	got, ok := m.Get(ctx, "k")
	if !ok || string(got) != "v" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	m.Set(ctx, "short", []byte("v"), -time.Second) // already expired
	if _, ok := m.Get(ctx, "short"); ok {
		t.Fatal("an expired entry was served")
	}
}

// A cache without a bound is a memory leak: the catalog is stamped with a
// generation, so a long-running replica would otherwise keep every snapshot
// it ever built.
func TestMemoryStaysBounded(t *testing.T) {
	ctx := context.Background()
	m := Memory{Limit: 8}
	for i := range 100 {
		m.Set(ctx, strconv.Itoa(i), []byte("v"), time.Hour)
	}
	m.mu.Lock()
	n := len(m.entries)
	m.mu.Unlock()
	if n > 8 {
		t.Fatalf("kept %d entries, limit was 8", n)
	}
	// The most recent write must still be there; that is the one in use.
	if _, ok := m.Get(ctx, "99"); !ok {
		t.Fatal("evicted the entry just written")
	}
}

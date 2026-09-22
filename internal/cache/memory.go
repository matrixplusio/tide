package cache

import (
	"context"
	"sync"
	"time"
)

// Memory is the cache used when no shared one is configured. It keeps
// values in this process, which is exactly the behaviour Tide had before
// Redis was an option: correct, but each replica pays for its own copy.
type Memory struct {
	mu      sync.Mutex
	entries map[string]entry
	// Limit bounds how many keys are kept; zero means DefaultLimit. A cache
	// without a bound is a memory leak with good intentions.
	Limit int
}

type entry struct {
	value   []byte
	expires time.Time
}

// DefaultLimit is generous for what is cached (one snapshot per generation,
// one entry per image digest) and small enough to stay well inside the
// container's memory limit.
const DefaultLimit = 2048

func (m *Memory) Name() string { return "in-process" }

func (m *Memory) Get(_ context.Context, key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]entry{}
	}
	if len(m.entries) >= m.limit() {
		m.evict()
	}
	m.entries[key] = entry{value: value, expires: time.Now().Add(ttl)}
}

func (m *Memory) limit() int {
	if m.Limit > 0 {
		return m.Limit
	}
	return DefaultLimit
}

// evict drops expired entries, and if that frees nothing, drops an arbitrary
// one. Keys here are generation-stamped, so the useful entries are the
// newest and anything left over is already garbage.
func (m *Memory) evict() {
	now := time.Now()
	freed := false
	for k, e := range m.entries {
		if now.After(e.expires) {
			delete(m.entries, k)
			freed = true
		}
	}
	if freed {
		return
	}
	for k := range m.entries {
		delete(m.entries, k)
		return
	}
}

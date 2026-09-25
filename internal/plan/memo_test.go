package plan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"tide/internal/catalog"
	"tide/internal/upstream/kargo"
)

// freightServer counts how many times the project-wide freight query is made
// and reports the URL of each, so a test can tell "asked once" from "asked
// once per item".
func freightServer(t *testing.T) (*catalog.Clients, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stage") == "" {
			calls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"groups":{"":{"items":[{"metadata":{"name":"f1"}}]}}}`))
	}))
	t.Cleanup(srv.Close)
	return &catalog.Clients{Kargo: kargo.New(srv.URL, "t", true)}, &calls
}

// Fifty services in one project asked Kargo for that project's freight fifty
// times, and the request takes no service: the answers were identical. That
// is what pushed a batch past the upstream timeout.
func TestTheProjectsFreightIsFetchedOncePerRequest(t *testing.T) {
	c, calls := freightServer(t)
	m := NewMemo()
	ctx := WithMemo(context.Background(), m)

	for i := 0; i < 50; i++ {
		if _, err := memoFrom(ctx).AllFreight(ctx, c, "idc", "kargo-acme-alpha"); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("asked the upstream %d times, want 1", got)
	}
}

// The items are planned concurrently, so deduplication that only holds when
// callers happen not to overlap would not deduplicate the case it exists for.
func TestConcurrentCallersShareOneFetch(t *testing.T) {
	c, calls := freightServer(t)
	ctx := WithMemo(context.Background(), NewMemo())

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := memoFrom(ctx).AllFreight(ctx, c, "idc", "kargo-acme-alpha"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("fifty concurrent callers made %d requests, want 1", got)
	}
}

// Two projects are two questions.
func TestDifferentProjectsAreNotShared(t *testing.T) {
	c, calls := freightServer(t)
	ctx := WithMemo(context.Background(), NewMemo())
	for _, p := range []string{"kargo-acme-alpha", "kargo-acme-beta", "kargo-acme-alpha"} {
		if _, err := memoFrom(ctx).AllFreight(ctx, c, "idc", p); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("two projects took %d requests, want 2", got)
	}
}

// Nothing installs a memo outside release creation, and those callers must
// keep working exactly as they did.
func TestWithoutAMemoEveryCallReachesTheUpstream(t *testing.T) {
	c, calls := freightServer(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := memoFrom(ctx).AllFreight(ctx, c, "idc", "kargo-acme-alpha"); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("made %d requests, want 3 — a nil memo must not change behaviour", got)
	}
}

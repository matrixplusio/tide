package executor

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"tide/internal/audit"
	"tide/internal/release"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

// fake records calls and finishes items on the first poll.
type fake struct {
	mu       sync.Mutex
	executed []string // services, in call order
	fail     map[string]bool
}

func service(it *release.Item) string {
	var p struct {
		Service string `json:"service"`
	}
	_ = json.Unmarshal(it.Payload, &p)
	return p.Service
}

func (*fake) Kind() string                                  { return release.KindRestart }
func (*fake) Validate(context.Context, *release.Item) error { return nil }
func (f *fake) Execute(_ context.Context, it *release.Item) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed = append(f.executed, service(it))
	return "ref-" + service(it), nil
}
func (f *fake) Poll(_ context.Context, it *release.Item) (ItemStatus, error) {
	if f.fail[service(it)] {
		return ItemStatus{Done: true, Error: "boom"}, nil
	}
	return ItemStatus{Done: true, Success: true}, nil
}

var tester = audit.Actor{Sub: "local:tester", Name: "tester"}

// batch creates an executing release: a and b in sequence 1, c in sequence 2.
func batch(t *testing.T, s *pg.Store) string {
	t.Helper()
	ctx := context.Background()
	item := func(svc string, seq int) release.ItemInput {
		p, _ := json.Marshal(release.RestartPayload{Upstream: "local", Service: svc, Env: "qa", App: svc + "-qa",
			Workloads: []release.Workload{{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "ns", Name: svc}}})
		return release.ItemInput{Kind: release.KindRestart, Sequence: seq, Payload: p}
	}
	r, err := s.Releases.Create(ctx, tester, release.CreateInput{Env: "qa", Reason: "batch",
		Items: []release.ItemInput{item("svc-c", 2), item("svc-a", 1), item("svc-b", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, tester, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Confirm(ctx, tester, r.ID, 0, time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func tick(t *testing.T, e *Engine, id string) *release.Release {
	t.Helper()
	ctx := context.Background()
	r, err := e.PG.Releases.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.advance(ctx, r, time.Hour); err != nil {
		t.Fatal(err)
	}
	r, err = e.PG.Releases.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func statuses(r *release.Release) map[string]release.ItemStatus {
	out := map[string]release.ItemStatus{}
	for _, it := range r.Items {
		out[service(&it)] = it.Status
	}
	return out
}

func TestBatchRunsSequenceGroupsInOrder(t *testing.T) {
	s, _ := testdb.Setup(t)
	f := &fake{}
	e := &Engine{PG: s, Executors: map[string]ItemExecutor{release.KindRestart: f}}
	id := batch(t, s)

	r := tick(t, e, id) // sequence 1 starts, both in parallel; sequence 2 waits
	if got := statuses(r); got["svc-a"] != release.ItemExecuting || got["svc-b"] != release.ItemExecuting || got["svc-c"] != release.ItemPending {
		t.Fatalf("tick 1: %v", got)
	}
	r = tick(t, e, id) // sequence 1 finishes, sequence 2 starts in the same tick
	if got := statuses(r); got["svc-a"] != release.ItemSucceeded || got["svc-b"] != release.ItemSucceeded || got["svc-c"] != release.ItemExecuting {
		t.Fatalf("tick 2: %v", got)
	}
	if i := slices.Index(f.executed, "svc-c"); i != 2 {
		t.Fatalf("svc-c must start after both sequence-1 items: %v", f.executed)
	}
	r = tick(t, e, id)
	if r.Status != release.Succeeded || statuses(r)["svc-c"] != release.ItemSucceeded {
		t.Fatalf("tick 3: %s %v", r.Status, statuses(r))
	}
}

func TestBatchFailureSkipsLaterSequences(t *testing.T) {
	s, _ := testdb.Setup(t)
	f := &fake{fail: map[string]bool{"svc-a": true}}
	e := &Engine{PG: s, Executors: map[string]ItemExecutor{release.KindRestart: f}}
	id := batch(t, s)

	tick(t, e, id)
	r := tick(t, e, id)
	got := statuses(r)
	if got["svc-a"] != release.ItemFailed || got["svc-b"] != release.ItemSucceeded || got["svc-c"] != release.ItemSkipped {
		t.Fatalf("after failure: %v", got)
	}
	if r.Status != release.Failed {
		t.Fatalf("release status: %s", r.Status)
	}
	if slices.Contains(f.executed, "svc-c") {
		t.Fatal("a skipped item must never reach upstream")
	}
}

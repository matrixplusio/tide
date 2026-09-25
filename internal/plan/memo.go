package plan

import (
	"context"
	"sync"

	"tide/internal/catalog"
	"tide/internal/upstream/kargo"
)

// Memo remembers upstream answers for the length of one request.
//
// Planning a release asks Kargo the same question once per item: "what
// freight does this project have?" takes a project and nothing else, so a
// batch of fifty services asked it fifty times and got fifty identical
// answers. Serially that was slower than the upstream client's own timeout,
// which is how a batch at the documented limit of fifty could not be
// submitted at all.
//
// It is deliberately not a cache. It lives in a local variable, is passed
// down by hand, and is thrown away when the request ends — so there is no
// expiry to get wrong and no way to read something stale. The next request
// asks Kargo again.
type Memo struct {
	mu   sync.Mutex
	once map[string]*sync.Once
	// Guarded by the once for their key, not by mu.
	freight map[string][]kargo.Freight
	stages  map[string]map[string]*kargo.Stage
	errs    map[string]error
}

func NewMemo() *Memo {
	return &Memo{
		once:    map[string]*sync.Once{},
		freight: map[string][]kargo.Freight{},
		stages:  map[string]map[string]*kargo.Stage{},
		errs:    map[string]error{},
	}
}

// starter returns the sync.Once for key, so that concurrent callers asking
// the same question make one request between them rather than one each.
func (m *Memo) starter(key string) *sync.Once {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.once[key]
	if !ok {
		o = &sync.Once{}
		m.once[key] = o
	}
	return o
}

// Stage is GetStage, served from one ListStages per project.
//
// The two return the same resource — checked field by field against a live
// Kargo across 47 stages, including ones carrying promotion history, with no
// difference outside resourceVersion. A stage the list does not have is
// fetched on its own: one created since the list was read is the case that
// leaves, and answering "not found" for it would be wrong.
func (m *Memo) Stage(ctx context.Context, c *catalog.Clients, upstream, project, stage string) (*kargo.Stage, error) {
	if m == nil {
		return c.Kargo.GetStage(ctx, project, stage)
	}
	key := "stages\x00" + upstream + "\x00" + project
	m.starter(key).Do(func() {
		list, err := c.Kargo.ListStages(ctx, project)
		byName := make(map[string]*kargo.Stage, len(list))
		for i := range list {
			byName[list[i].Metadata.Name] = &list[i]
		}
		m.mu.Lock()
		m.stages[key], m.errs[key] = byName, err
		m.mu.Unlock()
	})
	m.mu.Lock()
	byName, err := m.stages[key], m.errs[key]
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if st, ok := byName[stage]; ok {
		return st, nil
	}
	return c.Kargo.GetStage(ctx, project, stage)
}

// AllFreight is QueryFreight with no stage: every freight in the project.
// The first caller fetches, the rest wait for it and share the answer —
// items are planned concurrently, so without that the deduplication would
// only work when they happened not to overlap.
func (m *Memo) AllFreight(ctx context.Context, c *catalog.Clients, upstream, project string) ([]kargo.Freight, error) {
	if m == nil {
		return c.Kargo.QueryFreight(ctx, project, "")
	}
	key := "freight\x00" + upstream + "\x00" + project
	m.starter(key).Do(func() {
		f, err := c.Kargo.QueryFreight(ctx, project, "")
		m.mu.Lock()
		m.freight[key], m.errs[key] = f, err
		m.mu.Unlock()
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.freight[key], m.errs[key]
}

// memoKey carries a Memo through call sites that already pass a context and
// would otherwise all need a new parameter for something most of them do not
// use. A request without one plans exactly as it did before.
type memoKey struct{}

// WithMemo returns a context whose plan lookups share their answers.
func WithMemo(ctx context.Context, m *Memo) context.Context {
	return context.WithValue(ctx, memoKey{}, m)
}

func memoFrom(ctx context.Context) *Memo {
	m, _ := ctx.Value(memoKey{}).(*Memo)
	return m
}

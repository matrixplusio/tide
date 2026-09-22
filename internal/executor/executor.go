// Package executor drives executing releases: it runs item executors by
// sequence group and polls them to a terminal state.
package executor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"tide/internal/metrics"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/store/pg"
)

type ItemStatus struct {
	Done    bool
	Success bool
	Error   string
	// Waiting says what a not-yet-done item is waiting for; it ends up in the
	// failure message if the item times out.
	Waiting string
}

type ItemExecutor interface {
	Kind() string
	// Validate runs just before execution: the target must still exist and
	// the live version must still be what the release was built against.
	Validate(ctx context.Context, item *release.Item) error
	// Execute starts the change and returns an external reference.
	Execute(ctx context.Context, item *release.Item) (externalRef string, err error)
	Poll(ctx context.Context, item *release.Item) (ItemStatus, error)
}

// Notifier is told about release lifecycle events (Lark / Teams).
type Notifier interface {
	ReleaseEvent(ctx context.Context, r *release.Release, event string)
}

type Engine struct {
	PG        *pg.Store
	Settings  *settings.Store
	Executors map[string]ItemExecutor
	Notifier  Notifier
	Interval  time.Duration
	// OnChange is called after any state change (cache invalidation).
	OnChange func()
}

// Run polls until ctx ends. With several replicas only the one holding the
// advisory lock works; see pg.Lead.
func (e *Engine) Run(ctx context.Context) {
	if e.Interval == 0 {
		e.Interval = 5 * time.Second
	}
	pg.Lead(ctx, e.PG.DB(), pg.LockExecutor, e.Interval, "executor", e.Tick)
}

func (e *Engine) Tick(ctx context.Context) {
	policy, err := e.Settings.ReleasePolicy(ctx)
	if err != nil {
		zap.L().Error("load release policy failed", zap.Error(err))
		return
	}
	expired, err := e.PG.Releases.ExpireConfirming(ctx, policy.ConfirmTTL())
	if err != nil {
		zap.L().Error("expire confirming releases failed", zap.Error(err))
	}
	unapproved, err := e.PG.Releases.ExpireApprovals(ctx)
	if err != nil {
		zap.L().Error("expire approving releases failed", zap.Error(err))
	}
	for _, r := range append(expired, unapproved...) {
		e.changed()
		if e.Notifier != nil {
			e.Notifier.ReleaseEvent(ctx, r, "cancelled")
		}
	}
	list, err := e.PG.Releases.Executing(ctx)
	if err != nil {
		zap.L().Error("list active releases failed", zap.Error(err))
		return
	}
	e.publishState(ctx, list)
	for i := range list {
		if err := e.advance(ctx, &list[i], policy.ExecuteTimeout()); err != nil {
			zap.L().Error("advance release failed", zap.String("release", list[i].ID), zap.Error(err))
		}
	}
}

// publishState exports what is in flight, for dashboards and alerts.
func (e *Engine) publishState(ctx context.Context, executing []release.Release) {
	items := 0
	for i := range executing {
		for _, it := range executing[i].Items {
			if it.Status == release.ItemExecuting {
				items++
			}
		}
	}
	count := func(s release.Status) int {
		_, total, err := e.PG.Releases.List(ctx, pg.ReleaseFilter{Statuses: []release.Status{s}}, 1, 1)
		if err != nil {
			return 0
		}
		return int(total)
	}
	metrics.ExecutorState(items, count(release.Confirming), count(release.Approving))
}

func (e *Engine) changed() {
	if e.OnChange != nil {
		e.OnChange()
	}
}

func (e *Engine) advance(ctx context.Context, r *release.Release, timeout time.Duration) error {
	groups := map[int][]*release.Item{}
	var seqs []int
	for i := range r.Items {
		it := &r.Items[i]
		if _, ok := groups[it.Sequence]; !ok {
			seqs = append(seqs, it.Sequence)
		}
		groups[it.Sequence] = append(groups[it.Sequence], it)
	}
	sort.Ints(seqs)

	if e.Notifier != nil && !slices.ContainsFunc(r.Items, func(it release.Item) bool { return it.Status != release.ItemPending }) {
		e.Notifier.ReleaseEvent(ctx, r, "started")
	}

	failed := false
	for _, seq := range seqs {
		group := groups[seq]
		if failed {
			// A failed earlier group stops everything after it.
			for _, it := range group {
				if it.Status == release.ItemPending {
					if err := e.PG.Releases.FinishItem(ctx, *it, release.ItemSkipped, "skipped: an earlier sequence failed"); err != nil {
						return err
					}
					e.changed()
				}
			}
			continue
		}
		if err := e.runGroup(ctx, group, timeout); err != nil {
			return err
		}
		// Re-read to see what the group looks like now.
		items, err := e.PG.Releases.Items(ctx, r.ID)
		if err != nil {
			return err
		}
		done := true
		for _, it := range items {
			if it.Sequence != seq {
				continue
			}
			if !it.Status.Terminal() {
				done = false
			}
			if it.Status == release.ItemFailed {
				failed = true
			}
		}
		if !done {
			return nil // wait for this group before starting the next
		}
	}

	items, err := e.PG.Releases.Items(ctx, r.ID)
	if err != nil {
		return err
	}
	final := release.Succeeded
	for _, it := range items {
		if !it.Status.Terminal() {
			return nil
		}
		if it.Status != release.ItemSucceeded {
			final = release.Failed
		}
	}
	if err := e.PG.Releases.Finish(ctx, r.ID, final); err != nil {
		return err
	}
	since := time.Time{}
	if r.ConfirmedAt != nil {
		since = *r.ConfirmedAt
	}
	metrics.ReleaseFinished(r.Env, string(final), since, time.Now())
	e.changed()
	if e.Notifier != nil {
		if fresh, err := e.PG.Releases.Get(ctx, r.ID); err == nil {
			e.Notifier.ReleaseEvent(ctx, fresh, string(final))
		}
	}
	return nil
}

func (e *Engine) runGroup(ctx context.Context, group []*release.Item, timeout time.Duration) error {
	var wg sync.WaitGroup
	errs := make([]error, len(group))
	for i, it := range group {
		ex, ok := e.Executors[it.Kind]
		if !ok {
			if it.Status == release.ItemPending {
				errs[i] = e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, fmt.Sprintf("no executor for kind %q", it.Kind))
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.step(ctx, ex, it, timeout)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// unknownOutcomeAfter: an item marked executing without a stored reference
// means we crashed between calling upstream and recording the result. We
// cannot know whether the promotion exists, so fail loudly instead of retrying.
const unknownOutcomeAfter = time.Minute

func (e *Engine) step(ctx context.Context, ex ItemExecutor, it *release.Item, timeout time.Duration) error {
	switch it.Status {
	case release.ItemPending:
		if err := ex.Validate(ctx, it); err != nil {
			return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, "validate: "+err.Error())
		}
		claimed, err := e.PG.Releases.StartItem(ctx, it.ID)
		if err != nil || !claimed {
			return err
		}
		it.Status = release.ItemExecuting
		ref, err := ex.Execute(ctx, it)
		if err != nil {
			return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, err.Error())
		}
		it.ExternalRef = ref
		e.changed()
		return e.PG.Releases.MarkExecuted(ctx, *it, ref)

	case release.ItemExecuting:
		if it.ExternalRef == "" {
			if it.StartedAt != nil && time.Since(*it.StartedAt) > unknownOutcomeAfter {
				return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed,
					"execution outcome unknown: Tide stopped after contacting upstream but before recording the result; check upstream manually")
			}
			return nil
		}
		st, err := ex.Poll(ctx, it)
		if err != nil {
			zap.L().Warn("poll item failed", zap.Int64("item", it.ID), zap.Error(err))
		}
		if err == nil && st.Done {
			e.changed()
			if st.Success {
				return e.PG.Releases.FinishItem(ctx, *it, release.ItemSucceeded, "")
			}
			return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, st.Error)
		}
		if it.StartedAt != nil && time.Since(*it.StartedAt) > timeout {
			e.changed()
			msg := fmt.Sprintf("timed out after %s without a terminal state from upstream", timeout)
			if st.Waiting != "" {
				msg = fmt.Sprintf("timed out after %s; still waiting: %s", timeout, st.Waiting)
			}
			if err != nil {
				msg += "; last poll error: " + err.Error()
			}
			return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, msg)
		}
	}
	return nil
}

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

	"tide/internal/audit"
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
	// Paused is set while a blue-green or canary rollout waits for a person.
	// The execution timeout does not run then; the switch timeout does.
	Paused *Pause
	// RevertRef is a promotion the executor started to put the previous
	// version back; the engine records it so the next poll follows it.
	RevertRef string

	// aborted: a rollout was aborted, so the executor still has git and
	// Kargo to put back. Never seen by the engine.
	aborted bool
}

// RolloutDriver is implemented by the executors whose items can stop at a
// blue-green or canary rollout, so the engine can abort a wait nobody
// attended to.
type RolloutDriver interface {
	Rollout(ctx context.Context, it *release.Item, action RolloutAction) error
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

	// switchTimeout is the release policy's, read once per tick.
	switchTimeout time.Duration
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
	e.switchTimeout = policy.SwitchTimeout()
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
		if err == nil && st.RevertRef != "" {
			if err := e.PG.Releases.MarkReverting(ctx, *it, st.RevertRef); err != nil {
				return err
			}
			now := time.Now()
			it.RevertRef, it.ClockFrom = st.RevertRef, &now
			e.changed()
		}
		if err == nil && !st.Done {
			if st.Paused != nil {
				return e.waitForDecision(ctx, ex, it, st.Paused)
			}
			if it.PausedSince != nil {
				if err := e.PG.Releases.MarkResumed(ctx, it.ID); err != nil {
					return err
				}
				now := time.Now()
				it.ClockFrom = &now
				e.changed()
			}
		}
		if err == nil && st.Done {
			// A wait that ended with the item is over too; left set, a list
			// would go on saying the release waits for somebody.
			if it.PausedSince != nil {
				if err := e.PG.Releases.MarkResumed(ctx, it.ID); err != nil {
					return err
				}
			}
			e.changed()
			if st.Success {
				return e.PG.Releases.FinishItem(ctx, *it, release.ItemSucceeded, "")
			}
			return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, st.Error)
		}
		// After a wait for a decision the clock starts again: the rollout
		// was let through just now and is owed a whole timeout.
		from := it.StartedAt
		if it.ClockFrom != nil {
			from = it.ClockFrom
		}
		if from != nil && time.Since(*from) > timeout {
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

// waitForDecision keeps the books on a rollout stopped for a person: it
// announces the wait once, reminds once halfway through, and aborts it when
// the switch timeout has passed. Aborting only ever sends traffic back to the
// version that was already running.
//
// The database, not this process, remembers what was said: whichever replica
// leads next must not announce the same wait again.
func (e *Engine) waitForDecision(ctx context.Context, ex ItemExecutor, it *release.Item, p *Pause) error {
	limit := e.switchTimeout
	if limit <= 0 {
		limit = settings.DefaultSwitchTimeoutMinutes * time.Minute
	}
	// A canary that is let through often reaches its next stop between two
	// polls, so the executor never sees it moving. The rollout's own record
	// tells the stops apart: one that began after the wait on file is a new
	// wait, with its own announcement and its own deadline.
	if it.PausedSince != nil && !p.Since.IsZero() && p.Since.After(it.PausedSince.Add(time.Second)) {
		if err := e.PG.Releases.MarkResumed(ctx, it.ID); err != nil {
			return err
		}
		it.PausedSince, it.RemindedAt = nil, nil
	}
	since := p.Since
	if since.IsZero() || since.After(time.Now()) {
		since = time.Now()
	}
	if it.PausedSince != nil && it.PausedSince.Before(since) {
		since = *it.PausedSince
	}
	if it.PausedSince == nil {
		started, err := e.PG.Releases.MarkPaused(ctx, it.ID, since)
		if err != nil {
			return err
		}
		if started {
			e.changed()
			// A batch stops many items at once; the channel hears about it
			// once, with every waiting service listed, not once per item.
			if first, err := e.PG.Releases.FirstWaiting(ctx, it.ReleaseID); err == nil && first == it.ID {
				e.announce(ctx, it.ReleaseID, "awaiting_switch")
			}
		}
	}
	waited := time.Since(since)
	if waited > limit {
		driver, ok := ex.(RolloutDriver)
		if !ok {
			return e.PG.Releases.FinishItem(ctx, *it, release.ItemFailed, "waited too long for a decision")
		}
		if err := driver.Rollout(ctx, it, ActionAbort); err != nil && !errors.Is(err, ErrNotWaiting) {
			return fmt.Errorf("abort after waiting %s: %w", limit, err)
		}
		// The item is not finished here: the next poll sees the abort like
		// any other and puts the previous version back in git and Kargo.
		_ = e.PG.Audit.Write(ctx, audit.System, "release.rollout", it.ReleaseID, "",
			map[string]any{"item": it.ID, "action": ActionAbort, "reason": fmt.Sprintf("nobody decided within %s", limit)})
		e.changed()
		return nil
	}
	// The reminder, too, is the release's: only the first waiting item sends
	// it. The others do not mark themselves reminded, so whichever becomes
	// first once that one is decided still reminds about its own wait.
	if waited > limit/2 && it.RemindedAt == nil {
		first, err := e.PG.Releases.FirstWaiting(ctx, it.ReleaseID)
		if err != nil {
			return err
		}
		if first == it.ID {
			reminded, err := e.PG.Releases.MarkReminded(ctx, it.ID)
			if err != nil {
				return err
			}
			if reminded {
				e.announce(ctx, it.ReleaseID, "awaiting_switch")
			}
		}
	}
	return nil
}

// announce sends a release event with the release as it is now.
func (e *Engine) announce(ctx context.Context, releaseID, event string) {
	if e.Notifier == nil {
		return
	}
	if r, err := e.PG.Releases.Get(ctx, releaseID); err == nil {
		e.Notifier.ReleaseEvent(ctx, r, event)
	}
}

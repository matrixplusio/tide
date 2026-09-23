package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"tide/internal/audit"
	"tide/internal/release"
)

type Releases struct {
	db    *gorm.DB
	audit *Audit
}

type ReleaseFilter struct {
	Statuses []release.Status
	Env      string
	// Service is an exact item service name; ServiceLike matches names
	// containing it (list search).
	Service     string
	ServiceLike string
	// Services, when non-nil, keeps releases touching any of these services
	// (a catalog project resolved by the caller); empty matches nothing.
	Services []string
	Jira     string
	Kind     string
	// CreatedBy is an exact subject; Creator matches the name or subject.
	CreatedBy string
	Creator   string
	// DecidedBy keeps releases this subject approved or rejected.
	DecidedBy string
	Since     *time.Time
	Until     *time.Time
}

// Create inserts a draft with its items and audits it.
func (r *Releases) Create(ctx context.Context, actor audit.Actor, in release.CreateInput) (*release.Release, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	var id string
	err := r.tx(ctx, func(tx *Releases) error {
		var row struct {
			Day time.Time
			N   int
		}
		if err := tx.db.Raw(`
			INSERT INTO release_counters (day, n) VALUES ((now() AT TIME ZONE 'Asia/Shanghai')::date, 1)
			ON CONFLICT (day) DO UPDATE SET n = release_counters.n + 1
			RETURNING day, n`).Scan(&row).Error; err != nil {
			return err
		}
		id = fmt.Sprintf("REL-%s-%03d", row.Day.Format("20060102"), row.N)
		title := in.Title
		if title == "" {
			title = in.DefaultTitle()
		}
		source := in.Source
		if source == "" {
			source = release.SourceUI
		}
		if err := tx.db.Exec(`
			INSERT INTO releases (id, title, env, jira_ticket, reason, created_by, created_by_name, status, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'draft', $8)`,
			id, title, in.Env, in.JiraTicket, in.Reason, actor.Sub, actor.Name, source).Error; err != nil {
			return err
		}
		payloads := make([]json.RawMessage, len(in.Items))
		for i, it := range in.Items {
			payloads[i] = it.Payload
			if err := tx.db.Exec(`
				INSERT INTO release_items (release_id, kind, sequence, payload, status)
				VALUES ($1, $2, $3, $4::jsonb, 'planned')`, id, it.Kind, it.Sequence, string(it.Payload)).Error; err != nil {
				return err
			}
		}
		return tx.audit.Write(ctx, actor, "release.create", id, in.JiraTicket, map[string]any{
			"env": in.Env, "title": title, "reason": in.Reason, "source": source, "items": payloads,
		})
	})
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// services lists the services a release touches, for the audit record. The
// audit page's service filter looks at the record's own detail, so anything
// written without this is invisible to somebody tracing one service.
func (r *Releases) services(ctx context.Context, id string) []string {
	var names []string
	if err := r.db.WithContext(ctx).Raw(`SELECT DISTINCT payload->>'service' FROM release_items
		WHERE release_id = $1 AND payload->>'service' IS NOT NULL ORDER BY 1`, id).Scan(&names).Error; err != nil {
		// A record missing its services is worse than none of the extras, but
		// far better than refusing the write it belongs to.
		return nil
	}
	return names
}

// detail is the common shape of a release-level audit record: the environment
// and the services, plus whatever the action adds. Every release.* record goes
// through here so they are all findable the same way.
func (r *Releases) detail(ctx context.Context, rel *release.Release, extra map[string]any) map[string]any {
	d := map[string]any{"env": rel.Env}
	if svcs := r.services(ctx, rel.ID); len(svcs) > 0 {
		d["services"] = svcs
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// bumpCatalog tells every replica its service snapshot is out of date: throw
// it away and read every upstream again. It is called from inside the release
// transaction, so the new state and the notice that it changed become visible
// together.
//
// One counter covers the whole catalog, so this is expensive for everybody and
// belongs only where what is deployed actually changed — which of a release's
// steps is just the last one. Submitting, cancelling, approving and rejecting
// move a row in this table and nothing the snapshot holds.
func (r *Releases) bumpCatalog(ctx context.Context) error {
	return (&Cache{db: r.db}).Bump(ctx, ScopeCatalog)
}

// Submit moves draft → confirming and claims every target. The partial unique
// index turns a concurrent claim on the same service+env into ErrTargetBusy.
func (r *Releases) Submit(ctx context.Context, actor audit.Actor, id string) (*release.Release, error) {
	err := r.tx(ctx, func(tx *Releases) error {
		cur, err := tx.lock(ctx, id)
		if err != nil {
			return err
		}
		if !release.CanTransition(cur.Status, release.Confirming) {
			return fmt.Errorf("%w: %s → %s", release.ErrInvalidTransition, cur.Status, release.Confirming)
		}
		if err := tx.db.Exec(`UPDATE release_items SET status = 'pending' WHERE release_id = $1 AND status = 'planned'`, id).Error; err != nil {
			if isUniqueViolation(err) {
				return release.ErrTargetBusy
			}
			return err
		}
		if err := tx.db.Exec(`UPDATE releases SET status = 'confirming', submitted_at = now() WHERE id = $1`, id).Error; err != nil {
			return err
		}
		if err := tx.audit.Write(ctx, actor, "release.submit", id, cur.JiraTicket, tx.detail(ctx, cur, nil)); err != nil {
			return err
		}
		// Deliberately not bumping the catalog: it carries what is running,
		// not where a release has got to, and "in flight" is read from this
		// table. Invalidating it here made one person's release a fan-out
		// across every upstream for everybody — see bumpCatalog.
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Confirm moves a confirming release on after the reading window: to executing,
// or to approving when rule is not nil (the rule is snapshotted on the release).
// The read-time check uses the database clock so neither a fast client nor a
// skewed app server can shorten it. Rejections are audited outside the
// rolled-back transaction.
func (r *Releases) Confirm(ctx context.Context, actor audit.Actor, id string, readTime, ttl time.Duration, rule *release.ApprovalRule) (*release.Release, error) {
	var denied error
	var cur *release.Release
	err := r.tx(ctx, func(tx *Releases) error {
		var err error
		if cur, err = tx.lock(ctx, id); err != nil {
			return err
		}
		next := release.Executing
		if rule != nil {
			next = release.Approving
		}
		if !release.CanTransition(cur.Status, next) {
			return fmt.Errorf("%w: %s → %s", release.ErrInvalidTransition, cur.Status, next)
		}
		var secs float64
		if err := tx.db.Raw(`SELECT EXTRACT(EPOCH FROM now() - submitted_at) FROM releases WHERE id = $1`, id).Scan(&secs).Error; err != nil {
			return err
		}
		elapsed := time.Duration(secs * float64(time.Second))
		switch {
		case elapsed < readTime:
			denied = fmt.Errorf("%w: %.1fs remaining", release.ErrTooEarly, (readTime - elapsed).Seconds())
			return denied
		case elapsed > ttl:
			denied = release.ErrConfirmExpired
			return denied
		}
		if rule != nil {
			b, _ := json.Marshal(rule)
			if err := tx.db.Exec(`UPDATE releases SET status = 'approving', confirmed_at = now(), confirmed_by = $2, approval_rule = $3,
				approval_expires_at = now() + make_interval(mins => $4) WHERE id = $1`, id, actor.Sub, string(b), rule.TimeoutMinutes).Error; err != nil {
				return err
			}
			return tx.audit.Write(ctx, actor, "release.confirm", id, cur.JiraTicket, tx.detail(ctx, cur, map[string]any{"readSeconds": secs, "approval": rule}))
		}
		if err := tx.db.Exec(`UPDATE releases SET status = 'executing', confirmed_at = now(), confirmed_by = $2 WHERE id = $1`, id, actor.Sub).Error; err != nil {
			return err
		}
		return tx.audit.Write(ctx, actor, "release.confirm", id, cur.JiraTicket, tx.detail(ctx, cur, map[string]any{"readSeconds": secs}))
	})
	if denied != nil && cur != nil {
		_ = r.audit.Write(ctx, actor, "release.confirm.denied", id, cur.JiraTicket, r.detail(ctx, cur, map[string]any{"error": denied.Error()}))
	}
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Start moves a confirming release on without the reading window: to
// executing, or to approving when rule is not nil. It exists for releases
// nobody is reading — a CI-triggered one in an environment configured to
// release automatically. The reading window protects a person about to press
// a button; there is no person here, so enforcing it would only stall the
// release. Everything else (approval rules, target claims, audit) is
// unchanged, and the audit record names the pipeline that caused it.
func (r *Releases) Start(ctx context.Context, actor audit.Actor, id string, rule *release.ApprovalRule, detail map[string]any) (*release.Release, error) {
	err := r.tx(ctx, func(tx *Releases) error {
		cur, err := tx.lock(ctx, id)
		if err != nil {
			return err
		}
		next := release.Executing
		if rule != nil {
			next = release.Approving
		}
		if !release.CanTransition(cur.Status, next) {
			return fmt.Errorf("%w: %s → %s", release.ErrInvalidTransition, cur.Status, next)
		}
		d := tx.detail(ctx, cur, detail)
		d["auto"] = true
		if rule != nil {
			b, _ := json.Marshal(rule)
			d["approval"] = rule
			if err := tx.db.Exec(`UPDATE releases SET status = 'approving', confirmed_at = now(), confirmed_by = $2, approval_rule = $3,
				approval_expires_at = now() + make_interval(mins => $4) WHERE id = $1`, id, actor.Sub, string(b), rule.TimeoutMinutes).Error; err != nil {
				return err
			}
		} else if err := tx.db.Exec(`UPDATE releases SET status = 'executing', confirmed_at = now(), confirmed_by = $2 WHERE id = $1`,
			id, actor.Sub).Error; err != nil {
			return err
		}
		// A distinct action, not release.confirm: "a machine let this through"
		// and "a person pressed confirm" must not read the same in the log.
		return tx.audit.Write(ctx, actor, "release.confirm.auto", id, cur.JiraTicket, d)
	})
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// AuditConfirmDenied records a confirm rejected before reaching Confirm (e.g.
// digests that do not match).
func (r *Releases) AuditConfirmDenied(ctx context.Context, actor audit.Actor, rel *release.Release, reason string) error {
	return r.audit.Write(ctx, actor, "release.confirm.denied", rel.ID, rel.JiraTicket, r.detail(ctx, rel, map[string]any{"error": reason}))
}

func (r *Releases) Cancel(ctx context.Context, actor audit.Actor, id, why string) (*release.Release, error) {
	err := r.tx(ctx, func(tx *Releases) error {
		cur, err := tx.lock(ctx, id)
		if err != nil {
			return err
		}
		if !release.CanTransition(cur.Status, release.Cancelled) {
			return fmt.Errorf("%w: %s → %s", release.ErrInvalidTransition, cur.Status, release.Cancelled)
		}
		if err := tx.db.Exec(`UPDATE release_items SET status = 'cancelled', finished_at = now()
			WHERE release_id = $1 AND status IN ('planned', 'pending')`, id).Error; err != nil {
			return err
		}
		if err := tx.db.Exec(`UPDATE releases SET status = 'cancelled', finished_at = now() WHERE id = $1`, id).Error; err != nil {
			return err
		}
		if err := tx.audit.Write(ctx, actor, "release.cancel", id, cur.JiraTicket, tx.detail(ctx, cur, map[string]any{"from": cur.Status, "why": why})); err != nil {
			return err
		}
		// Deliberately not bumping the catalog: it carries what is running,
		// not where a release has got to, and "in flight" is read from this
		// table. Invalidating it here made one person's release a fan-out
		// across every upstream for everybody — see bumpCatalog.
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Decide records one approver's decision on an approving release. A rejection
// ends the release; an approval that completes the rule starts execution.
// started reports that the release moved to executing.
func (r *Releases) Decide(ctx context.Context, actor audit.Actor, id string, groups []string, approve bool, note string) (rel *release.Release, started bool, err error) {
	err = r.tx(ctx, func(tx *Releases) error {
		cur, err := tx.lock(ctx, id)
		if err != nil {
			return err
		}
		if cur.Status != release.Approving || cur.ApprovalRule == nil {
			return fmt.Errorf("%w: %s is not awaiting approval", release.ErrInvalidTransition, cur.Status)
		}
		var expired bool
		if err := tx.db.Raw(`SELECT approval_expires_at < now() FROM releases WHERE id = $1`, id).Scan(&expired).Error; err != nil {
			return err
		}
		if expired {
			return release.ErrApprovalExpired
		}
		switch {
		case actor.Sub == cur.CreatedBy:
			return release.ErrSelfApproval
		case !cur.ApprovalRule.Eligible(actor.Sub, groups, cur.CreatedBy):
			return release.ErrNotApprover
		}
		decision := "reject"
		if approve {
			decision = "approve"
		}
		res := tx.db.Exec(`INSERT INTO release_approvals (release_id, sub, name, decision, note) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (release_id, sub) DO NOTHING`, id, actor.Sub, actor.Name, decision, note)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return release.ErrAlreadyDecided
		}
		if !approve {
			if err := tx.db.Exec(`UPDATE release_items SET status = 'cancelled', finished_at = now()
				WHERE release_id = $1 AND status IN ('planned', 'pending')`, id).Error; err != nil {
				return err
			}
			if err := tx.db.Exec(`UPDATE releases SET status = 'rejected', finished_at = now() WHERE id = $1`, id).Error; err != nil {
				return err
			}
			if err := tx.audit.Write(ctx, actor, "release.reject", id, cur.JiraTicket, tx.detail(ctx, cur, map[string]any{"note": note})); err != nil {
				return err
			}
			// Nothing was deployed; see the note in Submit.
			return nil
		}
		var approved []string
		if err := tx.db.Raw(`SELECT sub FROM release_approvals WHERE release_id = $1 AND decision = 'approve'`, id).Scan(&approved).Error; err != nil {
			return err
		}
		done := cur.ApprovalRule.Satisfied(approved, cur.CreatedBy)
		if done {
			if err := tx.db.Exec(`UPDATE releases SET status = 'executing' WHERE id = $1`, id).Error; err != nil {
				return err
			}
			started = true
		}
		return tx.audit.Write(ctx, actor, "release.approve", id, cur.JiraTicket, tx.detail(ctx, cur, map[string]any{"note": note, "approvals": len(approved), "complete": done}))
	})
	if err != nil {
		return nil, false, err
	}
	rel, err = r.Get(ctx, id)
	return rel, started, err
}

// ExpireApprovals cancels releases whose approval window passed, freeing their
// targets, and returns the ones it cancelled.
func (r *Releases) ExpireApprovals(ctx context.Context) ([]*release.Release, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Raw(`SELECT id FROM releases WHERE status = 'approving' AND approval_expires_at < now()`).Scan(&ids).Error; err != nil {
		return nil, err
	}
	var out []*release.Release
	for _, id := range ids {
		rel, err := r.Cancel(ctx, audit.System, id, "approval expired")
		if err != nil && !errors.Is(err, release.ErrInvalidTransition) {
			return out, err
		}
		if rel != nil {
			out = append(out, rel)
		}
	}
	return out, nil
}

// ExpireConfirming cancels releases left in confirming past ttl so they
// release their targets, and returns the ones it cancelled.
func (r *Releases) ExpireConfirming(ctx context.Context, ttl time.Duration) ([]*release.Release, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Raw(`SELECT id FROM releases WHERE status = 'confirming' AND submitted_at < now() - make_interval(secs => $1)`,
		ttl.Seconds()).Scan(&ids).Error; err != nil {
		return nil, err
	}
	var out []*release.Release
	for _, id := range ids {
		rel, err := r.Cancel(ctx, audit.System, id, "confirmation expired")
		if err != nil && !errors.Is(err, release.ErrInvalidTransition) {
			return out, err
		}
		if rel != nil {
			out = append(out, rel)
		}
	}
	return out, nil
}

// StartItem claims a pending item before the executor calls upstream, so a
// crash between "promotion created" and "ref stored" is detectable rather than
// silently retried into a second promotion.
func (r *Releases) StartItem(ctx context.Context, itemID int64) (bool, error) {
	res := r.db.WithContext(ctx).Exec(`UPDATE release_items SET status = 'executing', started_at = now()
		WHERE id = $1 AND status = 'pending'`, itemID)
	return res.RowsAffected == 1, res.Error
}

// MarkExecuted stores the upstream reference (the Kargo Promotion name as
// rewritten by its webhook) and audits the execution.
func (r *Releases) MarkExecuted(ctx context.Context, item release.Item, ref string) error {
	return r.tx(ctx, func(tx *Releases) error {
		if err := tx.db.Exec(`UPDATE release_items SET external_ref = $2 WHERE id = $1`, item.ID, ref).Error; err != nil {
			return err
		}
		jira, err := tx.jira(item.ReleaseID)
		if err != nil {
			return err
		}
		detail := itemDetail(item)
		detail["externalRef"] = ref
		return tx.audit.Write(ctx, audit.System, "item.execute", item.ReleaseID, jira, detail)
	})
}

// FinishItem records a terminal item status and audits it.
func (r *Releases) FinishItem(ctx context.Context, item release.Item, status release.ItemStatus, errText string) error {
	if !release.CanTransitionItem(item.Status, status) {
		return fmt.Errorf("%w: item %s → %s", release.ErrInvalidTransition, item.Status, status)
	}
	return r.tx(ctx, func(tx *Releases) error {
		res := tx.db.Exec(`UPDATE release_items SET status = $2, error = NULLIF($3, ''), finished_at = now()
			WHERE id = $1 AND status = $4`, item.ID, status, errText, item.Status)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // another worker already moved it
		}
		jira, err := tx.jira(item.ReleaseID)
		if err != nil {
			return err
		}
		detail := itemDetail(item)
		detail["status"] = status
		if errText != "" {
			detail["error"] = errText
		}
		return tx.audit.Write(ctx, audit.System, "item."+string(status), item.ReleaseID, jira, detail)
	})
}

// Finish moves executing → succeeded/failed once every item is terminal.
func (r *Releases) Finish(ctx context.Context, id string, status release.Status) error {
	return r.tx(ctx, func(tx *Releases) error {
		cur, err := tx.lock(ctx, id)
		if err != nil {
			return err
		}
		if !release.CanTransition(cur.Status, status) {
			return fmt.Errorf("%w: %s → %s", release.ErrInvalidTransition, cur.Status, status)
		}
		if err := tx.db.Exec(`UPDATE releases SET status = $2, finished_at = now() WHERE id = $1`, id, status).Error; err != nil {
			return err
		}
		var rows []struct {
			Status string
			N      int
		}
		if err := tx.db.Raw(`SELECT status, count(*) AS n FROM release_items WHERE release_id = $1 GROUP BY status`, id).Scan(&rows).Error; err != nil {
			return err
		}
		counts := map[string]int{}
		for _, row := range rows {
			counts[row.Status] = row.N
		}
		if err := tx.audit.Write(ctx, audit.System, "release."+string(status), id, cur.JiraTicket, tx.detail(ctx, cur, map[string]any{"items": counts})); err != nil {
			return err
		}
		// The release is over: a new version is running and the targets are free.
		return tx.bumpCatalog(ctx)
	})
}

func (r *Releases) Get(ctx context.Context, id string) (*release.Release, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+releaseCols+` FROM releases WHERE id = $1`, id).Rows()
	if err != nil {
		return nil, err
	}
	list, err := scanReleases(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, release.ErrNotFound
	}
	if err := r.attachItems(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// List returns one page (newest first) with items attached, and the total.
func (r *Releases) List(ctx context.Context, f ReleaseFilter, page, pageSize int) ([]release.Release, int64, error) {
	where := []string{"1=1"}
	args := []any{}
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if len(f.Statuses) > 0 {
		st := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			st[i] = string(s)
		}
		where = append(where, "status = ANY("+next(st)+")")
	}
	if f.Env != "" {
		where = append(where, "env = "+next(f.Env))
	}
	if f.Service != "" {
		where = append(where, "EXISTS (SELECT 1 FROM release_items i WHERE i.release_id = r.id AND i.payload->>'service' = "+next(f.Service)+")")
	}
	if f.ServiceLike != "" {
		where = append(where, "EXISTS (SELECT 1 FROM release_items i WHERE i.release_id = r.id AND i.payload->>'service' ILIKE "+next(likePattern(f.ServiceLike))+` ESCAPE '\')`)
	}
	if f.Services != nil {
		where = append(where, "EXISTS (SELECT 1 FROM release_items i WHERE i.release_id = r.id AND i.payload->>'service' = ANY("+next(f.Services)+"))")
	}
	if f.Kind != "" {
		where = append(where, "EXISTS (SELECT 1 FROM release_items i WHERE i.release_id = r.id AND i.kind = "+next(f.Kind)+")")
	}
	if f.CreatedBy != "" {
		where = append(where, "created_by = "+next(f.CreatedBy))
	}
	if f.Creator != "" {
		pat := next(likePattern(f.Creator))
		where = append(where, "(created_by_name ILIKE "+pat+` ESCAPE '\' OR created_by ILIKE `+pat+` ESCAPE '\')`)
	}
	if f.DecidedBy != "" {
		where = append(where, "EXISTS (SELECT 1 FROM release_approvals a WHERE a.release_id = r.id AND a.sub = "+next(f.DecidedBy)+")")
	}
	if f.Since != nil {
		where = append(where, "created_at >= "+next(*f.Since))
	}
	if f.Until != nil {
		where = append(where, "created_at <= "+next(*f.Until))
	}
	if f.Jira != "" {
		where = append(where, "jira_ticket ILIKE "+next(likePattern(f.Jira))+` ESCAPE '\'`)
	}
	cond := strings.Join(where, " AND ")
	db := r.db.WithContext(ctx)
	var total int64
	if err := db.Raw(`SELECT count(*) FROM releases r WHERE `+cond, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	limit, offset := next(pageSize), next((page-1)*pageSize)
	rows, err := db.Raw(`SELECT `+releaseCols+` FROM releases r WHERE `+cond+
		` ORDER BY created_at DESC, id DESC LIMIT `+limit+` OFFSET `+offset, args...).Rows()
	if err != nil {
		return nil, 0, err
	}
	list, err := scanReleases(rows)
	if err != nil {
		return nil, 0, err
	}
	return list, total, r.attachItems(ctx, list)
}

// Executing returns all executing releases with items: the executor's queue.
func (r *Releases) Executing(ctx context.Context) ([]release.Release, error) {
	list, _, err := r.List(ctx, ReleaseFilter{Statuses: []release.Status{release.Executing}}, 1, 1000)
	return list, err
}

func (r *Releases) Items(ctx context.Context, releaseID string) ([]release.Item, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+itemCols+` FROM release_items WHERE release_id = $1 ORDER BY sequence, id`, releaseID).Rows()
	if err != nil {
		return nil, err
	}
	return scanItems(rows)
}

// EverDeployed reports whether Tide ever succeeded an image item for
// service+env, one input to first-deploy detection.
func (r *Releases) EverDeployed(ctx context.Context, service, env string) (bool, error) {
	var ok bool
	err := r.db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM release_items WHERE kind = 'image'
		AND payload->>'service' = $1 AND payload->>'env' = $2 AND status = 'succeeded')`, service, env).Scan(&ok).Error
	return ok, err
}

// CountToday returns today's (Asia/Shanghai) releases and how many succeeded.
// CountToday counts today's releases (Asia/Shanghai). services limits the
// count to releases touching them; nil counts everything, an empty list
// counts nothing — the same convention as ReleaseFilter.Services.
func (r *Releases) CountToday(ctx context.Context, services []string) (total, succeeded int64, err error) {
	var row struct{ Total, Succeeded int64 }
	q := `SELECT count(*) AS total, count(*) FILTER (WHERE status = 'succeeded') AS succeeded
		FROM releases r WHERE created_at >= date_trunc('day', now() AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai'`
	args := []any{}
	if services != nil {
		// IN (?) so gorm expands the slice; an empty one matches nothing.
		q += ` AND EXISTS (SELECT 1 FROM release_items i WHERE i.release_id = r.id AND i.payload->>'service' IN (?))`
		args = append(args, services)
	}
	err = r.db.WithContext(ctx).Raw(q, args...).Scan(&row).Error
	return row.Total, row.Succeeded, err
}

func (r *Releases) tx(ctx context.Context, fn func(tx *Releases) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Releases{db: tx, audit: &Audit{db: tx}})
	})
}

func (r *Releases) lock(ctx context.Context, id string) (*release.Release, error) {
	var row struct {
		ID, Env, JiraTicket, CreatedBy string
		Status                         release.Status
		ApprovalRule                   []byte
	}
	res := r.db.WithContext(ctx).Raw(`SELECT id, env, jira_ticket, created_by, status, approval_rule FROM releases WHERE id = $1 FOR UPDATE`, id).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, release.ErrNotFound
	}
	rel := &release.Release{ID: row.ID, Env: row.Env, JiraTicket: row.JiraTicket, CreatedBy: row.CreatedBy, Status: row.Status}
	if len(row.ApprovalRule) > 0 {
		rel.ApprovalRule = &release.ApprovalRule{}
		if err := json.Unmarshal(row.ApprovalRule, rel.ApprovalRule); err != nil {
			return nil, fmt.Errorf("release %s approval rule: %w", row.ID, err)
		}
	}
	return rel, nil
}

func (r *Releases) jira(releaseID string) (string, error) {
	var jira string
	err := r.db.Raw(`SELECT jira_ticket FROM releases WHERE id = $1`, releaseID).Scan(&jira).Error
	return jira, err
}

func (r *Releases) attachItems(ctx context.Context, list []release.Release) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	idx := map[string]int{}
	for i := range list {
		ids[i] = list[i].ID
		idx[list[i].ID] = i
	}
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+itemCols+` FROM release_items WHERE release_id = ANY($1) ORDER BY sequence, id`, ids).Rows()
	if err != nil {
		return err
	}
	items, err := scanItems(rows)
	if err != nil {
		return err
	}
	for _, it := range items {
		list[idx[it.ReleaseID]].Items = append(list[idx[it.ReleaseID]].Items, it)
	}
	arows, err := r.db.WithContext(ctx).Raw(`SELECT release_id, sub, name, decision, note, decided_at FROM release_approvals
		WHERE release_id = ANY($1) ORDER BY decided_at, id`, ids).Rows()
	if err != nil {
		return err
	}
	defer func() { _ = arows.Close() }()
	for arows.Next() {
		var rid string
		var a release.Approval
		if err := arows.Scan(&rid, &a.Sub, &a.Name, &a.Decision, &a.Note, &a.DecidedAt); err != nil {
			return err
		}
		list[idx[rid]].Approvals = append(list[idx[rid]].Approvals, a)
	}
	return arows.Err()
}

func itemDetail(item release.Item) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(item.Payload, &p)
	if p == nil {
		p = map[string]any{}
	}
	p["itemId"] = item.ID
	p["externalRef"] = item.ExternalRef
	return p
}

const releaseCols = `id, title, env, source, jira_ticket, reason, created_by, created_by_name, status, submitted_at,
	confirmed_at, COALESCE(confirmed_by, ''), created_at, finished_at, now(), approval_rule, approval_expires_at`

const itemCols = `id, release_id, kind, sequence, payload, status, COALESCE(external_ref, ''),
	COALESCE(error, ''), started_at, finished_at`

func scanReleases(rows *sql.Rows) ([]release.Release, error) {
	defer func() { _ = rows.Close() }()
	out := []release.Release{}
	for rows.Next() {
		var x release.Release
		var rule []byte
		if err := rows.Scan(&x.ID, &x.Title, &x.Env, &x.Source, &x.JiraTicket, &x.Reason, &x.CreatedBy, &x.CreatedByName, &x.Status,
			&x.SubmittedAt, &x.ConfirmedAt, &x.ConfirmedBy, &x.CreatedAt, &x.FinishedAt, &x.Now, &rule, &x.ApprovalExpiresAt); err != nil {
			return nil, err
		}
		if len(rule) > 0 {
			x.ApprovalRule = &release.ApprovalRule{}
			if err := json.Unmarshal(rule, x.ApprovalRule); err != nil {
				return nil, fmt.Errorf("release %s approval rule: %w", x.ID, err)
			}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func scanItems(rows *sql.Rows) ([]release.Item, error) {
	defer func() { _ = rows.Close() }()
	out := []release.Item{}
	for rows.Next() {
		var it release.Item
		var payload []byte
		if err := rows.Scan(&it.ID, &it.ReleaseID, &it.Kind, &it.Sequence, &payload, &it.Status, &it.ExternalRef,
			&it.Error, &it.StartedAt, &it.FinishedAt); err != nil {
			return nil, err
		}
		it.Payload = payload
		out = append(out, it)
	}
	return out, rows.Err()
}

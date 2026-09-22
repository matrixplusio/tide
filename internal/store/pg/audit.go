package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"tide/internal/audit"
)

type Audit struct {
	db *gorm.DB
}

// Write appends one record. Request id and client IP come from ctx (set by
// the HTTP layer), so callers never pass them by hand.
func (a *Audit) Write(ctx context.Context, actor audit.Actor, action, target, jira string, detail any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("audit: marshal detail: %w", err)
	}
	m := audit.MetaFrom(ctx)
	return a.db.WithContext(ctx).Exec(`
		INSERT INTO audit_log (actor, actor_name, action, target, jira_ticket, detail, request_id, client_ip)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6::jsonb, NULLIF($7, ''), NULLIF($8, ''))`,
		actor.Sub, actor.Name, action, target, jira, string(b), m.RequestID, m.ClientIP).Error
}

// List returns one page, newest first, and the total count for the filter.
func (a *Audit) List(ctx context.Context, f audit.Filter, page, pageSize int) ([]audit.Entry, int64, error) {
	where := []string{"1=1"}
	args := []any{}
	// Sequential "?" placeholders only: gorm binds them in order. Numbered
	// ones ($1, $2) silently lose their arguments once a query has more than
	// one of them, which used to break filtering by service and env together.
	add := func(cond string, vs ...any) {
		args = append(args, vs...)
		where = append(where, cond)
	}
	if f.Jira != "" {
		add(`jira_ticket ILIKE ? ESCAPE '\'`, likePattern(f.Jira))
	}
	if f.Target != "" {
		add(`target = ?`, f.Target)
	}
	if f.Actor != "" {
		add(`(actor = ? OR actor_name ILIKE ? ESCAPE '\')`, f.Actor, likePattern(f.Actor))
	}
	if f.ActorSub != "" {
		add(`actor = ?`, f.ActorSub)
	}
	if f.Services != nil {
		if len(f.Services) == 0 {
			where = append(where, "false")
		} else {
			list := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(f.Services)), ", ") + ")"
			vs := make([]any, 0, len(f.Services)*2)
			for range 2 {
				for _, name := range f.Services {
					vs = append(vs, name)
				}
			}
			// detail->'items' is an array on batch releases and absent
			// elsewhere; a few actions store an object there, so check first.
			add(`(detail->>'service' IN `+list+` OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(detail->'items') = 'array'
					THEN detail->'items' ELSE '[]'::jsonb END) i WHERE i->>'service' IN `+list+`)
				OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(detail->'services') = 'array'
					THEN detail->'services' ELSE '[]'::jsonb END) n WHERE n IN `+list+`))`, append(vs, vs[:len(f.Services)]...)...)
		}
	}
	if f.Action != "" {
		add(`action LIKE ? ESCAPE '\'`, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Action)+"%")
	}
	if f.Env != "" {
		add(`(detail->>'env' = ? OR detail @> jsonb_build_object('items', jsonb_build_array(jsonb_build_object('env', ?::text))))`, f.Env, f.Env)
	}
	if f.Service != "" {
		// Three shapes carry a service: an item record's own payload, a batch
		// release's items array, and the services list every release-level
		// record now writes.
		add(`(detail->>'service' = ?
			OR detail @> jsonb_build_object('items', jsonb_build_array(jsonb_build_object('service', ?::text)))
			OR detail->'services' @> to_jsonb(?::text))`, f.Service, f.Service, f.Service)
	}
	if f.Since != nil {
		add(`at >= ?`, *f.Since)
	}
	if f.Until != nil {
		add(`at <= ?`, *f.Until)
	}
	cond := strings.Join(where, " AND ")
	db := a.db.WithContext(ctx)

	var total int64
	if err := db.Raw(`SELECT count(*) FROM audit_log WHERE `+cond, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := db.Raw(`
		SELECT id, at, actor, actor_name, action, COALESCE(target, ''), COALESCE(jira_ticket, ''), detail,
		       COALESCE(request_id, ''), COALESCE(client_ip, '')
		FROM audit_log WHERE `+cond+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...).Rows()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := []audit.Entry{}
	for rows.Next() {
		var e audit.Entry
		var detail []byte
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.ActorName, &e.Action, &e.Target, &e.JiraTicket, &detail, &e.RequestID, &e.ClientIP); err != nil {
			return nil, 0, err
		}
		e.Detail = detail
		out = append(out, e)
	}
	return out, total, rows.Err()
}

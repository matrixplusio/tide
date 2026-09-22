// Package audit defines the append-only audit record. Writing and querying
// live in internal/store/pg; there is deliberately no update or delete, and
// the runtime database account could not run one anyway (see migration grants).
package audit

import (
	"context"
	"encoding/json"
	"time"
)

type Actor struct {
	Sub  string `json:"sub"`
	Name string `json:"name"`
}

// System is the actor for things Tide does on its own (executor, expiry).
var System = Actor{Sub: "system:tide", Name: "Tide"}

type Entry struct {
	ID         int64           `json:"id"`
	At         time.Time       `json:"at"`
	Actor      string          `json:"actor"`
	ActorName  string          `json:"actorName"`
	Action     string          `json:"action"`
	Target     string          `json:"target,omitempty"`
	JiraTicket string          `json:"jiraTicket,omitempty"`
	Detail     json.RawMessage `json:"detail"`
	RequestID  string          `json:"requestId,omitempty"`
	ClientIP   string          `json:"clientIp,omitempty"`
}

type Filter struct {
	Jira   string
	Target string
	Actor  string
	// ActorSub matches the actor exactly (Actor also matches names).
	ActorSub string
	Action   string
	Env      string
	Service  string
	// Services, when non-nil, keeps only records about these services: what
	// a viewer whose audit permission is limited to some projects may read.
	// An empty (non-nil) list matches nothing.
	Services []string
	Since    *time.Time
	Until    *time.Time
}

// Meta is request context attached to every record written during a request.
type Meta struct {
	RequestID string
	ClientIP  string
	UserAgent string // not stored in audit records; used for session listings
}

type metaKey struct{}

func WithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

func MetaFrom(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey{}).(Meta)
	return m
}

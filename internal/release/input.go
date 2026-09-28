package release

import (
	"tide/internal/i18n"

	"encoding/json"
	"fmt"
)

type ItemInput struct {
	Kind     string          `json:"kind"`
	Sequence int             `json:"sequence"`
	Payload  json.RawMessage `json:"payload"`
}

type CreateInput struct {
	Title      string
	Env        string
	JiraTicket string
	Reason     string
	// Source is SourceUI (default) or SourceCI.
	Source string
	Items  []ItemInput
}

// Validate is the domain's last line of defence; the HTTP layer validates
// user-facing fields first with friendlier messages.
func (in CreateInput) Validate() error {
	switch {
	case in.Env == "":
		return fmt.Errorf("%w: env is required", ErrInvalid)
	case !ValidOptionalJira(in.JiraTicket):
		return fmt.Errorf("%w: malformed jira ticket", ErrInvalid)
	case len(in.Items) == 0:
		return fmt.Errorf("%w: at least one item is required", ErrInvalid)
	}
	for i, it := range in.Items {
		var service, env string
		switch it.Kind {
		case KindImage:
			var p ImagePayload
			if err := json.Unmarshal(it.Payload, &p); err != nil {
				return fmt.Errorf("%w: item %d: %w", ErrInvalid, i, err)
			}
			if p.Freight == "" || p.To.Digest == "" {
				return fmt.Errorf("%w: item %d: freight and target digest are required", ErrInvalid, i)
			}
			service, env = p.Service, p.Env
		case KindRestart:
			var p RestartPayload
			if err := json.Unmarshal(it.Payload, &p); err != nil {
				return fmt.Errorf("%w: item %d: %w", ErrInvalid, i, err)
			}
			if p.App == "" || len(p.Workloads) == 0 {
				return fmt.Errorf("%w: item %d: application and workloads are required", ErrInvalid, i)
			}
			service, env = p.Service, p.Env
		case KindSync:
			var p SyncPayload
			if err := json.Unmarshal(it.Payload, &p); err != nil {
				return fmt.Errorf("%w: item %d: %w", ErrInvalid, i, err)
			}
			if p.App == "" || p.Revision == "" || len(p.Changes) == 0 {
				return fmt.Errorf("%w: item %d: application, revision and changes are required", ErrInvalid, i)
			}
			service, env = p.Service, p.Env
		case KindScale:
			var p ScalePayload
			if err := json.Unmarshal(it.Payload, &p); err != nil {
				return fmt.Errorf("%w: item %d: %w", ErrInvalid, i, err)
			}
			if p.App == "" || p.Path == "" {
				return fmt.Errorf("%w: item %d: application and manifest path are required", ErrInvalid, i)
			}
			if p.To < 0 {
				return fmt.Errorf("%w: item %d: a replica count cannot be negative", ErrInvalid, i)
			}
			if p.To > MaxReplicas {
				return fmt.Errorf("%w: item %d: %d replicas is beyond the limit of %d", ErrInvalid, i, p.To, MaxReplicas)
			}
			if p.From == p.To {
				return fmt.Errorf("%w: item %d: already at %d replicas", ErrInvalid, i, p.To)
			}
			service, env = p.Service, p.Env
		default:
			return fmt.Errorf("%w: item %d: unsupported kind %q", ErrInvalid, i, it.Kind)
		}
		if service == "" {
			return fmt.Errorf("%w: item %d: service is required", ErrInvalid, i)
		}
		if env != in.Env {
			return fmt.Errorf("%w: item %d targets %q but release targets %q", ErrInvalid, i, env, in.Env)
		}
	}
	return nil
}

// MaxReplicas bounds what a release may ask for. Not a cluster limit — the
// cluster has its own, and it answers with pods that never schedule — but a
// limit on typing mistakes: a stray digit turning 3 into 300 is the kind of
// change that is only noticed by what it starves.
const MaxReplicas = 50

func (in CreateInput) DefaultTitle() string {
	var p struct {
		Service string `json:"service"`
	}
	_ = json.Unmarshal(in.Items[0].Payload, &p)
	subject := p.Service
	if len(in.Items) > 1 {
		subject = i18n.T(i18n.Default, "pl.andNServices", p.Service, len(in.Items))
	}
	switch in.Items[0].Kind {
	case KindRestart:
		return i18n.T(i18n.Default, "pl.titleRestart", withSpace(subject, len(in.Items) == 1), in.Env)
	case KindSync:
		return i18n.T(i18n.Default, "pl.titleSync", withSpace(subject, len(in.Items) == 1), in.Env)
	case KindScale:
		var sp ScalePayload
		_ = json.Unmarshal(in.Items[0].Payload, &sp)
		return i18n.T(i18n.Default, "pl.titleScale", withSpace(subject, len(in.Items) == 1), sp.To, in.Env)
	}
	return fmt.Sprintf("%s → %s", subject, in.Env)
}

// withSpace separates a Latin service name from the following Chinese word;
// A subject like "svc and N services" already ends in a word.
func withSpace(s string, latinEnd bool) string {
	if latinEnd {
		return s + " "
	}
	return s
}

// Package release holds the release model, its state machine and persistence.
package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Status string

const (
	Draft      Status = "draft"
	Confirming Status = "confirming"
	Approving  Status = "approving"
	Executing  Status = "executing"
	Succeeded  Status = "succeeded"
	Failed     Status = "failed"
	Cancelled  Status = "cancelled"
	Rejected   Status = "rejected"
)

// transitions is the whole state machine. failed is terminal on purpose: after a
// failure the cluster has changed, so a retry must be a new release built from
// fresh state, not the old one re-run with stale context.
var transitions = map[Status][]Status{
	Draft:      {Confirming, Cancelled},
	Confirming: {Executing, Approving, Cancelled},
	Approving:  {Executing, Rejected, Cancelled},
	Executing:  {Succeeded, Failed},
}

func CanTransition(from, to Status) bool { return slices.Contains(transitions[from], to) }

func (s Status) Terminal() bool { return len(transitions[s]) == 0 }

type ItemStatus string

const (
	ItemPlanned   ItemStatus = "planned"
	ItemPending   ItemStatus = "pending"
	ItemExecuting ItemStatus = "executing"
	ItemSucceeded ItemStatus = "succeeded"
	ItemFailed    ItemStatus = "failed"
	ItemSkipped   ItemStatus = "skipped"
	ItemCancelled ItemStatus = "cancelled"
)

var itemTransitions = map[ItemStatus][]ItemStatus{
	ItemPlanned:   {ItemPending, ItemCancelled},
	ItemPending:   {ItemExecuting, ItemSkipped, ItemCancelled, ItemFailed},
	ItemExecuting: {ItemSucceeded, ItemFailed},
}

func CanTransitionItem(from, to ItemStatus) bool { return slices.Contains(itemTransitions[from], to) }

func (s ItemStatus) Terminal() bool { return len(itemTransitions[s]) == 0 }

// Confirmation read time, confirmation lifetime and execution timeout are
// release policy settings (settings.ReleasePolicy). The read time is enforced
// server side against the database clock and never goes below 10 seconds; a
// confirming release holds its targets, so the lifetime is bounded too.

var (
	ErrNotFound          = errors.New("release not found")
	ErrInvalidTransition = errors.New("invalid status transition")
	ErrTooEarly          = errors.New("confirmation window has not elapsed")
	ErrConfirmExpired    = errors.New("confirmation expired, cancel and create a new release")
	ErrApprovalExpired   = errors.New("approval window has passed")
	ErrSelfApproval      = errors.New("the creator cannot approve their own release")
	ErrNotApprover       = errors.New("not an approver for this release")
	ErrAlreadyDecided    = errors.New("already decided on this release")
	ErrTargetBusy        = errors.New("another release is already in flight for this service and environment")
	ErrInvalid           = errors.New("invalid release")
	ErrFrozen            = errors.New("environment is in a change freeze")
)

// FrozenError refuses a release during a change freeze window.
type FrozenError struct {
	Env    string
	Name   string
	Until  time.Time
	Reason string
}

func (e *FrozenError) Error() string {
	return fmt.Sprintf("%s is frozen by %q until %s", e.Env, e.Name, e.Until.Format(time.RFC3339))
}

func (e *FrozenError) Unwrap() error { return ErrFrozen }

// JiraRe is the one rule for ticket ids, shared with the HTTP layer.
var JiraRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]+-[0-9]+$`)

func ValidJira(s string) bool { return JiraRe.MatchString(s) }

// ValidOptionalJira accepts an empty ticket; whether one is required is a
// release policy decision made per environment.
func ValidOptionalJira(s string) bool { return s == "" || ValidJira(s) }

// Where a release came from. A CI release has no human creator, which
// changes who may confirm it (see the API's confirm handler).
const (
	SourceUI = "ui"
	SourceCI = "ci"
)

type Release struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Env           string     `json:"env"`
	Source        string     `json:"source"`
	JiraTicket    string     `json:"jiraTicket"`
	Reason        string     `json:"reason"`
	CreatedBy     string     `json:"createdBy"`
	CreatedByName string     `json:"createdByName"`
	Status        Status     `json:"status"`
	SubmittedAt   *time.Time `json:"submittedAt,omitempty"`
	ConfirmedAt   *time.Time `json:"confirmedAt,omitempty"`
	ConfirmedBy   string     `json:"confirmedBy,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
	Items         []Item     `json:"items,omitempty"`
	// ApprovalRule is the rule snapshot taken at confirmation (nil when the
	// environment needed no approval); Approvals are the decisions so far.
	ApprovalRule      *ApprovalRule `json:"approvalRule,omitempty"`
	ApprovalExpiresAt *time.Time    `json:"approvalExpiresAt,omitempty"`
	Approvals         []Approval    `json:"approvals,omitempty"`
	// Now is the database clock at read time, so clients can render the
	// confirmation countdown without trusting their own clock.
	Now time.Time `json:"now"`
}

// Automatic reports that nobody let this release through: CI created it and
// the same token "confirmed" it, which only Releases.Start does. A person
// confirming a CI release is recorded under their own subject, so the two
// never look alike.
//
// This is derived rather than stored, and it holds only while a CI token
// cannot reach the confirm endpoint — the token authenticates on one route
// and that route creates intakes, nothing else.
func (r *Release) Automatic() bool {
	return r.Source == SourceCI && r.ConfirmedBy != "" && r.ConfirmedBy == r.CreatedBy
}

type Item struct {
	ID          int64           `json:"id"`
	ReleaseID   string          `json:"releaseId"`
	Kind        string          `json:"kind"`
	Sequence    int             `json:"sequence"`
	Payload     json.RawMessage `json:"payload"`
	Status      ItemStatus      `json:"status"`
	ExternalRef string          `json:"externalRef,omitempty"`
	Error       string          `json:"error,omitempty"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	FinishedAt  *time.Time      `json:"finishedAt,omitempty"`
}

// Artifact pins an image by digest. Tag and version are for humans only.
type Artifact struct {
	Digest  string     `json:"digest"`
	Tag     string     `json:"tag"`
	Version string     `json:"version,omitempty"`
	BuiltAt *time.Time `json:"builtAt,omitempty"`
}

// ImagePayload is the payload of kind=image. from/to are stored in full
// because Kargo garbage-collects Freight and the audit trail must outlive it.
type ImagePayload struct {
	Upstream string `json:"upstream"`
	Project  string `json:"project"`
	Stage    string `json:"stage"`
	Service  string `json:"service"`
	Env      string `json:"env"`
	// App is the Argo CD Application whose workloads must roll out before the
	// item succeeds. Empty on releases created before it was recorded.
	App       string    `json:"app,omitempty"`
	Freight   string    `json:"freight"`
	Image     string    `json:"image"`
	From      *Artifact `json:"from"` // nil on first deploy
	To        Artifact  `json:"to"`
	Anomalies []Anomaly `json:"anomalies,omitempty"`
	// ConfigChanges are git changes Argo CD had not applied when the release
	// was built. Kargo syncs the whole Application, so they go out with the
	// image; WithConfig records that the creator chose that explicitly.
	ConfigChanges []ResourceChange `json:"configChanges,omitempty"`
	WithConfig    bool             `json:"withConfig,omitempty"`
	// Verified is the cross-site verification the environment requires
	// (Environment.PromotesFrom); re-checked before execution.
	Verified *SourceVerification `json:"verified,omitempty"`
}

// SourceVerification: Digest was verified in Stage of Project on Upstream,
// the stage serving environment Env.
type SourceVerification struct {
	Env        string     `json:"env"`
	Upstream   string     `json:"upstream"`
	Project    string     `json:"project"`
	Stage      string     `json:"stage"`
	Digest     string     `json:"digest"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
}

// Anomaly codes. Each one stands for exactly one sentence, which is what
// lets a reader be shown that sentence in their own language: two variants
// sharing a code would collapse into whichever the catalogue happened to
// hold.
const (
	// AnomalyConfigDrift: an upgrade would also apply unsynced git changes.
	AnomalyConfigDrift = "config_drift"
	// AnomalyFirstDeploy: Kargo has never put anything in this environment.
	AnomalyFirstDeploy = "first_deploy"
	// AnomalyFirstDeployPerKargo: the same, except Tide has deployed here
	// before — worth saying, because it means the two disagree.
	AnomalyFirstDeployPerKargo = "first_deploy_per_kargo"
)

type Anomaly struct {
	Code string `json:"code"` // rollback / first_deploy / multi_version_jump / short_soak
	// Message is the sentence as it was put to whoever confirmed the release,
	// in the language they were working in. It stays exactly as written: it
	// is what they read before deciding, which is the point of recording it.
	Message string `json:"message"`
	// Args are the same sentence's facts, kept apart from its wording so a
	// reader in another language can be shown it in theirs. Without them the
	// audit record is the only text there is, and a fleet whose people do not
	// share one language gets a release list written half in each.
	//
	// Empty on releases created before this existed; Message is then the only
	// thing to show, which is what it was always for.
	Args []string `json:"args,omitempty"`
}

const (
	KindImage   = "image"
	KindRestart = "restart"
	KindSync    = "sync"
)

// Resource change actions, from the cluster's point of view.
const (
	ChangeCreate = "create"
	ChangeUpdate = "update"
	ChangeDelete = "delete"
)

// ResourceChange is one resource a sync would create, update or delete, with
// a unified diff of its manifest (Secret values are masked by Argo CD).
type ResourceChange struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Diff      string `json:"diff,omitempty"`
	// Truncated: the diff was cut to keep the release row small.
	Truncated bool `json:"truncated,omitempty"`
}

// SyncPayload is the payload of kind=sync: apply configuration committed to
// git (anything but the image Kargo manages) by syncing the Argo CD
// Application to the revision the person reviewed.
type SyncPayload struct {
	Upstream string `json:"upstream"`
	Service  string `json:"service"`
	Env      string `json:"env"`
	App      string `json:"app"`
	// Project and Stage are set when Kargo manages the environment: a sync
	// must not overlap a promotion.
	Project  string `json:"project,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Revision string `json:"revision"`
	// Current is the version running when the release was built; a sync
	// keeps it.
	Current Artifact         `json:"current"`
	Changes []ResourceChange `json:"changes"`
	// Prune allows deleting resources git no longer renders.
	Prune bool `json:"prune,omitempty"`
	// Restart rolls the workloads after the sync even when their pod
	// template did not change (a ConfigMap or Secret with a fixed name).
	Restart   bool       `json:"restart,omitempty"`
	Workloads []Workload `json:"workloads,omitempty"`
}

// SyncPayload decodes a sync item's payload.
func (r *Release) SyncPayload(it Item) (*SyncPayload, error) {
	if it.Kind != KindSync {
		return nil, fmt.Errorf("item %d is %q, not sync", it.ID, it.Kind)
	}
	var p SyncPayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// RestartPayload is the payload of kind=restart: a rolling restart of an Argo
// CD Application's workloads at their current version. Nothing is promoted;
// Current records what was running when the release was built.
type RestartPayload struct {
	Upstream string `json:"upstream"`
	Service  string `json:"service"`
	Env      string `json:"env"`
	App      string `json:"app"`
	// Project and Stage are set when Kargo manages the environment: a restart
	// must not overlap a promotion (dev may auto-promote at any time).
	Project   string     `json:"project,omitempty"`
	Stage     string     `json:"stage,omitempty"`
	Current   Artifact   `json:"current"`
	Workloads []Workload `json:"workloads"`
}

// Workload is a restartable resource managed by the Application.
type Workload struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"` // Deployment / StatefulSet / DaemonSet
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// RestartPayload decodes a restart item's payload.
func (r *Release) RestartPayload(it Item) (*RestartPayload, error) {
	if it.Kind != KindRestart {
		return nil, fmt.Errorf("item %d is %q, not restart", it.ID, it.Kind)
	}
	var p RestartPayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Target is what every item kind shares: the service and environment it
// changes, and the digest the confirmation must echo ("" when unknown).
type Target struct {
	Upstream string
	Service  string
	Env      string
	Digest   string
}

func (r *Release) Target(it Item) (Target, error) {
	switch it.Kind {
	case KindImage:
		p, err := r.ImagePayload(it)
		if err != nil {
			return Target{}, err
		}
		return Target{Upstream: p.Upstream, Service: p.Service, Env: p.Env, Digest: p.To.Digest}, nil
	case KindRestart:
		p, err := r.RestartPayload(it)
		if err != nil {
			return Target{}, err
		}
		return Target{Upstream: p.Upstream, Service: p.Service, Env: p.Env, Digest: p.Current.Digest}, nil
	case KindSync:
		p, err := r.SyncPayload(it)
		if err != nil {
			return Target{}, err
		}
		return Target{Upstream: p.Upstream, Service: p.Service, Env: p.Env, Digest: p.Current.Digest}, nil
	}
	return Target{}, fmt.Errorf("item %d: unsupported kind %q", it.ID, it.Kind)
}

// ImagePayload decodes an image item's payload.
func (r *Release) ImagePayload(it Item) (*ImagePayload, error) {
	if it.Kind != KindImage {
		return nil, fmt.Errorf("item %d is %q, not image", it.ID, it.Kind)
	}
	var p ImagePayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Approval modes.
const (
	ApproveAny   = "any"   // one approver
	ApproveAll   = "all"   // every listed user
	ApproveCount = "count" // at least MinApprovals approvers
)

// ApprovalRule says who must approve a release and how many of them.
// Approvers are subject selectors: user:<sub> or group:<name>.
type ApprovalRule struct {
	Name           string   `json:"name"`
	Approvers      []string `json:"approvers"`
	Mode           string   `json:"mode"`
	MinApprovals   int      `json:"minApprovals"`
	TimeoutMinutes int      `json:"timeoutMinutes"`
}

type Approval struct {
	Sub       string    `json:"sub"`
	Name      string    `json:"name"`
	Decision  string    `json:"decision"` // approve / reject
	Note      string    `json:"note,omitempty"`
	DecidedAt time.Time `json:"decidedAt"`
}

// Eligible reports whether a person (sub + groups) may decide under the rule.
// The creator never may.
func (r ApprovalRule) Eligible(sub string, groups []string, creator string) bool {
	if sub == creator {
		return false
	}
	for _, a := range r.Approvers {
		switch {
		case strings.HasPrefix(a, "user:") && a[len("user:"):] == sub:
			return true
		case strings.HasPrefix(a, "group:") && slices.Contains(groups, a[len("group:"):]):
			return true
		}
	}
	return false
}

// Satisfied reports whether the approvals given (subs of people who
// approved) complete the rule for a release created by creator.
func (r ApprovalRule) Satisfied(approved []string, creator string) bool {
	switch r.Mode {
	case ApproveAll:
		n := 0
		for _, a := range r.Approvers {
			if u, ok := strings.CutPrefix(a, "user:"); ok && u != creator {
				if !slices.Contains(approved, u) {
					return false
				}
				n++
			}
		}
		return n > 0
	case ApproveCount:
		return len(approved) >= max(r.MinApprovals, 1)
	default:
		return len(approved) >= 1
	}
}

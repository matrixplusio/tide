// Package insights answers questions about the release process itself:
// who releases what, how often it fails, and — the part worth having —
// whether the safeguards Tide puts in the way are doing anything.
//
// Everything here is derived from releases, release_items and
// release_approvals. There are no counters to keep up to date and nothing
// to instrument: the records a release already leaves behind are the source.
// That also means the numbers survive restarts and can be asked about any
// period, which is exactly where Prometheus would not help.
package insights

import (
	"errors"
	"time"
)

// Sentinel errors: the caller turns these into field errors, so this package
// stays free of the request layer.
var (
	ErrRangeOrder   = errors.New("insights: range start is not before its end")
	ErrRangeTooWide = errors.New("insights: range is wider than the maximum")
)

// MaxRange caps how far back one query may look. Aggregates scan the period,
// so an unbounded range is a way to make the database do arbitrary work.
const MaxRange = 366 * 24 * time.Hour

// DefaultRange is used when the caller names neither end.
const DefaultRange = 90 * 24 * time.Hour

// Range is the period a report covers, always both ends resolved.
type Range struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// NewRange resolves a requested period: an open end means "now", an open
// start means DefaultRange before the end.
func NewRange(from, to *time.Time, now time.Time) (Range, error) {
	r := Range{To: now}
	if to != nil {
		r.To = *to
	}
	r.From = r.To.Add(-DefaultRange)
	if from != nil {
		r.From = *from
	}
	if !r.From.Before(r.To) {
		return Range{}, ErrRangeOrder
	}
	if r.To.Sub(r.From) > MaxRange {
		return Range{}, ErrRangeTooWide
	}
	return r, nil
}

// Report is everything the insights page shows for one range and scope.
type Report struct {
	Range Range `json:"range"`
	// Totals counts the releases the range holds, before any grouping.
	Totals Totals `json:"totals"`
	// Process measures the safeguards: the reading countdown, the anomalies
	// raised on the confirmation sheet, approvals, and automatic releases.
	Process Process `json:"process"`
	// Activity is the plain counting: which services, which environments,
	// when, and by whom.
	Activity Activity `json:"activity"`
	// Risk holds the signals worth a second look rather than a number to
	// optimise.
	Risk Risk `json:"risk"`
}

type Totals struct {
	Releases  int64 `json:"releases"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
	Rejected  int64 `json:"rejected"`
	// InFlight is everything not in a terminal state right now.
	InFlight int64 `json:"inFlight"`
}

type Process struct {
	// ConfirmDwell buckets how long people actually spent on the confirmation
	// sheet. The countdown makes the button unusable for the first N seconds;
	// if nearly everyone confirms in the second right after, the sheet is
	// being waited out rather than read.
	ConfirmDwell []Bucket `json:"confirmDwell"`
	// ConfirmSeconds is the configured countdown, so the client can say what
	// the buckets are being compared against.
	ConfirmSeconds int `json:"confirmSeconds"`
	// Anomalies: of the releases whose confirmation sheet highlighted
	// something (a rollback, a version jump, a short soak, unsynced config),
	// how many went ahead anyway.
	Anomalies AnomalyStats `json:"anomalies"`
	// Approvals: a rule that is never used to say no is a delay, not a gate.
	Approvals ApprovalStats `json:"approvals"`
	// Sources compares releases a person started with releases a build
	// pipeline started. It is the only way to answer whether automation is
	// safer or riskier here.
	Sources []SourceStats `json:"sources"`
}

// Bucket is one bar of a distribution. Upper is exclusive; the last bucket
// carries Upper == 0 meaning "everything above the previous one".
type Bucket struct {
	Label string `json:"label"`
	Upper int    `json:"upper"`
	Count int64  `json:"count"`
}

type AnomalyStats struct {
	// WithAnomaly is how many confirmed releases had at least one item whose
	// plan carried an anomaly.
	WithAnomaly int64 `json:"withAnomaly"`
	// WentAhead is how many of those were confirmed rather than cancelled.
	WentAhead int64 `json:"wentAhead"`
	// ByCode breaks the anomalies down by kind.
	ByCode []CodeCount `json:"byCode"`
}

type CodeCount struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

type ApprovalStats struct {
	// Requested is how many releases entered approval.
	Requested int64 `json:"requested"`
	Approved  int64 `json:"approved"`
	Rejected  int64 `json:"rejected"`
	// Expired is approvals nobody decided in time.
	Expired int64 `json:"expired"`
	// MedianSeconds and P90Seconds measure waiting for a decision.
	MedianSeconds float64 `json:"medianSeconds"`
	P90Seconds    float64 `json:"p90Seconds"`
	// SelfConfirmed counts releases whose creator also confirmed them. It is
	// not wrong — most environments have no approval rule — but it says how
	// often a release passed through exactly one pair of hands.
	SelfConfirmed int64 `json:"selfConfirmed"`
}

type SourceStats struct {
	Source    string `json:"source"` // ui | ci
	Total     int64  `json:"total"`
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

type Activity struct {
	Services     []ServiceStats `json:"services"`
	Environments []EnvStats     `json:"environments"`
	Kinds        []KindStats    `json:"kinds"`
	// Weekly is a weekday × hour grid in the viewer's requested location,
	// for spotting releases that happen when nobody else is around.
	Weekly []Slot `json:"weekly"`
	// People is omitted for viewers who cannot see every environment, since
	// a partial workload figure is worse than none.
	People []PersonStats `json:"people,omitempty"`
}

type ServiceStats struct {
	Service   string `json:"service"`
	Project   string `json:"project,omitempty"`
	Total     int64  `json:"total"`
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

type EnvStats struct {
	Env       string `json:"env"`
	Total     int64  `json:"total"`
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

type KindStats struct {
	Kind      string `json:"kind"`
	Total     int64  `json:"total"`
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

type Slot struct {
	// Weekday is 0 = Sunday, matching PostgreSQL's dow.
	Weekday int   `json:"weekday"`
	Hour    int   `json:"hour"`
	Count   int64 `json:"count"`
}

// PersonStats is workload, not a leaderboard: how much of the releasing and
// approving a person carried. The client sorts by name, and a failure share
// only means something next to the number of releases it came from.
type PersonStats struct {
	Sub  string `json:"sub"`
	Name string `json:"name"`
	// Token marks a machine subject (a CI token, sub "ci:<id>"). Its releases
	// are real work, but nobody chose to make them one at a time, so they do
	// not belong in a comparison between people.
	Token     bool  `json:"token"`
	Created   int64 `json:"created"`
	Confirmed int64 `json:"confirmed"`
	Approved  int64 `json:"approved"`
	Rejected  int64 `json:"rejected"`
	Failed    int64 `json:"failed"`
}

type Risk struct {
	// Coverage answers whether Tide is actually the way things get released:
	// services in the catalog that no release in this range ever touched.
	Coverage Coverage `json:"coverage"`
	// Rollbacks counts releases whose target was older than what was running.
	Rollbacks int64 `json:"rollbacks"`
	// Repeats lists service + environment + day combinations released three
	// or more times — usually firefighting, sometimes debugging in a shared
	// environment.
	Repeats []Repeat `json:"repeats"`
	// JiraReuse lists tickets used by many releases. A ticket covering twenty
	// releases has stopped being a link to a decision.
	JiraReuse []JiraCount `json:"jiraReuse"`
	// FreezeBlocked counts attempts refused by a freeze window.
	FreezeBlocked int64 `json:"freezeBlocked"`
	// AfterHours is releases outside working hours, as a share of the total.
	AfterHours int64 `json:"afterHours"`
}

type Coverage struct {
	// Catalog is how many services the catalog lists (within what the viewer
	// may see); Released is how many of them this range covers.
	Catalog  int `json:"catalog"`
	Released int `json:"released"`
	// Untouched names the ones with no release, most recently created first.
	Untouched []string `json:"untouched"`
}

type Repeat struct {
	Service string `json:"service"`
	Env     string `json:"env"`
	Day     string `json:"day"` // YYYY-MM-DD in the requested location
	Count   int64  `json:"count"`
}

type JiraCount struct {
	Jira  string `json:"jira"`
	Count int64  `json:"count"`
}

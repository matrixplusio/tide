// Package plan answers "what can go to this env" and "what is unusual about
// this change", the inputs to the confirmation checklist.
package plan

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/upstream/kargo"
)

// ErrNotManaged: the Application has no Kargo authorized-stage annotation.
var ErrNotManaged = errors.New("environment is not managed by Kargo")

// Error explains why a chosen artifact cannot be released. It is shown to
// whoever tried, so it carries a catalog key and is rendered in their
// language at the edge rather than here.
type Error struct {
	Key  i18n.Key
	Args []any
}

func (e *Error) Error() string { return i18n.T(i18n.Default, e.Key, e.Args...) }

func errf(key i18n.Key, args ...any) *Error { return &Error{Key: key, Args: args} }

// at renders text that is stored on a release. Stored text is evidence: it
// reads the same for everyone afterwards, so it uses the deployment's
// language rather than the language of whoever happened to create it.
func at(key i18n.Key, args ...any) string { return i18n.T(i18n.Default, key, args...) }

type StageMark struct {
	Stage string     `json:"stage"`
	Since *time.Time `json:"since,omitempty"`
}

type Candidate struct {
	Freight    string      `json:"freight"`
	Alias      string      `json:"alias,omitempty"`
	Image      string      `json:"image"`
	Tag        string      `json:"tag"`
	Digest     string      `json:"digest"`
	Version    string      `json:"version,omitempty"`
	BuiltAt    *time.Time  `json:"builtAt,omitempty"`
	CreatedAt  *time.Time  `json:"createdAt,omitempty"`
	Available  bool        `json:"available"`
	Current    bool        `json:"current"`
	VerifiedIn []StageMark `json:"verifiedIn"`
	CurrentIn  []StageMark `json:"currentIn"`
}

type Candidates struct {
	Deployment     *catalog.Deployment `json:"deployment"`
	UpstreamStages []string            `json:"upstreamStages"`
	// Warehouses the stage takes freight from; Direct when at least one of
	// them feeds the stage straight from CI rather than through another stage.
	Warehouses     []string    `json:"warehouses"`
	Direct         bool        `json:"direct"`
	Items          []Candidate `json:"items"`
	AvailableCount int         `json:"availableCount"`
	TotalCount     int         `json:"totalCount"`
	// Gate is the cross-site verification applied, if any.
	Gate *Gate `json:"gate,omitempty"`
}

// List returns freight for service→env. By default only freight Kargo
// considers available to the stage (i.e. already verified upstream); all=true
// lists everything, with unavailable entries marked so they cannot be picked.
func List(ctx context.Context, hub *catalog.Hub, d *catalog.Deployment, gate *Gate, all bool) (*Candidates, error) {
	c, err := hub.Named(ctx, d.Upstream)
	if err != nil {
		return nil, err
	}
	if d.KargoProject == "" {
		return nil, ErrNotManaged
	}
	// One list per project rather than one fetch per item: fifty services in
	// a project were fifty requests for stages the same list already held.
	stage, err := memoFrom(ctx).Stage(ctx, c, d.Upstream, d.KargoProject, d.KargoStage)
	if err != nil {
		return nil, err
	}
	available, err := c.Kargo.QueryFreight(ctx, d.KargoProject, d.KargoStage)
	if err != nil {
		return nil, err
	}
	// Every item in a batch asks this same question — the project's whole
	// freight, with nothing service-specific in the request. Within one
	// request they share the answer; on their own they fetch it as before.
	everything, err := memoFrom(ctx).AllFreight(ctx, c, d.Upstream, d.KargoProject)
	if err != nil {
		return nil, err
	}
	// A Kargo project may hold several warehouses (e.g. a dev CI and a qa CI);
	// only freight this stage requests is relevant to it.
	available, everything = requested(stage, available), requested(stage, everything)
	avail := map[string]bool{}
	for _, f := range available {
		avail[f.Metadata.Name] = true
	}
	out := &Candidates{Deployment: d, UpstreamStages: stage.UpstreamStages(), AvailableCount: len(available), TotalCount: len(everything)}
	out.Warehouses, out.Direct = stage.Warehouses()
	// With a gate, availability also depends on the other site, so list
	// everything and filter after applying it.
	src := available
	if all || gate != nil {
		src = everything
	}
	for _, f := range src {
		cand := toCandidate(f, d)
		cand.Available = avail[f.Metadata.Name]
		if img, err := hub.Inspect(ctx, c, cand.Image, cand.Digest); err == nil {
			cand.Version, cand.BuiltAt = img.Version(), img.Created
		}
		out.Items = append(out.Items, cand)
	}
	if gate != nil {
		gate.apply(ctx, hub, out)
		if !all {
			out.Items = slices.DeleteFunc(out.Items, func(c Candidate) bool { return !c.Available })
		}
	}
	return out, nil
}

func requested(stage *kargo.Stage, list []kargo.Freight) []kargo.Freight {
	var out []kargo.Freight
	for _, f := range list {
		if stage.Requests(f.Origin) {
			out = append(out, f)
		}
	}
	return out
}

func toCandidate(f kargo.Freight, d *catalog.Deployment) Candidate {
	c := Candidate{Freight: f.Metadata.Name, Alias: f.Alias, CreatedAt: f.Metadata.CreationTimestamp, Current: f.Metadata.Name == d.Freight}
	for _, img := range f.Images {
		c.Image, c.Tag, c.Digest = img.RepoURL, img.Tag, img.Digest
		if d.Image == "" || img.RepoURL == d.Image {
			break
		}
	}
	for s, v := range f.Status.VerifiedIn {
		c.VerifiedIn = append(c.VerifiedIn, StageMark{Stage: s, Since: v.VerifiedAt})
	}
	for s, v := range f.Status.CurrentlyIn {
		c.CurrentIn = append(c.CurrentIn, StageMark{Stage: s, Since: v.Since})
	}
	slices.SortFunc(c.VerifiedIn, func(a, b StageMark) int { return cmpStr(a.Stage, b.Stage) })
	slices.SortFunc(c.CurrentIn, func(a, b StageMark) int { return cmpStr(a.Stage, b.Stage) })
	return c
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Build resolves a freight choice into an image item payload with from/to
// pinned by digest, and computes anomalies against live state.
func Build(ctx context.Context, hub *catalog.Hub, sys settings.ReleasePolicy, d *catalog.Deployment, gate *Gate, freight string, everDeployed bool) (*release.ImagePayload, error) {
	cands, err := List(ctx, hub, d, gate, true)
	if err != nil {
		return nil, err
	}
	var target *Candidate
	for i := range cands.Items {
		if cands.Items[i].Freight == freight {
			target = &cands.Items[i]
		}
	}
	if target == nil {
		return nil, errf("pl.freightUnknown", short(freight), d.Env)
	}
	if !target.Available && gate != nil && !slices.ContainsFunc(target.VerifiedIn, func(m StageMark) bool { return m.Stage == gate.Label }) {
		if gate.Problem != "" {
			return nil, errf("pl.freightBlocked", short(freight), d.Env, gate.Problem)
		}
		return nil, errf("pl.freightUnverifiedGate", short(freight), label(target.Version, target.Tag), gate.Label, d.Env)
	}
	if !target.Available {
		if len(cands.UpstreamStages) == 0 {
			return nil, errf("pl.freightNotPromoted", short(freight), d.Env)
		}
		return nil, errf("pl.freightUnverified", short(freight), strings.Join(cands.UpstreamStages, " / "), d.Env)
	}
	if target.Current {
		return nil, errf("pl.freightAlreadyRunning", short(freight), d.Env)
	}
	p := &release.ImagePayload{
		Upstream: d.Upstream, Project: d.KargoProject, Stage: d.KargoStage, Service: d.Service, Env: d.Env, App: d.App,
		Freight: freight, Image: target.Image,
		To: release.Artifact{Digest: target.Digest, Tag: target.Tag, Version: target.Version, BuiltAt: target.BuiltAt},
	}
	var current *Candidate
	if d.Freight != "" {
		p.From = &release.Artifact{Digest: d.Digest, Tag: d.Tag, Version: d.Version, BuiltAt: d.BuiltAt}
		for i := range cands.Items {
			if cands.Items[i].Freight == d.Freight {
				current = &cands.Items[i]
			}
		}
	}
	if gate != nil {
		for _, m := range target.VerifiedIn {
			if m.Stage == gate.Label {
				p.Verified = gate.Verification(target.Digest, m.Since)
			}
		}
	}
	p.Anomalies = Anomalies(sys, cands, target, current, p.From, everDeployed, time.Now())
	return p, nil
}

var tagTime = regexp.MustCompile(`^(\d{14})-`)

// buildTime orders artifacts: freight creation, else image build time, else
// the timestamp prefix of the build tag.
func buildTime(c *Candidate, a *release.Artifact) time.Time {
	if c != nil && c.CreatedAt != nil {
		return *c.CreatedAt
	}
	if a != nil && a.BuiltAt != nil {
		return *a.BuiltAt
	}
	if a != nil {
		if m := tagTime.FindStringSubmatch(a.Tag); m != nil {
			t, _ := time.ParseInLocation("20060102150405", m[1], time.Local)
			return t
		}
	}
	return time.Time{}
}

func Anomalies(sys settings.ReleasePolicy, cands *Candidates, target, current *Candidate, from *release.Artifact, everDeployed bool, now time.Time) []release.Anomaly {
	var out []release.Anomaly
	if from == nil {
		// The message is stored so it reads later exactly as it read to
		// whoever confirmed, like an audit record; Args carry the same facts
		// separately so a reader in another language can be shown them in
		// theirs. The two variants are separate codes for the same reason:
		// one message per code, or the code cannot stand in for it.
		code, msg := release.AnomalyFirstDeploy, at("pl.firstDeploy")
		if everDeployed {
			code, msg = release.AnomalyFirstDeployPerKargo, at("pl.firstDeployPerKargo")
		}
		out = append(out, release.Anomaly{Code: code, Message: msg})
		return out
	}
	tt := buildTime(target, &release.Artifact{Tag: target.Tag, BuiltAt: target.BuiltAt})
	ct := buildTime(current, from)
	if !tt.IsZero() && !ct.IsZero() && tt.Before(ct) {
		to, back := label(target.Version, target.Tag), label(from.Version, from.Tag)
		out = append(out, release.Anomaly{Code: "rollback",
			Message: at("pl.rollback", to, back), Args: []string{to, back}})
	}
	if !tt.IsZero() && !ct.IsZero() && tt.After(ct) && sys.MultiVersionJump > 0 {
		n := 0
		for _, c := range cands.Items {
			bt := buildTime(&c, nil)
			if bt.After(ct) && !bt.After(tt) {
				n++
			}
		}
		if n > sys.MultiVersionJump {
			count, was, now := strconv.Itoa(n), label(from.Version, from.Tag), label(target.Version, target.Tag)
			out = append(out, release.Anomaly{Code: "multi_version_jump",
				Message: at("pl.versionJump", n, was, now), Args: []string{count, was, now}})
		}
	}
	if sys.MinSoakMinutes > 0 {
		for _, up := range cands.UpstreamStages {
			var since *time.Time
			for _, v := range target.VerifiedIn {
				if v.Stage == up {
					since = v.Since
				}
			}
			for _, v := range target.CurrentIn {
				if v.Stage == up && since == nil {
					since = v.Since
				}
			}
			soak := time.Duration(sys.MinSoakMinutes) * time.Minute
			if since != nil && now.Sub(*since) < soak {
				soaked := now.Sub(*since).Round(time.Minute).String()
				out = append(out, release.Anomaly{Code: "short_soak",
					Message: at("pl.shortSoak", up, soaked, sys.MinSoakMinutes),
					Args:    []string{up, soaked, strconv.Itoa(sys.MinSoakMinutes)}})
			}
		}
	}
	return out
}

func short(freight string) string {
	if len(freight) > 12 {
		return freight[:12]
	}
	return freight
}

func label(version, tag string) string {
	if version != "" {
		return version
	}
	return tag
}

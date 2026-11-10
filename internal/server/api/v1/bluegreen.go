package v1

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"tide/internal/catalog"
	"tide/internal/executor"
	"tide/internal/i18n"
	"tide/internal/plan"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/store/pg"
)

// The blue-green window, from the service side. A release succeeds the
// moment traffic is on the new version; what follows — the previous version
// kept running, ending that early, rolling back to it — happens after the
// release is over, to a service and environment, so it lives here and not on
// the release.

// BlueGreenView is a service's blue-green state in one environment.
type BlueGreenView struct {
	// WindowSeconds is the environment's setting, what Kargo writes.
	WindowSeconds int                    `json:"windowSeconds"`
	Rollouts      []executor.RolloutView `json:"rollouts"`
	// Drift: a blue-green Rollout's manifest says something other than the
	// setting — set by hand, or not promoted since the setting changed.
	Drift    bool                `json:"drift"`
	Retained []executor.Retained `json:"retained"`
	// Rollback is what rolling back would put back, when it can be done now;
	// otherwise RollbackError says why not (only while a version is kept).
	Rollback      *RollbackOffer `json:"rollback,omitempty"`
	RollbackError string         `json:"rollbackError,omitempty"`
}

// RollbackOffer is the version a rollback would switch back to.
type RollbackOffer struct {
	To release.Artifact `json:"to"`
	// ReleaseID is the release whose switch it undoes.
	ReleaseID string    `json:"releaseId"`
	Until     time.Time `json:"until"`
}

// blueGreenView reads it; nil for a service with no blue-green Rollout.
func (a *API) blueGreenView(ctx context.Context, d *catalog.Deployment) (*BlueGreenView, error) {
	rs, err := executor.ViewRollouts(ctx, a.Hub, d.Upstream, d.App)
	if err != nil {
		return nil, err
	}
	v := &BlueGreenView{Rollouts: []executor.RolloutView{}, Retained: []executor.Retained{}}
	for _, r := range rs {
		if r.Strategy == "blueGreen" {
			v.Rollouts = append(v.Rollouts, r)
		}
	}
	if len(v.Rollouts) == 0 {
		return nil, nil
	}
	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		return nil, err
	}
	if e, ok := envs.Named(d.Env); ok {
		v.WindowSeconds = int(e.BlueGreenWindow().Seconds())
	}
	for _, r := range v.Rollouts {
		if r.ScaleDownDelaySeconds != nil && *r.ScaleDownDelaySeconds != int64(v.WindowSeconds) ||
			r.AutoPromotion != nil && !*r.AutoPromotion {
			v.Drift = true
		}
	}
	c, err := a.Hub.Named(ctx, d.Upstream)
	if err != nil {
		return nil, err
	}
	if v.Retained, err = executor.RetainedOf(ctx, c, d.App); err != nil {
		return nil, err
	}
	if len(v.Retained) > 0 {
		if rb, err := a.planRollback(ctx, c, d, v.Retained); err == nil {
			v.Rollback = &rb.offer
		} else {
			v.RollbackError = errcode.From(err).Text(i18n.From(ctx))
		}
	}
	return v, nil
}

type rollbackPlan struct {
	offer   RollbackOffer
	freight string
	last    *release.ImagePayload
}

// planRollback works out whether this service can be rolled back without
// approval right now, and to what. Every condition is checked here, on the
// server, from the cluster and Tide's own records; nothing the browser sends
// decides it. The exemption from approval is narrow on purpose:
//
//   - the previous version is still running, ready, before its deadline —
//     the window, as the controller itself reports it;
//   - the version it runs is exactly the one the last successful release
//     replaced, and that release's version is what runs now. Anything else
//     means something moved in between, and "the previous version" no longer
//     names one thing.
//
// Outside that, putting an older version back is an ordinary release.
func (a *API) planRollback(ctx context.Context, c *catalog.Clients, d *catalog.Deployment, kept []executor.Retained) (*rollbackPlan, error) {
	if len(kept) == 0 {
		return nil, errcode.NewKey(errcode.Conflict, "r.noWindow")
	}
	list, _, err := a.PG.Releases.List(ctx, pg.ReleaseFilter{Service: d.Service, Env: d.Env, Kind: release.KindImage,
		Statuses: []release.Status{release.Succeeded}}, 1, 1)
	if err != nil {
		return nil, err
	}
	var rel *release.Release
	if len(list) > 0 {
		if rel, err = a.PG.Releases.Get(ctx, list[0].ID); err != nil {
			return nil, err
		}
	}
	running := d.Digest
	if p := lastImage(rel, d.Service); p != nil {
		if running, err = executor.RunningDigest(ctx, c, p.Project, p.Stage, p.Image); err != nil {
			return nil, err
		}
	}
	offer, last, err := decideRollback(time.Now(), kept, rel, d.Service, running)
	if err != nil {
		return nil, err
	}
	avail, err := c.Kargo.QueryFreight(ctx, last.Project, last.Stage)
	if err != nil {
		return nil, err
	}
	freight := executor.PreviousFreight(avail, last)
	if freight == "" {
		return nil, errcode.NewKey(errcode.Conflict, "r.rollbackNoFreight", labelArtifact(*last.From))
	}
	return &rollbackPlan{offer: offer, freight: freight, last: last}, nil
}

// decideRollback is planRollback's judgement, apart from fetching what it
// judges: kept is what the cluster says is retained, rel the last successful
// image release of service here (nil when none), running the digest that
// runs now.
func decideRollback(now time.Time, kept []executor.Retained, rel *release.Release, service, running string) (RollbackOffer, *release.ImagePayload, error) {
	var usable []executor.Retained
	rollouts := map[string]bool{}
	for _, r := range kept {
		rollouts[r.Rollout] = true
		if r.Usable(now) {
			usable = append(usable, r)
		}
	}
	switch {
	case len(kept) == 0:
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.noWindow")
	case len(rollouts) > 1:
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.rollbackSeveral")
	case len(usable) == 0:
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.windowNotUsable")
	}
	last := lastImage(rel, service)
	switch {
	case last == nil:
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.rollbackNoRelease")
	case last.From == nil:
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.rollbackFirstDeploy")
	case last.To.Digest == "" || last.To.Digest != running:
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.rollbackMovedOn", rel.ID)
	case last.From.Digest == "":
		// The confirmation is held to a digest; a version recorded by tag
		// alone cannot be confirmed, so it is not offered.
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.rollbackNotRetained", labelArtifact(*last.From))
	}
	// Several versions can be kept at once — a Rollout without a revision
	// limit keeps every one still inside its window — so the one to switch
	// back to is the one running the version replaced, wherever it is listed.
	i := slices.IndexFunc(usable, func(r executor.Retained) bool { return r.Runs(*last.From) })
	if i < 0 {
		return RollbackOffer{}, nil, errcode.NewKey(errcode.Conflict, "r.rollbackNotRetained", labelArtifact(*last.From))
	}
	return RollbackOffer{To: *last.From, ReleaseID: rel.ID, Until: usable[i].Until}, last, nil
}

// lastImage is service's succeeded image item in rel, nil when it has none.
func lastImage(rel *release.Release, service string) *release.ImagePayload {
	if rel == nil {
		return nil
	}
	var last *release.ImagePayload
	for _, it := range rel.Items {
		if p, err := rel.ImagePayload(it); err == nil && p.Service == service && it.Status == release.ItemSucceeded {
			last = p
		}
	}
	return last
}

func labelArtifact(a release.Artifact) string {
	if a.Tag != "" {
		return a.Tag
	}
	return a.Digest
}

// endObservation ends one service's window now: the previous version is
// scaled down instead of at its deadline.
func (a *API) endObservation(c *gin.Context) {
	p := newParams(c)
	_, d, err := a.deployment(c, p, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.requireTarget(c, rbac.ReleasesCreate, d.Service, d.Env, ""); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	n, err := a.endOne(ctx, d)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	_ = a.PG.Audit.Write(ctx, currentUser(c).Actor(), "bluegreen.end", d.Service+"/"+d.Env, "",
		map[string]any{"service": d.Service, "env": d.Env, "replicaSets": n})
	respond.OK(c, gin.H{"ended": n})
}

func (a *API) endOne(ctx context.Context, d *catalog.Deployment) (int, error) {
	cl, err := a.Hub.Named(ctx, d.Upstream)
	if err != nil {
		return 0, err
	}
	n, err := executor.EndObservation(ctx, cl, d.App)
	if errors.Is(err, executor.ErrNotObserving) {
		return 0, errcode.NewKey(errcode.Conflict, "r.noWindow")
	}
	return n, err
}

type endAllReq struct {
	// The items the person was shown; only these are acted on.
	Items []int64 `json:"items" binding:"required,min=1,max=50" label:"items"`
}

// endObservationAll ends the window of every listed service of a release, one
// at a time: each is checked, acted on and audited on its own, and one that
// fails does not stop the rest.
func (a *API) endObservationAll(c *gin.Context) {
	p := newParams(c)
	id := p.releaseID()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req endAllReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	rel, err := a.PG.Releases.Get(ctx, id)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	snap, err := a.Hub.Snapshot(ctx, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	g, err := a.grants(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	wanted := map[int64]bool{}
	for _, i := range req.Items {
		wanted[i] = true
	}
	loc := i18n.From(ctx)
	results := []RolloutResult{}
	for _, it := range rel.Items {
		if !wanted[it.ID] {
			continue
		}
		res := RolloutResult{ItemID: it.ID}
		pl, err := rel.ImagePayload(it)
		if err != nil {
			res.Error = i18n.T(loc, "r.noWindow")
			results = append(results, res)
			continue
		}
		res.Service = pl.Service
		d := deploymentIn(snap, pl.Service, rel.Env)
		switch {
		case d == nil:
			res.Error = errcode.New(errcode.ServiceNotDeployed, "").Text(loc)
		case !g.HasTarget(rbac.ReleasesCreate, a.targetOf(ctx, d.Service, d.Env)):
			res.Error = errcode.NewKey(errcode.Forbidden, "r.noReleasePermission").Text(loc)
		default:
			if n, err := a.endOne(ctx, d); err != nil {
				res.Error = errcode.From(err).Text(loc)
			} else {
				res.OK = true
				_ = a.PG.Audit.Write(ctx, currentUser(c).Actor(), "bluegreen.end", d.Service+"/"+d.Env, rel.JiraTicket,
					map[string]any{"service": d.Service, "env": d.Env, "replicaSets": n, "release": rel.ID, "batch": true})
			}
		}
		results = append(results, res)
	}
	respond.OK(c, gin.H{"results": results})
}

type rollbackReq struct {
	// The digest of the version the person was shown and agreed to put
	// back: proof the confirmation was for this version, as on a release.
	Digest string `json:"digest" binding:"required,max=100" label:"digest"`
}

func (r *rollbackReq) Normalize() { trim(&r.Digest) }

// rollback switches a service back to the version its window is keeping, as
// a release that goes out at once: the person's second confirmation is the
// confirmation, and no approval is asked. Change freezes and the enforced
// release checks still apply.
func (a *API) rollback(c *gin.Context) {
	var req rollbackReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	p := newParams(c)
	_, d, err := a.deployment(c, p, true)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.requireTarget(c, rbac.ReleasesCreate, d.Service, d.Env, ""); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	u := currentUser(c)
	policy, err := a.Settings.ReleasePolicy(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.checkPolicy(ctx, policy, d.Env, "", "", false); err != nil {
		respond.Fail(c, err)
		return
	}
	cl, err := a.Hub.Named(ctx, d.Upstream)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	kept, err := executor.RetainedOf(ctx, cl, d.App)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	rb, err := a.planRollback(ctx, cl, d, kept)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if rb.offer.To.Digest == "" || req.Digest != rb.offer.To.Digest {
		respond.FailCode(c, errcode.DigestMismatch, "")
		return
	}
	gate, err := a.gate(ctx, d)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	payload, err := plan.Build(ctx, a.Hub, policy, d, gate, rb.freight, true)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	// Built from the freight, then held to what was offered: a Freight that
	// resolves to anything else is not the rollback the person agreed to.
	if payload.To.Digest != rb.offer.To.Digest {
		respond.FailCode(c, errcode.DigestMismatch, "")
		return
	}
	payload.RollbackOf = rb.offer.ReleaseID
	tier := ""
	if envs, err := a.Settings.Environments(ctx); err == nil {
		if e, ok := envs.Named(d.Env); ok {
			tier = e.Tier
		}
	}
	if blocked := plan.EnforcedAnomalies(i18n.From(ctx), policy, d.Env, tier, payload.Anomalies); len(blocked) > 0 {
		respond.Fail(c, errcode.NewKey(errcode.ThresholdBlocked, "r.thresholdBlocked", blocked[0]))
		return
	}
	b, _ := json.Marshal(payload)
	loc := i18n.From(ctx)
	in := release.CreateInput{
		Title:  i18n.T(loc, "r.rollbackTitle", d.Service, labelArtifact(rb.offer.To)),
		Env:    d.Env,
		Reason: i18n.T(loc, "r.rollbackReason", rb.offer.ReleaseID),
		Items:  []release.ItemInput{{Kind: release.KindImage, Payload: b}},
	}
	rel, err := a.PG.Releases.Create(ctx, u.Actor(), in)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if _, err := a.PG.Releases.Submit(ctx, u.Actor(), rel.ID); err != nil {
		_, _ = a.PG.Releases.Cancel(context.WithoutCancel(ctx), u.Actor(), rel.ID, "submit failed: "+err.Error())
		respond.Fail(c, err)
		return
	}
	// No reading wait and no approval rule: the second confirmation in the
	// browser was the reading, and the window is why approval is waived.
	confirmed, err := a.PG.Releases.Confirm(ctx, u.Actor(), rel.ID, 0, policy.ConfirmTTL(), nil)
	if err != nil {
		_, _ = a.PG.Releases.Cancel(context.WithoutCancel(ctx), u.Actor(), rel.ID, "confirm failed: "+err.Error())
		respond.Fail(c, err)
		return
	}
	_ = a.PG.Audit.Write(ctx, u.Actor(), "release.rollback", rel.ID, "",
		map[string]any{"service": d.Service, "env": d.Env, "rollbackOf": rb.offer.ReleaseID,
			"to": rb.offer.To.Digest, "keptUntil": rb.offer.Until})
	a.Hub.Reset()
	a.respondRelease(c, confirmed)
}

package v1

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/plan"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/store/pg"
	"tide/internal/upstream/argocd"
	"tide/internal/upstream/kargo"
)

const upstreamTimeout = 20 * time.Second

func (a *API) overview(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	// fresh is how somebody who changed the cluster without going through
	// Tide gets to see it now rather than within the cache's lifetime. The
	// page never asks for it on its own: it is one button, pressed by a
	// person who knows they are waiting for something.
	p := newParams(c)
	fresh := p.boolean("fresh")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	out := gin.H{}
	vis, err := a.visibility(c, rbac.ServicesView)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	relVis, err := a.visibility(c, rbac.ReleasesView)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	snap, err := a.Hub.Snapshot(ctx, fresh)
	if err != nil {
		e := errcode.From(err)
		out["upstreamError"] = gin.H{"code": e.Code, "msg": e.Text(i18n.From(ctx))}
	} else {
		seen := vis.services(snap.Services)
		stats, unhealthy, drifted, domains := fleetState(seen)
		out["upstreams"], out["envOrder"], out["envStats"] = snap.Upstreams, snap.EnvOrder, stats
		// The count alone was a number with nowhere to go: it said thirty-five
		// services disagree with git and offered no way to learn which. Both
		// lists are capped — drift is the resting state of a fleet nobody
		// prunes, and a list of almost everything is not a list.
		out["unhealthy"], out["drifted"] = capped(unhealthy), capped(drifted)
		out["driftedCount"] = len(drifted)
		out["serviceCount"], out["domainCount"] = len(seen), domains
	}
	// Releases are listed within the same scope as the services they touch.
	seen := pg.ReleaseFilter{Services: relVis.names()}
	inFlight, _, err := a.PG.Releases.List(ctx, withStatuses(seen, release.Confirming, release.Approving, release.Executing), 1, 100)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	failed, _, err := a.PG.Releases.List(ctx, withStatuses(seen, release.Failed), 1, 5)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	recent, _, err := a.PG.Releases.List(ctx, seen, 1, 10)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	total, ok, err := a.PG.Releases.CountToday(ctx, relVis.names())
	if err != nil {
		respond.Fail(c, err)
		return
	}
	mine := 0
	for _, r := range inFlight {
		if r.CreatedBy == u.Sub {
			mine++
		}
	}
	for key, list := range map[string][]release.Release{"inFlight": inFlight, "recentFailed": failed, "recent": recent} {
		if out[key], err = a.views(c, list); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	out["myInFlight"] = mine
	out["today"] = gin.H{"total": total, "succeeded": ok}
	respond.OK(c, out)
}

func (a *API) listServices(c *gin.Context) {
	p := newParams(c)
	fresh := p.boolean("fresh")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	vis, err := a.visibility(c, rbac.ServicesView)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	snap, err := a.Hub.Snapshot(c.Request.Context(), fresh)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	services := vis.services(snap.Services)
	active, _, err := a.PG.Releases.List(c.Request.Context(), pg.ReleaseFilter{Statuses: []release.Status{release.Confirming, release.Approving, release.Executing}}, 1, 100)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	inFlight := map[string]string{}
	for _, r := range active {
		for _, it := range r.Items {
			if tg, err := r.Target(it); err == nil {
				inFlight[tg.Service+"/"+tg.Env] = r.ID
			}
		}
	}
	respond.OK(c, gin.H{"services": services, "envOrder": snap.EnvOrder, "upstreams": snap.Upstreams, "at": snap.At, "inFlight": inFlight})
}

// withStatuses copies f with the statuses set.
func withStatuses(f pg.ReleaseFilter, st ...release.Status) pg.ReleaseFilter {
	f.Statuses = st
	return f
}

// deployment resolves validated :service (and :env when present) against the
// live catalog.
func (a *API) deployment(c *gin.Context, p *params, fresh bool) (*catalog.Service, *catalog.Deployment, error) {
	name := p.service()
	env := ""
	if p.hasPath("env") {
		env = p.env()
	}
	if err := p.err(); err != nil {
		return nil, nil, err
	}
	snap, err := a.Hub.Snapshot(c.Request.Context(), fresh)
	if err != nil {
		return nil, nil, err
	}
	svc := snap.Find(name)
	if svc == nil {
		return nil, nil, errcode.New(errcode.ServiceNotFound, "")
	}
	// Outside the viewer's scope the service does not exist for them.
	vis, err := a.visibility(c, rbac.ServicesView)
	if err != nil {
		return nil, nil, err
	}
	if !vis.service(svc) || (env != "" && !vis.env(name, env)) {
		return nil, nil, errcode.New(errcode.ServiceNotFound, "")
	}
	if env == "" {
		return svc, nil, nil
	}
	d := svc.Envs[env]
	if d == nil {
		return nil, nil, errcode.New(errcode.ServiceNotDeployed, "")
	}
	return svc, d, nil
}

func (a *API) getService(c *gin.Context) {
	p := newParams(c)
	// Filtering here rather than in the browser: the history is the twenty
	// most recent releases across every environment, so narrowing that list
	// client-side would show nothing for prod as soon as dev had twenty of
	// its own — and "no prod releases" would be a lie.
	env := p.match("env", reEnv, "label.envName")
	svc, _, err := a.deployment(c, p, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	history, _, err := a.PG.Releases.List(c.Request.Context(), pg.ReleaseFilter{Service: svc.Name, Env: env}, 1, 20)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	vs, err := a.views(c, history)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"service": svc, "releases": vs})
}

func (a *API) getEnv(c *gin.Context) {
	p := newParams(c)
	fresh := p.boolean("fresh")
	svc, d, err := a.deployment(c, p, fresh)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	history, _, err := a.PG.Releases.List(ctx, pg.ReleaseFilter{Service: d.Service, Env: d.Env}, 1, 30)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	g, err := a.grants(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	tg := a.targetOf(ctx, d.Service, d.Env)
	vs, err := a.views(c, history)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	out := gin.H{
		"deployment": d,
		"live":       a.live(ctx, d, d.Digest, d.Tag),
		"canOperate": g.HasTarget(rbac.ReleasesCreate, tg),
		// What the viewer may do on this service here, scopes applied.
		"can": gin.H{
			"create":  g.HasTarget(rbac.ReleasesCreate, tg),
			"sync":    g.HasTarget(rbac.ReleasesSync, tg),
			"restart": g.HasTarget(rbac.ReleasesRestart, tg),
			"pods":    g.HasTarget(rbac.PodsView, tg),
		},
		"releases":  vs,
		"conflicts": svc.Conflicts,
	}
	byRef := map[string]string{}
	for _, r := range history {
		for _, it := range r.Items {
			if it.ExternalRef != "" {
				byRef[it.ExternalRef] = r.ID
			}
		}
	}
	promos := []PromotionView{}
	if d.KargoProject != "" {
		cl, err := a.Hub.Named(ctx, d.Upstream)
		if err == nil {
			var list []kargo.Promotion
			if list, err = cl.Kargo.ListPromotions(ctx, d.KargoProject, d.KargoStage); err == nil {
				for i := range list[:min(len(list), 30)] {
					v := promotionView(&list[i])
					v.ReleaseID = byRef[v.Name]
					promos = append(promos, v)
				}
			}
		}
		if err != nil {
			out["promotionsError"] = errcode.From(err).Text(i18n.From(ctx))
		}
	}
	out["promotions"] = promos
	respond.OK(c, out)
}

func (a *API) listCandidates(c *gin.Context) {
	p := newParams(c)
	all := p.boolean("all")
	_, d, err := a.deployment(c, p, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	gate, err := a.gate(ctx, d)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	cands, err := plan.List(ctx, a.Hub, d, gate, all)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if cands.Items == nil {
		cands.Items = []plan.Candidate{}
	}
	respond.OK(c, cands)
}

// gate returns the cross-site verification d's environment requires, if any.
func (a *API) gate(ctx context.Context, d *catalog.Deployment) (*plan.Gate, error) {
	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		return nil, err
	}
	return plan.GateFor(ctx, a.Hub, envs, d)
}

// configDiff shows what a config sync of the service in env would change.
// refresh=true asks Argo CD to re-read git first.
func (a *API) configDiff(c *gin.Context) {
	p := newParams(c)
	refresh := p.boolean("refresh")
	_, d, err := a.deployment(c, p, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	g, err := a.grants(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	// Anyone who may upgrade or sync here may see what a sync would change.
	if !g.HasTarget(rbac.ReleasesCreate, a.targetOf(ctx, d.Service, d.Env)) {
		if err := a.requireTarget(c, rbac.ReleasesSync, d.Service, d.Env, d.Service); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	diff, err := plan.DiffConfig(ctx, a.Hub, d, refresh)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, diff)
}

func (a *API) podLogs(c *gin.Context) {
	p := newParams(c)
	pod := p.pod()
	container := p.container()
	tail := p.intRange("tail", 500, 1, 5000)
	_, d, err := a.deployment(c, p, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.requireTarget(c, rbac.PodsView, d.Service, d.Env, d.Service+"/"+pod); err != nil {
		respond.Fail(c, err)
		return
	}
	cl, err := a.Hub.Named(c.Request.Context(), d.Upstream)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	// Argo CD scopes the pod to the Application, so a pod name from another
	// app or namespace is refused upstream.
	lines, err := cl.ArgoCD.Logs(ctx, d.App, d.Namespace, pod, container, tail)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if lines == nil {
		lines = []argocd.LogLine{}
	}
	respond.OK(c, gin.H{"lines": lines})
}

func (a *API) podEvents(c *gin.Context) {
	p := newParams(c)
	pod := p.pod()
	uid := p.uuid("uid")
	_, d, err := a.deployment(c, p, false)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.requireTarget(c, rbac.PodsView, d.Service, d.Env, d.Service+"/"+pod); err != nil {
		respond.Fail(c, err)
		return
	}
	cl, err := a.Hub.Named(c.Request.Context(), d.Upstream)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	events, err := cl.ArgoCD.Events(ctx, d.App, d.Namespace, pod, uid)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if events == nil {
		events = []argocd.Event{}
	}
	respond.OK(c, gin.H{"events": events})
}

type Pod struct {
	Name      string     `json:"name"`
	Namespace string     `json:"namespace"`
	UID       string     `json:"uid"`
	Health    string     `json:"health"`
	Message   string     `json:"message,omitempty"`
	Status    string     `json:"status"`
	Ready     string     `json:"ready"`
	Restarts  string     `json:"restarts"`
	Images    []string   `json:"images"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// IsTarget marks pods running the target digest or tag.
	IsTarget bool `json:"isTarget"`
}

type Resource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Sync      string `json:"sync"`
	Health    string `json:"health,omitempty"`
	Message   string `json:"message,omitempty"`
}

type Rollout struct {
	Name      string `json:"name"`
	Desired   int    `json:"desired"`
	Updated   int    `json:"updated"`
	Ready     int    `json:"ready"`
	Available int    `json:"available"`
}

type Live struct {
	App       string     `json:"app"`
	Sync      string     `json:"sync"`
	Health    string     `json:"health"`
	Operation string     `json:"operation,omitempty"`
	Resources []Resource `json:"resources"`
	Pods      []Pod      `json:"pods"`
	Rollouts  []Rollout  `json:"rollouts"`
	Error     string     `json:"error,omitempty"`
}

// live reads the Argo CD view of an Application. Upstream failures are
// reported inside the view so the rest of the page still renders.
func (a *API) live(ctx context.Context, d *catalog.Deployment, targets ...string) *Live {
	out := &Live{App: d.App, Resources: []Resource{}, Pods: []Pod{}, Rollouts: []Rollout{}}
	fail := func(err error) *Live {
		out.Error = errcode.From(err).Text(i18n.From(ctx))
		return out
	}
	cl, err := a.Hub.Named(ctx, d.Upstream)
	if err != nil {
		return fail(err)
	}
	app, err := cl.ArgoCD.GetApplication(ctx, d.App)
	if err != nil {
		return fail(err)
	}
	out.Sync, out.Health = app.Status.Sync.Status, app.Status.Health.Status
	if app.Status.OperationState != nil {
		out.Operation = app.Status.OperationState.Phase
	}
	for _, res := range app.Status.Resources {
		rr := Resource{Kind: res.Kind, Name: res.Name, Namespace: res.Namespace, Sync: res.Status}
		if res.Health != nil {
			rr.Health, rr.Message = res.Health.Status, res.Health.Message
		}
		out.Resources = append(out.Resources, rr)
		if res.Kind == "Deployment" || res.Kind == "StatefulSet" {
			if m, err := cl.ArgoCD.Resource(ctx, d.App, res.Group, res.Version, res.Kind, res.Namespace, res.Name); err == nil {
				out.Rollouts = append(out.Rollouts, rolloutFrom(res.Name, m))
			}
		}
	}
	tree, err := cl.ArgoCD.ResourceTree(ctx, d.App)
	if err != nil {
		return fail(err)
	}
	for _, n := range tree.Nodes {
		if n.Kind != "Pod" {
			continue
		}
		pod := podFrom(n)
		for _, t := range targets {
			for _, img := range n.Images {
				if t != "" && (strings.HasSuffix(img, ":"+t) || strings.HasSuffix(img, "@"+t)) {
					pod.IsTarget = true
				}
			}
		}
		out.Pods = append(out.Pods, pod)
	}
	slices.SortFunc(out.Pods, func(x, y Pod) int { return strings.Compare(x.Name, y.Name) })
	return out
}

func podFrom(n argocd.ResourceNode) Pod {
	p := Pod{Name: n.Name, Namespace: n.Namespace, UID: n.UID, Images: n.Images, CreatedAt: n.CreatedAt,
		Status: n.InfoValue("Status Reason"), Ready: n.InfoValue("Containers"), Restarts: n.InfoValue("Restart Count")}
	if p.Images == nil {
		p.Images = []string{}
	}
	if n.Health != nil {
		p.Health, p.Message = n.Health.Status, n.Health.Message
	}
	return p
}

func rolloutFrom(name string, m map[string]any) Rollout {
	num := func(path ...string) int {
		var cur any = m
		for _, k := range path {
			mm, ok := cur.(map[string]any)
			if !ok {
				return 0
			}
			cur = mm[k]
		}
		f, _ := cur.(float64)
		return int(f)
	}
	return Rollout{Name: name, Desired: num("spec", "replicas"), Updated: num("status", "updatedReplicas"),
		Ready: num("status", "readyReplicas"), Available: num("status", "availableReplicas")}
}

type PromotionView struct {
	Name       string     `json:"name"`
	Phase      string     `json:"phase"`
	Message    string     `json:"message,omitempty"`
	Freight    string     `json:"freight"`
	Tag        string     `json:"tag,omitempty"`
	Digest     string     `json:"digest,omitempty"`
	CreatedAt  *time.Time `json:"createdAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Actor      string     `json:"actor,omitempty"`
	Steps      []StepView `json:"steps"`
	ReleaseID  string     `json:"releaseId,omitempty"`
}

type StepView struct {
	Name       string     `json:"name"`
	Uses       string     `json:"uses"`
	Status     string     `json:"status"`
	Message    string     `json:"message,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

func promotionView(p *kargo.Promotion) PromotionView {
	v := PromotionView{Name: p.Metadata.Name, Phase: p.Status.Phase, Message: p.Status.Message, Freight: p.Spec.Freight,
		CreatedAt: p.Metadata.CreationTimestamp, FinishedAt: p.Status.FinishedAt,
		Actor: p.Metadata.Annotations["kargo.akuity.io/create-actor"], Steps: []StepView{}}
	if f := p.Status.Freight; f != nil && len(f.Images) > 0 {
		v.Tag, v.Digest = f.Images[0].Tag, f.Images[0].Digest
	}
	for i, st := range p.Spec.Steps {
		sv := StepView{Name: st.As, Uses: st.Uses, Status: "Pending"}
		if st.Task != nil && sv.Uses == "" {
			sv.Uses = "task:" + st.Task.Name
		}
		if sv.Name == "" {
			sv.Name = sv.Uses
		}
		if i < len(p.Status.StepExecutionMetadata) {
			m := p.Status.StepExecutionMetadata[i]
			sv.Status, sv.Message, sv.StartedAt, sv.FinishedAt = m.Status, m.Message, m.StartedAt, m.FinishedAt
			if sv.Status == "" && m.StartedAt != nil {
				sv.Status = "Running"
			}
		}
		v.Steps = append(v.Steps, sv)
	}
	return v
}

// overviewListMax bounds each list on the overview. Past it the page says how
// many more there are and sends the reader to the services page, which is
// built for looking through hundreds of rows.
const overviewListMax = 20

func capped(ds []*catalog.Deployment) []*catalog.Deployment {
	if len(ds) > overviewListMax {
		return ds[:overviewListMax]
	}
	return ds
}

// envStat is one environment's line on the overview.
type envStat struct {
	Services  int `json:"services"`
	Unhealthy int `json:"unhealthy"`
	Drifted   int `json:"drifted"`
}

// fleetState counts the two things that can be wrong with a deployment, and
// counts them separately.
//
// Health and sync are independent axes that mean different things. Unhealthy
// is "this service is not serving". OutOfSync is "the cluster does not match
// git", which in an installation where nothing prunes is the resting state of
// very nearly every Application. Adding the two together gave "178 of 179
// unexpected" — a number nobody can act on, and one that reads as broken
// monitoring rather than as a fleet that mostly works. Only the unhealthy
// ones are listed; drift is a count, because a list of almost everything is
// not a list.
func fleetState(svcs []catalog.Service) (stats map[string]*envStat, unhealthy, drifted []*catalog.Deployment, domains int) {
	stats = map[string]*envStat{}
	unhealthy, drifted = []*catalog.Deployment{}, []*catalog.Deployment{}
	seen := map[string]bool{}
	for _, svc := range svcs {
		seen[svc.Domain] = true
		for env, d := range svc.Envs {
			if stats[env] == nil {
				stats[env] = &envStat{}
			}
			stats[env].Services++
			if d.Health != "Healthy" {
				stats[env].Unhealthy++
				unhealthy = append(unhealthy, d)
			}
			if d.Sync != "Synced" {
				stats[env].Drifted++
				drifted = append(drifted, d)
			}
		}
	}
	return stats, unhealthy, drifted, len(seen)
}

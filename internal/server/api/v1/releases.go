package v1

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"tide/internal/auth"
	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/plan"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

type releaseItemReq struct {
	// Kind is "image" (default: promote a freight) or "restart" (roll the
	// current version, e.g. after a config change).
	Kind     string `json:"kind" label:"kind"`
	Service  string `json:"service" binding:"required,max=253" label:"service"`
	Freight  string `json:"freight" label:"freight"`
	Sequence int    `json:"sequence" binding:"omitempty,gte=1,lte=100" label:"sequence"`
	// Prune (sync): allow deleting resources git no longer renders.
	Prune bool `json:"prune"`
	// Restart (sync): roll the workloads after the sync even if their pod
	// template does not change.
	Restart bool `json:"restart"`
	// WithConfig (image): send unsynced git changes along with the image.
	WithConfig bool `json:"withConfig"`
}

type createReleaseReq struct {
	Env        string           `json:"env" binding:"required" label:"env"`
	JiraTicket string           `json:"jiraTicket" binding:"max=64" label:"jira"`
	Reason     string           `json:"reason" label:"reason"`
	Title      string           `json:"title" binding:"max=200" label:"title"`
	Items      []releaseItemReq `json:"items" binding:"required,min=1,max=50,dive" label:"items"`
}

func (r *createReleaseReq) Normalize() {
	trim(&r.Env, &r.JiraTicket, &r.Reason, &r.Title)
	r.JiraTicket = strings.ToUpper(r.JiraTicket)
	for i := range r.Items {
		trim(&r.Items[i].Kind, &r.Items[i].Service, &r.Items[i].Freight)
		if r.Items[i].Kind == "" {
			r.Items[i].Kind = release.KindImage
		}
		if r.Items[i].Sequence == 0 {
			r.Items[i].Sequence = 1
		}
	}
}

func (r *createReleaseReq) Check() error {
	errs := []*validate.FieldError{}
	if r.Env != "" && !reEnv.MatchString(r.Env) {
		errs = append(errs, validate.FieldKey("env", "r.envFormat"))
	}
	if r.JiraTicket != "" && !release.ValidJira(r.JiraTicket) {
		errs = append(errs, validate.FieldKey("jiraTicket", "r.jiraFormat"))
	}
	if n := utf8.RuneCountInString(r.Reason); r.Reason != "" && n < 4 {
		errs = append(errs, validate.FieldKey("reason", "r.reasonTooShort"))
	} else if n > 2000 {
		errs = append(errs, validate.FieldKey("reason", "r.reasonTooLong"))
	}
	seen := map[string]bool{}
	for i, it := range r.Items {
		switch {
		case it.Service != "" && !reDNSName.MatchString(it.Service):
			errs = append(errs, validate.FieldKey(itemField(i, "service"), "r.serviceFormat"))
		case seen[it.Service]:
			errs = append(errs, validate.FieldKey(itemField(i, "service"), "r.serviceDup", it.Service))
		}
		seen[it.Service] = true
		switch it.Kind {
		case release.KindImage:
			switch {
			case it.Freight == "":
				errs = append(errs, validate.FieldKey(itemField(i, "freight"), "r.freightRequired"))
			case !reFreight.MatchString(it.Freight):
				errs = append(errs, validate.FieldKey(itemField(i, "freight"), "r.freightFormat"))
			}
		case release.KindRestart, release.KindSync:
			if it.Freight != "" {
				errs = append(errs, validate.FieldKey(itemField(i, "freight"), "r.freightNotNeeded"))
			}
		default:
			errs = append(errs, validate.FieldKey(itemField(i, "kind"), "r.kindEnum"))
		}
		if it.Kind != release.KindSync && (it.Prune || it.Restart) {
			errs = append(errs, validate.FieldKey(itemField(i, "prune"), "r.pruneSyncOnly"))
		}
		if it.Kind != release.KindImage && it.WithConfig {
			errs = append(errs, validate.FieldKey(itemField(i, "withConfig"), "r.withConfigImageOnly"))
		}
	}
	return validate.Collect(errs...)
}

func itemField(i int, name string) string {
	return "items." + itoa(i) + "." + name
}

// createRelease resolves each choice against live upstream state, pins
// digests, computes anomalies, then creates the release and submits it into
// confirming. The confirmation clock starts on the server at that moment.
func (a *API) createRelease(c *gin.Context) {
	var req createReleaseReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	for _, it := range req.Items {
		if err := a.requireTarget(c, kindPermission(it.Kind), it.Service, req.Env, req.JiraTicket); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
	defer cancel()
	u := currentUser(c)
	sys, err := a.Settings.ReleasePolicy(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.checkPolicy(ctx, sys, req.Env, req.JiraTicket, req.Reason, true); err != nil {
		respond.Fail(c, err)
		return
	}
	snap, err := a.Hub.Snapshot(ctx, true)
	if err != nil {
		respond.Fail(c, err)
		return
	}

	for i, it := range req.Items {
		if svc := snap.Find(it.Service); svc != nil && len(svc.Conflicts) > 0 {
			respond.Fail(c, errcode.NewKey(errcode.ServiceConflict, "r.itemConflict", i+1, strings.Join(svc.Conflicts, "; ")))
			return
		}
	}
	if err := a.checkBatch(ctx, snap, &req); err != nil {
		respond.Fail(c, err)
		return
	}

	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	tier := ""
	if e, ok := envs.Named(req.Env); ok {
		tier = e.Tier
	}
	var blocked []string
	in := release.CreateInput{Title: req.Title, Env: req.Env, JiraTicket: req.JiraTicket, Reason: req.Reason}

	// Every service is resolved before any of them is planned, so that a name
	// that is not deployed here is reported without having asked an upstream
	// anything.
	deployments := make([]*catalog.Deployment, len(req.Items))
	for i, it := range req.Items {
		d := deploymentIn(snap, it.Service, req.Env)
		if d == nil {
			respond.Fail(c, errcode.Invalid(errcode.FieldError{Field: itemField(i, "service"), Key: "r.notDeployedTo", Args: []any{it.Service, req.Env}}))
			return
		}
		deployments[i] = d
	}

	// Planning an item costs several upstream calls — Kargo for the stage and
	// its freight, the registry for each candidate's metadata. Serially, that
	// is linear in the size of the batch, and a batch near the documented
	// limit of fifty spent longer than the upstream client's own timeout, so
	// the fifty the docs allow could not actually be submitted. The items do
	// not depend on one another, so they are planned together.
	type planned struct {
		payload any
		err     error
	}
	plans := make([]planned, len(req.Items))
	tasks := make([]func(), 0, len(req.Items))
	// Shared for this request only, so the items stop asking Kargo the same
	// project-wide question once each.
	ctx = plan.WithMemo(ctx, plan.NewMemo())
	for i, it := range req.Items {
		i, it, d := i, it, deployments[i]
		tasks = append(tasks, func() {
			var payload any
			var err error
			switch it.Kind {
			case release.KindRestart:
				payload, err = plan.BuildRestart(ctx, a.Hub, d)
			case release.KindSync:
				payload, err = plan.BuildSync(ctx, a.Hub, d, it.Prune, it.Restart)
				var pe *plan.PruneError
				if errors.As(err, &pe) {
					err = errcode.Invalid(errcode.FieldError{Field: itemField(i, "prune"), Msg: it.Service + "：" + pe.Error()})
				}
			default:
				var ever bool
				if ever, err = a.PG.Releases.EverDeployed(ctx, it.Service, req.Env); err == nil {
					var gate *plan.Gate
					if gate, err = a.gate(ctx, d); err == nil {
						payload, err = plan.Build(ctx, a.Hub, sys, d, gate, it.Freight, ever)
					}
				}
			}
			if err == nil {
				if ip, ok := payload.(*release.ImagePayload); ok {
					err = plan.AttachConfigDrift(ctx, a.Hub, d, ip, it.WithConfig)
				}
			}
			plans[i] = planned{payload: payload, err: err}
		})
	}
	catalog.Parallel(8, tasks...)

	// Reported in the order they were sent, whichever finished first: the
	// caller numbered these items and an error against item 7 has to be the
	// one it gets when items 7 and 9 both failed.
	for i, it := range req.Items {
		if plans[i].err != nil {
			respond.Fail(c, plans[i].err)
			return
		}
		payload := plans[i].payload
		if ip, ok := payload.(*release.ImagePayload); ok {
			for _, msg := range plan.EnforcedAnomalies(i18n.From(ctx), sys, req.Env, tier, ip.Anomalies) {
				blocked = append(blocked, i18n.T(i18n.From(ctx), "r.itemBlocked", i+1, it.Service, msg))
			}
		}
		b, _ := json.Marshal(payload)
		in.Items = append(in.Items, release.ItemInput{Kind: it.Kind, Sequence: it.Sequence, Payload: b})
	}
	if len(blocked) > 0 {
		respond.Fail(c, errcode.NewKey(errcode.ThresholdBlocked, "r.thresholdBlocked", strings.Join(blocked, "; ")))
		return
	}
	rel, err := a.PG.Releases.Create(ctx, u.Actor(), in)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	submitted, err := a.PG.Releases.Submit(ctx, u.Actor(), rel.ID)
	if err != nil {
		// Don't leave an orphan draft; the cancel records why it never started.
		_, _ = a.PG.Releases.Cancel(context.WithoutCancel(ctx), u.Actor(), rel.ID, "submit failed: "+err.Error())
		respond.Fail(c, err)
		return
	}
	a.Hub.Reset()
	a.respondRelease(c, submitted)
}

func deploymentIn(snap *catalog.Snapshot, service, env string) *catalog.Deployment {
	if svc := snap.Find(service); svc != nil {
		return svc.Envs[env]
	}
	return nil
}

// ReleaseView adds the server-computed confirmation window so clients render
// the countdown from server time, not their own clock.
type ReleaseView struct {
	*release.Release
	// Automatic: released without a person (see release.Release.Automatic).
	Automatic     bool       `json:"automatic"`
	ConfirmableAt *time.Time `json:"confirmableAt,omitempty"`
	// CanApprove: the viewer is an approver who has not decided yet.
	CanApprove bool       `json:"canApprove"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
}

func view(r *release.Release, policy settings.ReleasePolicy) ReleaseView {
	v := ReleaseView{Release: r, Automatic: r.Automatic()}
	if r.Items == nil {
		r.Items = []release.Item{}
	}
	if r.Status == release.Confirming && r.SubmittedAt != nil {
		ca, ea := r.SubmittedAt.Add(policy.ConfirmRead()), r.SubmittedAt.Add(policy.ConfirmTTL())
		v.ConfirmableAt, v.ExpiresAt = &ca, &ea
	}
	return v
}

// views renders releases with the current release policy.
func (a *API) views(c *gin.Context, list []release.Release) ([]ReleaseView, error) {
	policy, err := a.Settings.ReleasePolicy(c.Request.Context())
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseView, len(list))
	u := currentUser(c)
	groups := slices.Concat(u.Groups, u.LocalGroups)
	for i := range list {
		out[i] = view(&list[i], policy)
		out[i].CanApprove = canApprove(&list[i], u.Sub, groups)
	}
	return out, nil
}

func (a *API) view(c *gin.Context, r *release.Release) (ReleaseView, error) {
	vs, err := a.views(c, []release.Release{*r})
	if err != nil {
		return ReleaseView{}, err
	}
	return vs[0], nil
}

// respondRelease responds with one release view.
func (a *API) respondRelease(c *gin.Context, r *release.Release) {
	v, err := a.view(c, r)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, v)
}

var releaseStatuses = []string{"draft", "confirming", "approving", "executing", "succeeded", "failed", "cancelled", "rejected"}

func (a *API) listReleases(c *gin.Context) {
	p := newParams(c)
	f := pg.ReleaseFilter{
		Env:         p.match("env", reEnv, "label.envName"),
		ServiceLike: p.text("service", 100),
		Jira:        p.text("jira", 64),
		Kind:        p.enum("kind", []string{release.KindImage, release.KindRestart, release.KindSync}),
		Creator:     p.text("creator", 100),
		Since:       p.time("since"),
		Until:       p.time("until"),
	}
	if f.Since != nil && f.Until != nil && f.Since.After(*f.Until) {
		p.bad("since", "p.sinceAfterUntil")
	}
	for _, s := range p.enumList("status", releaseStatuses) {
		f.Statuses = append(f.Statuses, release.Status(s))
	}
	project := p.text("project", 100)
	awaiting := p.boolean("awaiting")
	if p.boolean("mine") {
		f.CreatedBy = currentUser(c).Sub
	}
	if p.boolean("decided") {
		f.DecidedBy = currentUser(c).Sub
	}
	page, size := p.page()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	vis, err := a.visibility(c, rbac.ReleasesView)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if seen := vis.names(); seen != nil {
		f.Services = intersect(f.Services, seen)
	}
	if project != "" {
		snap, err := a.Hub.Snapshot(c.Request.Context(), false)
		if err != nil {
			respond.Fail(c, err)
			return
		}
		byProject := []string{}
		for _, svc := range snap.Services {
			if svc.Project == project {
				byProject = append(byProject, svc.Name)
			}
		}
		f.Services = intersect(f.Services, byProject)
	}
	if awaiting {
		// Approving releases this viewer may still decide on; filtered here
		// because eligibility depends on the viewer's groups.
		f.Statuses = []release.Status{release.Approving}
		all, _, err := a.PG.Releases.List(c.Request.Context(), f, 1, 500)
		if err != nil {
			respond.Fail(c, err)
			return
		}
		vs, err := a.views(c, all)
		if err != nil {
			respond.Fail(c, err)
			return
		}
		mine := []ReleaseView{}
		for _, v := range vs {
			if v.CanApprove {
				mine = append(mine, v)
			}
		}
		from, to := min((page-1)*size, len(mine)), min(page*size, len(mine))
		respond.Page(c, mine[from:to], int64(len(mine)), page, size)
		return
	}
	list, total, err := a.PG.Releases.List(c.Request.Context(), f, page, size)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	vs, err := a.views(c, list)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.Page(c, vs, total, page, size)
}

type ItemLive struct {
	ItemID    int64          `json:"itemId"`
	Promotion *PromotionView `json:"promotion,omitempty"`
	Live      *Live          `json:"live,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func (a *API) getRelease(c *gin.Context) {
	p := newParams(c)
	id := p.releaseID()
	withLive := p.booleanOr("live", true)
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	rel, err := a.PG.Releases.Get(c.Request.Context(), id)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	v, err := a.view(c, rel)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.requireVisible(c, rel); err != nil {
		respond.Fail(c, err)
		return
	}
	can, err := a.releaseAbilities(c, rel)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	out := gin.H{"release": v, "can": can}
	// Live view only while it matters; history renders from stored state.
	recent := rel.FinishedAt != nil && time.Since(*rel.FinishedAt) < 2*time.Hour
	if withLive && (rel.Status == release.Executing || recent) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), upstreamTimeout)
		defer cancel()
		snap, _ := a.Hub.Snapshot(ctx, false)
		// One item is two upstream round trips — the promotion from Kargo and
		// the workload from Argo CD. Serially that is a batch of fifty taking
		// a hundred of them, which does not finish inside the three seconds
		// the page refreshes on: every poll returned data already stale, so a
		// running batch looked frozen and then turned green all at once when
		// the last one landed. They do not depend on each other, so they go
		// together, bounded so a large batch does not stampede the upstream.
		lives := make([]ItemLive, len(rel.Items))
		gather := make([]func(), 0, len(rel.Items))
		for i, it := range rel.Items {
			i, it := i, it
			gather = append(gather, func() {
				tg, err := rel.Target(it)
				if err != nil {
					return
				}
				il := ItemLive{ItemID: it.ID}
				tag := ""
				if pl, err := rel.ImagePayload(it); err == nil {
					tag = pl.To.Tag
					if cl, err := a.Hub.Named(ctx, pl.Upstream); err != nil {
						il.Error = errcode.From(err).Text(i18n.From(ctx))
					} else if it.ExternalRef != "" {
						if promo, err := cl.Kargo.GetPromotion(ctx, pl.Project, it.ExternalRef); err == nil {
							v := promotionView(promo)
							il.Promotion = &v
						} else {
							il.Error = errcode.From(err).Text(i18n.From(ctx))
						}
					}
				} else if pl, err := rel.RestartPayload(it); err == nil {
					tag = pl.Current.Tag
				} else if pl, err := rel.SyncPayload(it); err == nil {
					tag = pl.Current.Tag
				}
				if snap != nil {
					if d := deploymentIn(snap, tg.Service, tg.Env); d != nil {
						il.Live = a.live(ctx, d, tg.Digest, tag)
					}
				}
				lives[i] = il
			})
		}
		catalog.Parallel(8, gather...)
		// Items whose target could not be read leave a zero value behind.
		lives = slices.DeleteFunc(lives, func(l ItemLive) bool { return l.ItemID == 0 })
		out["live"] = lives
	}
	respond.OK(c, out)
}

type confirmReq struct {
	// Empty only when no item has a known digest (restarting an Application
	// that Kargo does not manage).
	Digests []string `json:"digests" binding:"max=50" label:"confirmedDigests"`
}

func (r *confirmReq) Check() error {
	for i, d := range r.Digests {
		if !reDigest.MatchString(d) {
			return validate.Collect(validate.FieldKey("digests."+itoa(i), "r.digestFormat"))
		}
	}
	return nil
}

// ownedBy reports whether u may act on rel as the person who started it.
// A release a person created is theirs: the reading window is their read, and
// nobody confirms or withdraws it on their behalf. A CI release has no such
// person — the "creator" is a token — so holding the environment's permission
// is what decides. Both the handlers and releaseAbilities go through here, so
// a button can never appear that the server would refuse.
func ownedBy(rel *release.Release, u *auth.User) bool {
	return rel.Source == release.SourceCI || rel.CreatedBy == u.Sub
}

func (a *API) confirmRelease(c *gin.Context) {
	p := newParams(c)
	id := p.releaseID()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req confirmReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	u := currentUser(c)
	rel, err := a.PG.Releases.Get(ctx, id)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.requireKinds(c, rel); err != nil {
		respond.Fail(c, err)
		return
	}
	// The digest check below still makes whoever confirms state what they
	// are releasing.
	if !ownedBy(rel, u) {
		_ = a.PG.Releases.AuditConfirmDenied(ctx, u.Actor(), rel, "only the creator can confirm")
		respond.Fail(c, errcode.NewKey(errcode.Forbidden, "r.onlyCreatorConfirms"))
		return
	}
	policy, err := a.Settings.ReleasePolicy(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.checkPolicy(ctx, policy, rel.Env, "", "", false); err != nil {
		_ = a.PG.Releases.AuditConfirmDenied(ctx, u.Actor(), rel, err.Error())
		respond.Fail(c, err)
		return
	}
	// The client must echo exactly the digests it showed: proof the person
	// confirmed this change and not a list that changed under them.
	want, got := map[string]bool{}, map[string]bool{}
	for _, it := range rel.Items {
		if tg, err := rel.Target(it); err == nil && tg.Digest != "" {
			want[tg.Digest] = true
		}
	}
	for _, d := range req.Digests {
		got[d] = true
	}
	if !sameSet(want, got) {
		_ = a.PG.Releases.AuditConfirmDenied(ctx, u.Actor(), rel, "confirmed digests do not match the release")
		respond.FailCode(c, errcode.DigestMismatch, "")
		return
	}
	rel, err = a.PG.Releases.Confirm(ctx, u.Actor(), rel.ID, policy.ConfirmRead(), policy.ConfirmTTL(), a.approvalRule(ctx, policy, rel))
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if rel.Status == release.Approving && a.Notifier != nil {
		a.Notifier.ReleaseEvent(ctx, rel, "approval_requested")
	}
	a.respondRelease(c, rel)
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

type cancelReq struct {
	Reason string `json:"reason" binding:"max=500" label:"cancelReason"`
}

func (r *cancelReq) Normalize() { trim(&r.Reason) }

func (a *API) cancelRelease(c *gin.Context) {
	p := newParams(c)
	id := p.releaseID()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req cancelReq
	if c.Request.ContentLength != 0 {
		if err := bindJSON(c, &req); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	ctx := c.Request.Context()
	rel, err := a.PG.Releases.Get(ctx, id)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	u := currentUser(c)
	check := a.requireKinds(c, rel)
	if !ownedBy(rel, u) {
		check = a.requireEach(c, rel, func(string) rbac.Permission { return rbac.ReleasesCancelAny })
	}
	if check != nil {
		respond.Fail(c, check)
		return
	}
	rel, err = a.PG.Releases.Cancel(ctx, u.Actor(), rel.ID, req.Reason)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if a.Notifier != nil {
		a.Notifier.ReleaseEvent(context.WithoutCancel(ctx), rel, "cancelled")
	}
	a.respondRelease(c, rel)
}

func (a *API) listAudit(c *gin.Context) {
	p := newParams(c)
	f := struct {
		jira, service, env, actor, action string
	}{
		jira:    p.text("jira", 64),
		service: p.match("service", reDNSName, "label.serviceName"),
		env:     p.match("env", reEnv, "label.envName"),
		actor:   p.text("actor", 255),
		action:  p.match("action", reAction, "label.action"),
	}
	since, until := p.time("since"), p.time("until")
	if since != nil && until != nil && since.After(*until) {
		p.bad("since", "p.sinceAfterUntil")
	}
	page, size := p.page()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	vis, err := a.visibility(c, rbac.AuditView)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	filter := auditFilter(f.jira, f.service, f.env, f.actor, f.action, since, until)
	// A viewer limited to some projects sees only records about those
	// services; records that belong to no service (logins, settings) need an
	// unrestricted grant.
	filter.Services = vis.names()
	items, total, err := a.PG.Audit.List(c.Request.Context(), filter, page, size)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.Page(c, items, total, page, size)
}

// checkBatch applies the service catalog's change rules:
//   - a release with several items is one kind of change, within one project,
//     and (with a batch dimension configured) one value of that dimension;
//   - with a batch dimension, a value may only change while every value
//     ordered before it has nothing in flight in the same project and env
//     (e.g. frontend waits for backend).
func (a *API) checkBatch(ctx context.Context, snap *catalog.Snapshot, req *createReleaseReq) error {
	cat, err := a.Settings.Catalog(ctx)
	if err != nil {
		return err
	}
	var active []release.Release
	if cat.BatchDimension != "" {
		if active, _, err = a.PG.Releases.List(ctx, pg.ReleaseFilter{Statuses: []release.Status{release.Confirming, release.Approving, release.Executing}, Env: req.Env}, 1, 100); err != nil {
			return err
		}
	}
	return batchViolation(cat, snap.Find, req.Env, req.Items, active)
}

// batchViolation is checkBatch without I/O: find resolves a service in the
// catalog, active are the env's in-flight releases.
func batchViolation(cat settings.Catalog, find func(string) *catalog.Service, env string, items []releaseItemReq, active []release.Release) error {
	dim, hasDim := cat.Dimension(cat.BatchDimension)
	first := find(items[0].Service)
	if first == nil {
		return nil // reported per item by the caller
	}
	typeOf := func(s *catalog.Service) string { return s.Dimensions[dim.Key] }
	if len(items) > 1 {
		var errs []*validate.FieldError
		for i, it := range items[1:] {
			svc := find(it.Service)
			if svc == nil {
				continue
			}
			f := itemField(i+1, "service")
			switch {
			case it.Kind != items[0].Kind:
				errs = append(errs, validate.FieldKey(itemField(i+1, "kind"), "r.batchSameKind"))
			case first.Project == "" || svc.Project != first.Project:
				errs = append(errs, validate.FieldKey(f, "r.batchSameProject",
					first.Name, orUnassigned(first.Project), svc.Name, orUnassigned(svc.Project)))
			case hasDim && (typeOf(first) == "" || typeOf(svc) != typeOf(first)):
				errs = append(errs, validate.FieldKey(f, "r.batchSameDimension", dim.Name,
					first.Name, orUnassigned(dim.ValueName(typeOf(first))), svc.Name, orUnassigned(dim.ValueName(typeOf(svc)))))
			}
		}
		if err := validate.Collect(errs...); err != nil {
			return err
		}
	}
	if !hasDim || first.Project == "" {
		return nil
	}
	rank := dim.Rank(typeOf(first))
	if rank <= 0 {
		return nil // first in order, or not part of the configured order
	}
	for _, r := range active {
		for _, it := range r.Items {
			tg, err := r.Target(it)
			if err != nil {
				continue
			}
			svc := find(tg.Service)
			if svc == nil || svc.Project != first.Project {
				continue
			}
			if v := typeOf(svc); dim.Rank(v) >= 0 && dim.Rank(v) < rank {
				return errcode.NewKey(errcode.OrderBlocked, "r.orderBlocked",
					first.Project, env, dim.ValueName(v), r.ID, dim.ValueName(typeOf(first)))
			}
		}
	}
	return nil
}

// orUnassigned returns the name, or a key that renders as "unassigned" in the
// reader's language: it is substituted into a sentence, and i18n.T renders an
// argument that is itself a key in the same locale as the sentence.
func orUnassigned(s string) any {
	if s == "" {
		return i18n.Key("r.unassigned")
	}
	return s
}

// kindPermission is the environment permission an item kind needs.
func kindPermission(kind string) rbac.Permission {
	switch kind {
	case release.KindRestart:
		return rbac.ReleasesRestart
	case release.KindSync:
		return rbac.ReleasesSync
	}
	return rbac.ReleasesCreate
}

// intersect keeps the values present in both lists; a nil first list means
// "not filtered yet" and yields the second.
func intersect(a, b []string) []string {
	if a == nil {
		return b
	}
	out := []string{}
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// requireVisible refuses a release touching a service the viewer may not see.
func (a *API) requireVisible(c *gin.Context, rel *release.Release) error {
	vis, err := a.visibility(c, rbac.ReleasesView)
	if err != nil {
		return err
	}
	for _, it := range rel.Items {
		tg, err := rel.Target(it)
		if err != nil {
			continue
		}
		if !vis.env(tg.Service, rel.Env) {
			a.deny(c, string(rbac.ReleasesView)+"@"+rel.Env, rel.ID)
			return errcode.New(errcode.ReleaseNotFound, "")
		}
	}
	return nil
}

// releaseAbilities says what the viewer may do on rel, with the same scoped
// checks the endpoints apply (see ownedBy).
func (a *API) releaseAbilities(c *gin.Context, rel *release.Release) (gin.H, error) {
	g, err := a.grants(c)
	if err != nil {
		return nil, err
	}
	ctx := c.Request.Context()
	all := func(perm func(kind string) rbac.Permission) bool {
		for _, it := range rel.Items {
			tg, err := rel.Target(it)
			if err != nil || !g.HasTarget(perm(it.Kind), a.targetOf(ctx, tg.Service, rel.Env)) {
				return false
			}
		}
		return true
	}
	mine := ownedBy(rel, currentUser(c))
	return gin.H{
		"confirm": mine && all(kindPermission),
		"cancel":  (mine && all(kindPermission)) || all(func(string) rbac.Permission { return rbac.ReleasesCancelAny }),
		"pods":    all(func(string) rbac.Permission { return rbac.PodsView }),
	}, nil
}

// requireKinds checks, for every item of rel, the permission its kind needs
// on its service.
func (a *API) requireKinds(c *gin.Context, rel *release.Release) error {
	return a.requireEach(c, rel, kindPermission)
}

// requireEach checks perm(kind) on the service of every item in rel.
func (a *API) requireEach(c *gin.Context, rel *release.Release, perm func(kind string) rbac.Permission) error {
	for _, it := range rel.Items {
		tg, err := rel.Target(it)
		if err != nil {
			return err
		}
		if err := a.requireTarget(c, perm(it.Kind), tg.Service, rel.Env, rel.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkPolicy applies change freezes and, when creating (withJira), the
// required fields and the Jira project allow-list.
func (a *API) checkPolicy(ctx context.Context, policy settings.ReleasePolicy, env, jira, reason string, withJira bool) error {
	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		return err
	}
	tier := ""
	if e, ok := envs.Named(env); ok {
		tier = e.Tier
	}
	for _, f := range policy.ActiveFreezes(time.Now()) {
		if rbac.EnvMatches(f.Envs, env, tier) {
			return &release.FrozenError{Env: env, Name: f.Name, Until: f.EndsAt, Reason: f.Reason}
		}
	}
	if !withJira {
		return nil
	}
	var missing []*validate.FieldError
	if jira == "" && rbac.EnvMatches(policy.JiraRequired, env, tier) {
		missing = append(missing, validate.FieldKey("jiraTicket", "r.jiraRequiredHere"))
	}
	if reason == "" && rbac.EnvMatches(policy.ReasonRequired, env, tier) {
		missing = append(missing, validate.FieldKey("reason", "r.reasonRequiredHere"))
	}
	if err := validate.Collect(missing...); err != nil {
		return err
	}
	if jira != "" && len(policy.JiraProjects) > 0 {
		project, _, _ := strings.Cut(jira, "-")
		if !slices.Contains(policy.JiraProjects, project) {
			return validate.Errors{{Field: "jiraTicket", Key: "r.jiraProjectNotAllowed",
				Args: []any{project, strings.Join(policy.JiraProjects, " / ")}}}
		}
	}
	return nil
}

// approvalRule returns the approval rule for rel under policy, nil if none.
// A batch is one project and one type, so its first item decides.
func (a *API) approvalRule(ctx context.Context, policy settings.ReleasePolicy, rel *release.Release) *release.ApprovalRule {
	tier := ""
	if envs, err := a.Settings.Environments(ctx); err == nil {
		if e, ok := envs.Named(rel.Env); ok {
			tier = e.Tier
		}
	}
	t := rbac.Target{Env: rel.Env}
	if len(rel.Items) > 0 {
		if tg, err := rel.Target(rel.Items[0]); err == nil {
			t = a.targetOf(ctx, tg.Service, rel.Env)
		}
	}
	return policy.ApprovalFor(rel.Env, tier, t.Project, t.Type)
}

type approveReq struct {
	Note string `json:"note" binding:"max=500" label:"approveNote"`
}

func (r *approveReq) Normalize() { trim(&r.Note) }

type rejectReq struct {
	Note string `json:"note" binding:"required,min=2,max=500" label:"rejectNote"`
}

func (r *rejectReq) Normalize() { trim(&r.Note) }

func (a *API) approveRelease(c *gin.Context) {
	var req approveReq
	if c.Request.ContentLength != 0 {
		if err := bindJSON(c, &req); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	a.decide(c, true, req.Note)
}

func (a *API) rejectRelease(c *gin.Context) {
	var req rejectReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	a.decide(c, false, req.Note)
}

// canApprove: the viewer may still decide on this approving release.
func canApprove(r *release.Release, sub string, groups []string) bool {
	if r.Status != release.Approving || r.ApprovalRule == nil || !r.ApprovalRule.Eligible(sub, groups, r.CreatedBy) {
		return false
	}
	for _, d := range r.Approvals {
		if d.Sub == sub {
			return false
		}
	}
	return true
}

func (a *API) decide(c *gin.Context, approve bool, note string) {
	p := newParams(c)
	id := p.releaseID()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	u := currentUser(c)
	if cur, err := a.PG.Releases.Get(c.Request.Context(), id); err == nil {
		if err := a.requireVisible(c, cur); err != nil {
			respond.Fail(c, err)
			return
		}
	}
	rel, started, err := a.PG.Releases.Decide(c.Request.Context(), u.Actor(), id, slices.Concat(u.Groups, u.LocalGroups), approve, note)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	// An approval that starts the release is announced by the executor ("started").
	if !approve && a.Notifier != nil {
		a.Notifier.ReleaseEvent(c.Request.Context(), rel, "rejected")
	}
	_ = started
	a.Hub.Reset()
	a.respondRelease(c, rel)
}

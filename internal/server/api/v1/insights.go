package v1

import (
	"errors"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tide/internal/insights"
	"tide/internal/rbac"
	"tide/internal/server/api/respond"
	"tide/internal/store/pg"
)

// maxUntouched caps the "never released" list. The count is the point; the
// names are there to make it actionable, not to be a full inventory.
const maxUntouched = 50

// getInsights reports on the release history. It reads the same records the
// releases and audit pages read, under the same scope: a viewer limited to
// some projects gets numbers for those projects only.
func (a *API) getInsights(c *gin.Context) {
	p := newParams(c)
	from, to := p.time("from"), p.time("to")
	env := p.match("env", reEnv, "label.envName")
	loc := p.location("tz")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	r, err := insights.NewRange(from, to, time.Now())
	switch {
	case errors.Is(err, insights.ErrRangeOrder):
		p.bad("from", "p.sinceAfterUntil")
	case errors.Is(err, insights.ErrRangeTooWide):
		p.bad("from", "i.rangeTooWide", int(insights.MaxRange/(24*time.Hour)))
	}
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}

	vis, err := a.visibility(c, rbac.AuditView)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	f := pg.InsightsFilter{Range: r, Services: vis.names(), Env: env, Location: loc}

	// Workload per person is only shown to a viewer who can see every
	// environment: a partial count invites a comparison that is not true.
	rep, err := a.PG.Insights.Report(ctx, f, vis.all)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	// Coverage and project names need the catalog, which needs the upstreams.
	// Those can be unconfigured or unreachable while the release history is
	// perfectly readable, so a catalog failure costs those two things and not
	// the whole page.
	if err := a.coverage(c, f, rep, vis); err != nil {
		zap.L().Warn("insights coverage unavailable", zap.Error(err))
		rep.Risk.Coverage = insights.Coverage{Untouched: []string{}}
	}
	if policy, err := a.Settings.ReleasePolicy(ctx); err == nil {
		rep.Process.ConfirmSeconds = policy.ConfirmReadSeconds
	}
	// Projects come from the catalog too; missing ones just leave the field
	// empty, and the client groups by service name.
	a.tagProjects(c, rep)
	respond.OK(c, rep)
}

// coverage answers "is Tide how things actually get released": every service
// the catalog lists, against the ones this period touched.
func (a *API) coverage(c *gin.Context, f pg.InsightsFilter, rep *insights.Report, vis *visibility) error {
	ctx := c.Request.Context()
	snap, err := a.Hub.Snapshot(ctx, false)
	if err != nil {
		return err
	}
	released, err := a.PG.Insights.ReleasedServices(ctx, f)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(released))
	for _, s := range released {
		seen[s] = true
	}
	cov := &rep.Risk.Coverage
	cov.Untouched = []string{}
	for i := range snap.Services {
		svc := &snap.Services[i]
		if !vis.service(svc) {
			continue
		}
		// An environment filter makes "untouched" mean something narrower
		// than the catalog can answer, so only the count is reported then.
		cov.Catalog++
		if seen[svc.Name] {
			cov.Released++
		} else if len(cov.Untouched) < maxUntouched {
			cov.Untouched = append(cov.Untouched, svc.Name)
		}
	}
	slices.Sort(cov.Untouched)
	return nil
}

// tagProjects fills in each service's project from the catalog so the client
// can group without a second lookup.
func (a *API) tagProjects(c *gin.Context, rep *insights.Report) {
	snap, err := a.Hub.Snapshot(c.Request.Context(), false)
	if err != nil {
		return
	}
	for i := range rep.Activity.Services {
		if svc := snap.Find(rep.Activity.Services[i].Service); svc != nil {
			rep.Activity.Services[i].Project = svc.Project
		}
	}
}

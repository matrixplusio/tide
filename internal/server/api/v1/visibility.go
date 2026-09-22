package v1

import (
	"github.com/gin-gonic/gin"

	"tide/internal/catalog"
	"tide/internal/rbac"
)

// visibility answers "which services may this viewer see" for one permission.
// Grants can be narrowed to projects and service types, so listings filter on
// it and detail endpoints check it; a viewer who may see nothing sees an empty
// list, not someone else's services.
type visibility struct {
	grants *rbac.Grants
	perm   rbac.Permission
	snap   *catalog.Snapshot
	// dimension is the catalog key whose value scopes count as a service type.
	dimension string
	// all: the viewer holds the permission without any project / type limit,
	// so nothing needs filtering (and records without a service are visible).
	all bool
}

func (a *API) visibility(c *gin.Context, perm rbac.Permission) (*visibility, error) {
	g, err := a.grants(c)
	if err != nil {
		return nil, err
	}
	ctx := c.Request.Context()
	v := &visibility{grants: g, perm: perm, all: g.Unrestricted(perm)}
	if cat, err := a.Settings.Catalog(ctx); err == nil {
		v.dimension = cat.BatchDimension
	}
	// The catalog is only needed to resolve projects and types.
	if !v.all {
		if v.snap, err = a.Hub.Snapshot(ctx, false); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// service reports whether svc is visible in at least one of its environments.
func (v *visibility) service(svc *catalog.Service) bool {
	if v.all {
		return true
	}
	for env := range svc.Envs {
		if v.grants.HasTarget(v.perm, v.target(svc, env)) {
			return true
		}
	}
	return false
}

// env reports whether one service in one environment is visible.
func (v *visibility) env(service, env string) bool {
	if v.all {
		return true
	}
	svc := v.snap.Find(service)
	if svc == nil {
		return false
	}
	return v.grants.HasTarget(v.perm, v.target(svc, env))
}

func (v *visibility) target(svc *catalog.Service, env string) rbac.Target {
	t := rbac.Target{Env: env, Project: svc.Project}
	if v.dimension != "" {
		t.Type = svc.Dimensions[v.dimension]
	}
	return t
}

// services filters a catalog listing.
func (v *visibility) services(in []catalog.Service) []catalog.Service {
	if v.all {
		return in
	}
	out := make([]catalog.Service, 0, len(in))
	for i := range in {
		if v.service(&in[i]) {
			out = append(out, in[i])
		}
	}
	return out
}

// names lists the visible service names, or nil when everything is visible.
// Callers pass it to a store filter; an empty (non-nil) list matches nothing.
func (v *visibility) names() []string {
	if v.all {
		return nil
	}
	out := []string{}
	for i := range v.snap.Services {
		if v.service(&v.snap.Services[i]) {
			out = append(out, v.snap.Services[i].Name)
		}
	}
	return out
}

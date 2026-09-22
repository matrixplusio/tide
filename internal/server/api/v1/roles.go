package v1

import (
	"slices"

	"github.com/gin-gonic/gin"

	"tide/internal/access"
	"tide/internal/i18n"
	"tide/internal/rbac"
	"tide/internal/server/api/respond"
	"tide/internal/store/pg"
)

type permissionView struct {
	rbac.Def
	// Name and Description shadow Def's keys with the rendered text.
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Routes      []Route `json:"routes"`
}

type tierView struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// permissionCatalog lists every permission with the API routes that declare it.
func (a *API) permissionCatalog(c *gin.Context) {
	loc := i18n.From(c.Request.Context())
	items := make([]permissionView, len(rbac.Catalog))
	for i, d := range rbac.Catalog {
		items[i] = permissionView{Def: d, Name: i18n.T(loc, d.Name), Description: i18n.T(loc, d.Description), Routes: []Route{}}
		for _, r := range a.routes {
			if slices.Contains(r.Global, d.Key) || slices.Contains(r.Env, d.Key) || slices.Contains(r.Dynamic, d.Key) {
				items[i].Routes = append(items[i].Routes, r)
			}
		}
	}
	tiers := make([]tierView, len(rbac.Tiers))
	for i, t := range rbac.Tiers {
		tiers[i] = tierView{Key: t.Key, Name: i18n.T(loc, t.Name)}
	}
	respond.OK(c, gin.H{"items": items, "tiers": tiers})
}

func (a *API) listRoles(c *gin.Context) {
	items, err := a.PG.Access.ListRoles(c.Request.Context())
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"items": items})
}

type roleReq struct {
	ID          string   `json:"id" label:"roleId"`
	Name        string   `json:"name" label:"roleName"`
	Description string   `json:"description" label:"description"`
	Permissions []string `json:"permissions" binding:"max=50" label:"permissions"`
}

func (r *roleReq) Normalize() { trim(&r.ID, &r.Name, &r.Description) }

func (r roleReq) input() access.RoleInput {
	return access.RoleInput{ID: r.ID, Name: r.Name, Description: r.Description, Permissions: r.Permissions}
}

func (a *API) createRole(c *gin.Context) {
	var req roleReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	role, err := a.Access.CreateRole(c.Request.Context(), currentUser(c).Actor(), req.input())
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, role)
}

type roleUpdateReq struct {
	Name        string   `json:"name" label:"roleName"`
	Description string   `json:"description" label:"description"`
	Permissions []string `json:"permissions" binding:"max=50" label:"permissions"`
}

func (r *roleUpdateReq) Normalize() { trim(&r.Name, &r.Description) }

func (a *API) updateRole(c *gin.Context) {
	p := newParams(c)
	id := p.role()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req roleUpdateReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	role, err := a.Access.UpdateRole(c.Request.Context(), currentUser(c).Actor(),
		access.RoleInput{ID: id, Name: req.Name, Description: req.Description, Permissions: req.Permissions})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, role)
}

func (a *API) deleteRole(c *gin.Context) {
	p := newParams(c)
	id := p.role()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.DeleteRole(c.Request.Context(), currentUser(c).Actor(), id); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

func (a *API) listBindings(c *gin.Context) {
	p := newParams(c)
	f := pg.BindingFilter{RoleID: p.match("role", access.RoleIDRe, "label.roleId"), Subject: p.text("subject", 262)}
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	items, err := a.PG.Access.ListBindings(c.Request.Context(), f)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"items": items})
}

type bindingReq struct {
	RoleID   string   `json:"roleId" binding:"required" label:"role"`
	Subject  string   `json:"subject" binding:"required,max=262" label:"subject"`
	Envs     []string `json:"envs" binding:"max=50" label:"envScope"`
	Projects []string `json:"projects" label:"projectScope"`
	Types    []string `json:"types" label:"kindScope"`
}

func (r *bindingReq) Normalize() {
	trim(&r.RoleID, &r.Subject)
	trimAll(r.Envs, r.Projects, r.Types)
}

func trimAll(lists ...[]string) {
	for _, l := range lists {
		for i := range l {
			trim(&l[i])
		}
	}
}

func (a *API) createBinding(c *gin.Context) {
	var req bindingReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	b, err := a.Access.CreateBinding(c.Request.Context(), currentUser(c).Actor(),
		access.BindingInput{RoleID: req.RoleID, Subject: req.Subject, Envs: req.Envs, Projects: req.Projects, Types: req.Types})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, b)
}

type bindingScopeReq struct {
	Envs     []string `json:"envs" binding:"max=50" label:"envScope"`
	Projects []string `json:"projects" label:"projectScope"`
	Types    []string `json:"types" label:"kindScope"`
}

func (r *bindingScopeReq) Normalize() { trimAll(r.Envs, r.Projects, r.Types) }

func (a *API) updateBinding(c *gin.Context) {
	p := newParams(c)
	id := p.id("binding", "label.bindingId")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req bindingScopeReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	b, err := a.Access.UpdateBinding(c.Request.Context(), currentUser(c).Actor(), id, pg.BindingScope{Envs: req.Envs, Projects: req.Projects, Types: req.Types})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, b)
}

func (a *API) deleteBinding(c *gin.Context) {
	p := newParams(c)
	id := p.id("binding", "label.bindingId")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.DeleteBinding(c.Request.Context(), currentUser(c).Actor(), id); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

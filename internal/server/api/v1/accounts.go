package v1

import (
	"github.com/gin-gonic/gin"

	"tide/internal/access"
	"tide/internal/rbac"
	"tide/internal/server/api/respond"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

// ---- users ----

func (a *API) listUsers(c *gin.Context) {
	p := newParams(c)
	f := pg.UserFilter{Query: p.text("q", 100)}
	f.Method = p.enum("method", []string{"local", "oidc"})
	switch p.enum("status", []string{"enabled", "disabled"}) {
	case "enabled":
		f.Disabled = new(bool)
	case "disabled":
		t := true
		f.Disabled = &t
	}
	page, size := p.page()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	items, total, err := a.PG.Accounts.ListUsers(c.Request.Context(), f, page, size)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.Page(c, items, total, page, size)
}

type createUserReq struct {
	Username        string `json:"username" binding:"required" label:"username"`
	Name            string `json:"name" label:"displayName"`
	Password        string `json:"password" binding:"required" label:"password"`
	ConfirmPassword string `json:"confirmPassword" binding:"required" label:"confirmPassword"`
}

func (r *createUserReq) Normalize() { trim(&r.Username, &r.Name) }

func (r *createUserReq) Check() error {
	return validate.Collect(
		validate.Username("username", r.Username),
		validate.DisplayName("name", r.Name),
		validate.Password("password", r.Username, r.Password),
		validate.Confirm("confirmPassword", r.Password, r.ConfirmPassword),
	)
}

func (a *API) createUser(c *gin.Context) {
	var req createUserReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	u, err := a.Auth.CreateUser(c.Request.Context(), currentUser(c), req.Username, req.Name, req.Password)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, u)
}

// userParam reads and validates :user.
func userParam(c *gin.Context) (int64, error) {
	p := newParams(c)
	id := p.id("user", "label.userId")
	return id, p.err()
}

func (a *API) getUser(c *gin.Context) {
	id, err := userParam(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	u, err := a.PG.Accounts.UserByID(ctx, id)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if u == nil {
		respond.Fail(c, access.ErrUserNotFound)
		return
	}
	policy, err := a.Access.Policy(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	bindings := policy.Match(access.Subject(u))
	if bindings == nil {
		bindings = []rbac.EffectiveBinding{}
	}
	n, err := a.PG.Accounts.CountSessions(ctx, u.ID)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"user": u, "bindings": bindings, "sessionCount": n})
}

func (a *API) renameUser(c *gin.Context) {
	id, err := userParam(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	var req profileReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	u, err := a.Auth.Rename(c.Request.Context(), currentUser(c), id, req.Name)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, u)
}

type resetPasswordReq struct {
	NewPassword     string `json:"newPassword" binding:"required" label:"newPassword"`
	ConfirmPassword string `json:"confirmPassword" binding:"required" label:"confirmPassword"`
}

// Check runs the policy without the username rule; the service applies the
// full policy once it knows whose account this is.
func (r *resetPasswordReq) Check() error {
	return validate.Collect(
		validate.Password("newPassword", "", r.NewPassword),
		validate.Confirm("confirmPassword", r.NewPassword, r.ConfirmPassword),
	)
}

func (a *API) resetPassword(c *gin.Context) {
	id, err := userParam(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	var req resetPasswordReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Auth.ResetPassword(c.Request.Context(), currentUser(c), id, req.NewPassword); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

type setDisabledReq struct {
	Disabled *bool `json:"disabled" binding:"required" label:"disabled"`
}

func (a *API) setUserDisabled(c *gin.Context) {
	id, err := userParam(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	var req setDisabledReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Auth.SetDisabled(c.Request.Context(), currentUser(c), id, *req.Disabled); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

func (a *API) revokeUserSessions(c *gin.Context) {
	id, err := userParam(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Auth.RevokeSessions(c.Request.Context(), currentUser(c), id); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

// ---- groups ----

func (a *API) listGroups(c *gin.Context) {
	items, err := a.PG.Access.ListGroups(c.Request.Context())
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"items": items})
}

type groupReq struct {
	Name        string `json:"name" label:"groupName"`
	Description string `json:"description" label:"description"`
}

func (r *groupReq) Normalize() { trim(&r.Name, &r.Description) }

func (a *API) createGroup(c *gin.Context) {
	var req groupReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.CreateGroup(c.Request.Context(), currentUser(c).Actor(), req.Name, req.Description); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

type groupDescriptionReq struct {
	Description string `json:"description" label:"description"`
}

func (r *groupDescriptionReq) Normalize() { trim(&r.Description) }

func (a *API) updateGroup(c *gin.Context) {
	p := newParams(c)
	name := p.group()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req groupDescriptionReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.UpdateGroup(c.Request.Context(), currentUser(c).Actor(), name, req.Description); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

func (a *API) deleteGroup(c *gin.Context) {
	p := newParams(c)
	name := p.group()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.DeleteGroup(c.Request.Context(), currentUser(c).Actor(), name); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

func (a *API) groupMembers(c *gin.Context) {
	p := newParams(c)
	name := p.group()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	ok, err := a.PG.Access.GroupExists(ctx, name)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if !ok {
		respond.Fail(c, access.ErrGroupNotFound)
		return
	}
	items, err := a.PG.Access.GroupMembers(ctx, a.PG.Accounts, name)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"items": items})
}

type addMembersReq struct {
	UserIDs []int64 `json:"userIds" binding:"required,min=1,max=100,dive,gt=0" label:"user"`
}

func (a *API) addGroupMembers(c *gin.Context) {
	p := newParams(c)
	name := p.group()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	var req addMembersReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.AddMembers(c.Request.Context(), currentUser(c).Actor(), name, req.UserIDs); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

func (a *API) removeGroupMember(c *gin.Context) {
	p := newParams(c)
	name := p.group()
	id := p.id("user", "label.userId")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Access.RemoveMember(c.Request.Context(), currentUser(c).Actor(), name, id); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

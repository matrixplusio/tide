package v1

import (
	"time"

	"github.com/gin-gonic/gin"

	"tide/internal/audit"
	"tide/internal/auth"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/settings"
	"tide/internal/validate"
	"tide/internal/version"
)

type environmentInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Tier        string `json:"tier"`
	Description string `json:"description"`
}

type approvalInfo struct {
	Envs     []string             `json:"envs"`
	Projects []string             `json:"projects"`
	Types    []string             `json:"types"`
	Rule     release.ApprovalRule `json:"rule"`
}

type appInfo struct {
	SiteName     string                 `json:"siteName"`
	Announcement *settings.Announcement `json:"announcement,omitempty"`
	JiraBaseURL  string                 `json:"jiraBaseUrl,omitempty"`
	// JiraRequired lists the environments where a Jira ticket is mandatory.
	JiraRequired []string `json:"jiraRequired"`
	// ReasonRequired lists the environments where a reason is mandatory.
	ReasonRequired []string `json:"reasonRequired"`
	// SoakEnforced / VersionJumpEnforced list the environments where those
	// thresholds block a release; the values are MinSoakMinutes / MultiVersionJump.
	SoakEnforced        []string `json:"soakEnforced"`
	VersionJumpEnforced []string `json:"versionJumpEnforced"`
	// ConfigDriftEnforced lists the environments where unsynced config blocks an upgrade.
	ConfigDriftEnforced []string `json:"configDriftEnforced"`
	MinSoakMinutes      int      `json:"minSoakMinutes"`
	MultiVersionJump    int      `json:"multiVersionJump"`
	// Approvals lists the approval rules with environments resolved to names,
	// in policy order; clients pick one per service like ApprovalFor does.
	Approvals          []approvalInfo    `json:"approvals"`
	ConfirmReadSeconds int               `json:"confirmReadSeconds"`
	ActiveFreezes      []settings.Freeze `json:"activeFreezes"`
	// Dimensions are the service catalog filters (see settings.Catalog).
	Dimensions []settings.Dimension `json:"dimensions"`
	// BatchDimension is the dimension key batch releases may not mix ("" = none).
	BatchDimension string `json:"batchDimension"`
	// Version is what this server is, shown in the sidebar. "which build am
	// I looking at" should be answerable without shell access to the pod.
	Version string `json:"version"`
}

func (a *API) me(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	g, err := a.grants(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	sys, err := a.Settings.System(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	policy, err := a.Settings.ReleasePolicy(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	envPerms, can := map[string][]rbac.Permission{}, map[string]bool{}
	jiraRequired, reasonRequired, soakEnforced, jumpEnforced, driftEnforced := []string{}, []string{}, []string{}, []string{}, []string{}
	approvals := make([]approvalInfo, len(policy.Approvals))
	for i, ap := range policy.Approvals {
		approvals[i] = approvalInfo{Envs: []string{}, Projects: orEmpty(ap.Projects), Types: orEmpty(ap.Types), Rule: ap.ApprovalRule}
	}
	rbacEnvs := make([]rbac.Env, len(envs.Items))
	infos := make([]environmentInfo, len(envs.Items))
	for i, e := range envs.Items {
		infos[i] = environmentInfo{Name: e.Name, DisplayName: e.DisplayName, Tier: e.Tier, Description: e.Description}
		envPerms[e.Name] = g.ForEnv(e.Name)
		can[e.Name] = g.HasEnv(rbac.ReleasesCreate, e.Name)
		if rbac.EnvMatches(policy.JiraRequired, e.Name, e.Tier) {
			jiraRequired = append(jiraRequired, e.Name)
		}
		if rbac.EnvMatches(policy.ReasonRequired, e.Name, e.Tier) {
			reasonRequired = append(reasonRequired, e.Name)
		}
		rbacEnvs[i] = rbac.Env{Name: e.Name, Tier: e.Tier}
		for j, ap := range policy.Approvals {
			if rbac.EnvMatches(ap.Envs, e.Name, e.Tier) {
				approvals[j].Envs = append(approvals[j].Envs, e.Name)
			}
		}
		if policy.MinSoakMinutes > 0 && rbac.EnvMatches(policy.SoakEnforced, e.Name, e.Tier) {
			soakEnforced = append(soakEnforced, e.Name)
		}
		if rbac.EnvMatches(policy.ConfigDriftEnforced, e.Name, e.Tier) {
			driftEnforced = append(driftEnforced, e.Name)
		}
		if policy.MultiVersionJump > 0 && rbac.EnvMatches(policy.VersionJumpEnforced, e.Name, e.Tier) {
			jumpEnforced = append(jumpEnforced, e.Name)
		}
	}
	cat, err := a.Settings.Catalog(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	app := appInfo{SiteName: sys.SiteName, JiraBaseURL: policy.JiraBaseURL, JiraRequired: jiraRequired, ReasonRequired: reasonRequired,
		SoakEnforced: soakEnforced, VersionJumpEnforced: jumpEnforced, ConfigDriftEnforced: driftEnforced, MinSoakMinutes: policy.MinSoakMinutes, MultiVersionJump: policy.MultiVersionJump, Approvals: approvals, Dimensions: cat.Dimensions, BatchDimension: cat.BatchDimension,
		ConfirmReadSeconds: int(policy.ConfirmRead().Seconds()), ActiveFreezes: policy.ActiveFreezes(time.Now()),
		Version: version.Get().Short()}
	if sys.Announcement.Enabled && sys.Announcement.Text != "" {
		app.Announcement = &sys.Announcement
	}
	// What the navigation needs: whether the permission is held anywhere at
	// all. What is inside each page is filtered per service.
	canView := gin.H{}
	for _, p := range []rbac.Permission{rbac.ServicesView, rbac.ReleasesView, rbac.AuditView} {
		canView[string(p)] = g.HasAnyTarget(p)
	}
	respond.OK(c, gin.H{
		"user": u, "permissions": g.Global(), "envPermissions": envPerms, "scopedGrants": g.ScopedGrants(rbacEnvs), "canView": canView, "canOperate": can,
		"envOrder": envs.Order(), "environments": infos, "app": app,
	})
}

type profileReq struct {
	Name string `json:"name" label:"displayName"`
}

func (r *profileReq) Normalize() { trim(&r.Name) }

func (a *API) updateProfile(c *gin.Context) {
	var req profileReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	u := currentUser(c)
	out, err := a.Auth.Rename(c.Request.Context(), u, u.ID, req.Name)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, out)
}

type changePasswordReq struct {
	CurrentPassword string `json:"currentPassword" binding:"required,max=1024" label:"currentPassword"`
	NewPassword     string `json:"newPassword" binding:"required" label:"newPassword"`
	ConfirmPassword string `json:"confirmPassword" binding:"required" label:"confirmPassword"`
	username        string
}

func (r *changePasswordReq) Check() error {
	errs := []*validate.FieldError{
		validate.Password("newPassword", r.username, r.NewPassword),
		validate.Confirm("confirmPassword", r.NewPassword, r.ConfirmPassword),
	}
	if r.CurrentPassword != "" && r.CurrentPassword == r.NewPassword {
		errs = append(errs, validate.FieldKey("newPassword", "au.newPasswordSame"))
	}
	return validate.Collect(errs...)
}

func (a *API) changeOwnPassword(c *gin.Context) {
	u := currentUser(c)
	if !u.IsLocal() {
		respond.FailCode(c, errcode.SSOPasswordManaged, "")
		return
	}
	req := changePasswordReq{username: u.Username}
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.Auth.ChangePassword(c.Request.Context(), u, req.CurrentPassword, req.NewPassword); err != nil {
		respond.Fail(c, err)
		return
	}
	clearCookie(c, sessionCookie, "/")
	respond.OK(c, nil)
}

func (a *API) mySessions(c *gin.Context) {
	u := currentUser(c)
	items, err := a.PG.Accounts.ListSessions(c.Request.Context(), u.ID)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	token, _ := cookie(c, sessionCookie)
	current := sessionIDOf(token)
	for i := range items {
		items[i].Current = items[i].ID == current
	}
	respond.OK(c, gin.H{"items": items})
}

func sessionIDOf(token string) string {
	if token == "" {
		return ""
	}
	return auth.SessionID(token)
}

func (a *API) deleteMySession(c *gin.Context) {
	p := newParams(c)
	id := p.session()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	token, _ := cookie(c, sessionCookie)
	if err := a.Auth.DeleteOwnSession(c.Request.Context(), currentUser(c), token, id); err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, nil)
}

func (a *API) myBindings(c *gin.Context) {
	g, err := a.grants(c)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	items := g.Bindings()
	if items == nil {
		items = []rbac.EffectiveBinding{}
	}
	respond.OK(c, gin.H{"items": items})
}

func (a *API) myActivity(c *gin.Context) {
	p := newParams(c)
	page, size := p.page()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	items, total, err := a.PG.Audit.List(c.Request.Context(), audit.Filter{ActorSub: currentUser(c).Sub}, page, size)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.Page(c, items, total, page, size)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Package v1 is the /api/v1 HTTP surface. Handlers only bind, validate, call
// domain services and respond (CONVENTIONS.md §2, §4); they never write JSON
// directly and never trust a client-provided value without checking it.
package v1

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tide/internal/access"
	"tide/internal/audit"
	"tide/internal/auth"
	"tide/internal/catalog"
	"tide/internal/ci"
	"tide/internal/i18n"
	"tide/internal/notify"
	"tide/internal/rbac"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/settings"
	"tide/internal/setup"
	"tide/internal/store/pg"
)

type Deps struct {
	PG       *pg.Store
	Settings *settings.Store
	Auth     *auth.Service
	Access   *access.Service
	Setup    *setup.Service
	Hub      *catalog.Hub
	Notifier *notify.Notifier
	CI       *ci.Service
}

type API struct {
	Deps
	routes []Route
}

// Route is one registered endpoint and the permissions it declares. The
// list backs GET /rbac/permissions so administrators can see what each
// permission opens.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Any of these global permissions is enough; empty = signed in.
	Global []rbac.Permission `json:"-"`
	// Env is checked by the handler against the target environment.
	Env []rbac.Permission `json:"-"`
	// Section routes check a permission that depends on the path.
	Dynamic []rbac.Permission `json:"-"`
}

const (
	sessionCookie = "tide_session"
	ssoFlowCookie = "tide_sso_flow"
	setupCookie   = "tide_setup"

	ctxUser    = "user"
	ctxGrants  = "grants"
	ctxCIToken = "ci_token"

	apiPrefix = "/api/v1"
)

type guard struct {
	global  []rbac.Permission
	env     []rbac.Permission
	scoped  []rbac.Permission
	dynamic []rbac.Permission
}

func need(ps ...rbac.Permission) guard { return guard{global: ps} }

// needScoped: the permission must be held somewhere; the handler then filters
// to what the viewer may actually see.
func needScoped(ps ...rbac.Permission) guard  { return guard{scoped: ps} }
func needEnv(ps ...rbac.Permission) guard     { return guard{env: ps} }
func needSection(ps ...rbac.Permission) guard { return guard{dynamic: ps} }

var signedIn = guard{}

func (a *API) handle(g *gin.RouterGroup, method, path string, gd guard, h gin.HandlerFunc) {
	a.routes = append(a.routes, Route{Method: method, Path: apiPrefix + path, Global: gd.global, Env: gd.env, Dynamic: gd.dynamic})
	handlers := []gin.HandlerFunc{}
	if len(gd.global) > 0 {
		handlers = append(handlers, a.requireAny(gd.global...))
	}
	if len(gd.scoped) > 0 {
		handlers = append(handlers, a.requireAnyScope(gd.scoped...))
	}
	g.Handle(method, path, append(handlers, h)...)
}

func Register(r *gin.Engine, d Deps) {
	a := &API{Deps: d}
	g := r.Group(apiPrefix, a.auditMeta, a.generations, a.setupGate, a.loadUser)

	// Public.
	g.GET("/auth/methods", a.authMethods)
	g.GET("/auth/challenge", a.loginChallenge)
	g.POST("/auth/login", a.login)
	g.POST("/auth/logout", a.logout)
	g.GET("/auth/sso/login", a.ssoLogin)       // 302, no envelope
	g.GET("/auth/sso/callback", a.ssoCallback) // 302, no envelope
	g.GET("/setup/state", a.setupState)
	g.POST("/setup/token", a.setupToken)
	g.POST("/setup/admin", a.setupAdmin)

	// Machine callers. A build pipeline presents a token, never a session,
	// and this is the only endpoint it can reach. It is registered outside
	// a.handle on purpose: the route list behind /rbac/permissions describes
	// what people's permissions open, and a token is not a person.
	g.Group("", a.ciToken).POST("/ci/releases", a.ciRelease)

	in := g.Group("", a.requireUser)
	h := func(method, path string, gd guard, fn gin.HandlerFunc) { a.handle(in, method, path, gd, fn) }

	// Me.
	h("GET", "/me", signedIn, a.me)
	h("PUT", "/me/profile", signedIn, a.updateProfile)
	h("PUT", "/me/password", signedIn, a.changeOwnPassword)
	h("GET", "/me/sessions", signedIn, a.mySessions)
	h("DELETE", "/me/sessions/:session", signedIn, a.deleteMySession)
	h("GET", "/me/bindings", signedIn, a.myBindings)
	h("GET", "/me/activity", signedIn, a.myActivity)

	// Services and releases.
	h("GET", "/overview", needScoped(rbac.ServicesView), a.overview)
	h("GET", "/services", needScoped(rbac.ServicesView), a.listServices)
	h("GET", "/services/:service", needScoped(rbac.ServicesView), a.getService)
	h("GET", "/services/:service/envs/:env", needScoped(rbac.ServicesView), a.getEnv)
	h("GET", "/services/:service/envs/:env/candidates", needScoped(rbac.ServicesView), a.listCandidates)
	h("GET", "/services/:service/envs/:env/config-diff", needEnv(rbac.ReleasesSync, rbac.ReleasesCreate), a.configDiff)
	h("GET", "/services/:service/envs/:env/pods/:pod/logs", needEnv(rbac.PodsView), a.podLogs)
	h("GET", "/services/:service/envs/:env/pods/:pod/events", needEnv(rbac.PodsView), a.podEvents)
	h("GET", "/releases", needScoped(rbac.ReleasesView), a.listReleases)
	h("GET", "/releases/:id", needScoped(rbac.ReleasesView), a.getRelease)
	h("POST", "/releases", needEnv(rbac.ReleasesCreate, rbac.ReleasesRestart, rbac.ReleasesSync), a.createRelease)
	h("POST", "/releases/:id/confirm", needEnv(rbac.ReleasesCreate, rbac.ReleasesRestart, rbac.ReleasesSync), a.confirmRelease)
	// Who may approve is decided by the release's approval rule, not a permission.
	h("POST", "/releases/:id/approve", needScoped(rbac.ReleasesView), a.approveRelease)
	h("POST", "/releases/:id/reject", needScoped(rbac.ReleasesView), a.rejectRelease)
	h("POST", "/releases/:id/cancel", needEnv(rbac.ReleasesCreate, rbac.ReleasesRestart, rbac.ReleasesSync, rbac.ReleasesCancelAny), a.cancelRelease)
	h("GET", "/audit", needScoped(rbac.AuditView), a.listAudit)
	// Insights reads the same records the audit page reads, under the
	// same scope, so it needs no permission of its own.
	h("GET", "/insights", needScoped(rbac.AuditView), a.getInsights)

	// Users and groups. Role administrators may read them to pick subjects.
	h("GET", "/users", need(rbac.UsersManage, rbac.RolesManage), a.listUsers)
	h("POST", "/users", need(rbac.UsersManage), a.createUser)
	h("GET", "/users/:user", need(rbac.UsersManage, rbac.RolesManage), a.getUser)
	h("PUT", "/users/:user", need(rbac.UsersManage), a.renameUser)
	h("PUT", "/users/:user/password", need(rbac.UsersManage), a.resetPassword)
	h("PUT", "/users/:user/disabled", need(rbac.UsersManage), a.setUserDisabled)
	h("DELETE", "/users/:user/sessions", need(rbac.UsersManage), a.revokeUserSessions)
	h("GET", "/groups", need(rbac.UsersManage, rbac.RolesManage), a.listGroups)
	h("POST", "/groups", need(rbac.UsersManage), a.createGroup)
	h("PUT", "/groups/:group", need(rbac.UsersManage), a.updateGroup)
	h("DELETE", "/groups/:group", need(rbac.UsersManage), a.deleteGroup)
	h("GET", "/groups/:group/members", need(rbac.UsersManage, rbac.RolesManage), a.groupMembers)
	h("POST", "/groups/:group/members", need(rbac.UsersManage), a.addGroupMembers)
	h("DELETE", "/groups/:group/members/:user", need(rbac.UsersManage), a.removeGroupMember)

	// Roles and bindings.
	h("GET", "/rbac/permissions", need(rbac.RolesManage), a.permissionCatalog)
	h("GET", "/roles", need(rbac.RolesManage), a.listRoles)
	h("POST", "/roles", need(rbac.RolesManage), a.createRole)
	h("PUT", "/roles/:role", need(rbac.RolesManage), a.updateRole)
	h("DELETE", "/roles/:role", need(rbac.RolesManage), a.deleteRole)
	h("GET", "/role-bindings", need(rbac.RolesManage), a.listBindings)
	h("POST", "/role-bindings", need(rbac.RolesManage), a.createBinding)
	h("PUT", "/role-bindings/:binding", need(rbac.RolesManage), a.updateBinding)
	h("DELETE", "/role-bindings/:binding", need(rbac.RolesManage), a.deleteBinding)

	// Settings: the permission depends on the section.
	allSections := needSection(rbac.EnvironmentsManage, rbac.NotificationsManage, rbac.SettingsManage)
	h("GET", "/settings", allSections, a.getSettings)
	h("PUT", "/settings/:section", allSections, a.putSettings)
	h("POST", "/settings/upstreams/test", need(rbac.EnvironmentsManage), a.testUpstreams)
	h("GET", "/settings/catalog/labels", need(rbac.EnvironmentsManage), a.catalogLabels)
	h("POST", "/settings/notify/test", need(rbac.NotificationsManage), a.testNotify)

	// CI tokens are credentials that can start a release, so issuing and
	// revoking them is a settings-administrator job.
	h("GET", "/ci/tokens", need(rbac.SettingsManage), a.listCITokens)
	h("POST", "/ci/tokens", need(rbac.SettingsManage), a.createCIToken)
	h("DELETE", "/ci/tokens/:token", need(rbac.SettingsManage), a.revokeCIToken)
	h("GET", "/ci/intakes", needScoped(rbac.ReleasesView), a.listCIIntakes)
	h("GET", "/ci/snippet", need(rbac.SettingsManage), a.ciSnippet)

	// Generating a Kargo pipeline reads the catalog and writes nothing, but it
	// is an environment-wiring job and belongs with upstreams and the catalog.
	h("GET", "/kargo/generate", need(rbac.EnvironmentsManage), a.generateKargo)
	h("GET", "/kargo/generate.zip", need(rbac.EnvironmentsManage), a.downloadKargo)
	h("POST", "/kargo/push", need(rbac.EnvironmentsManage), a.pushKargo)
	h("GET", "/kargo/identity", need(rbac.EnvironmentsManage), a.kargoRepoIdentity)
}

// auditMeta puts request id and client IP into the request context so every
// audit record written during the request carries them.
func (a *API) auditMeta(c *gin.Context) {
	c.Request = c.Request.WithContext(audit.WithMeta(c.Request.Context(), audit.Meta{
		RequestID: c.GetString("request_id"), ClientIP: c.ClientIP(), UserAgent: c.Request.UserAgent(),
	}))
	c.Next()
}

// generations resolves the cache counters once per request. Every cache
// consulted while serving it then agrees about which world it is in, and a
// change made on another replica is picked up on the next request rather
// than whenever some TTL happens to lapse. One small query; the table has a
// row per scope and never grows.
func (a *API) generations(c *gin.Context) {
	gens, err := a.PG.Cache.Generations(c.Request.Context())
	if err != nil {
		// Without them every cache re-reads from the database, which is slow
		// but correct; refusing the request would be worse.
		zap.L().Warn("read cache generations failed", zap.String("request_id", c.GetString("request_id")), zap.Error(err))
		c.Next()
		return
	}
	c.Request = c.Request.WithContext(pg.WithGenerations(c.Request.Context(), gens))
	c.Next()
}

// setupGate: before initialization only setup and sign-in discovery work;
// afterwards the setup endpoints are gone for good.
func (a *API) setupGate(c *gin.Context) {
	if !a.Setup.Initialized() {
		_ = a.Setup.Refresh(c.Request.Context()) // another replica may have finished setup
	}
	p := strings.TrimPrefix(c.Request.URL.Path, "/api/v1")
	isSetup := strings.HasPrefix(p, "/setup/")
	switch {
	case a.Setup.Initialized() && isSetup:
		respond.Fail(c, errcode.NewKey(errcode.NotFound, "g.setupClosed"))
	case !a.Setup.Initialized() && !isSetup && p != "/auth/methods":
		respond.FailCode(c, errcode.SetupRequired, "")
	default:
		c.Next()
	}
}

func (a *API) loadUser(c *gin.Context) {
	if token, err := cookie(c, sessionCookie); err == nil {
		if u := a.Auth.UserFromToken(c.Request.Context(), token); u != nil {
			c.Set(ctxUser, u)
			c.Set("user_id", u.Sub)
		}
	}
	c.Next()
}

func (a *API) requireUser(c *gin.Context) {
	if currentUser(c) == nil {
		respond.FailCode(c, errcode.Unauthorized, "")
		return
	}
	c.Next()
}

func currentUser(c *gin.Context) *auth.User {
	u, _ := c.Get(ctxUser)
	user, _ := u.(*auth.User)
	return user
}

// grants evaluates the current user's permissions once per request.
func (a *API) grants(c *gin.Context) (*rbac.Grants, error) {
	if g, ok := c.Get(ctxGrants); ok {
		return g.(*rbac.Grants), nil
	}
	g, err := a.Access.Grants(c.Request.Context(), &currentUser(c).User)
	if err != nil {
		return nil, err
	}
	c.Set(ctxGrants, g)
	return g, nil
}

func (a *API) requireAny(ps ...rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := a.check(c, ps...); err != nil {
			respond.Fail(c, err)
			return
		}
		c.Next()
	}
}

// check requires any of ps globally; refusals are audited.
func (a *API) check(c *gin.Context, ps ...rbac.Permission) error {
	g, err := a.grants(c)
	if err != nil {
		return err
	}
	if g.HasAny(ps...) {
		return nil
	}
	need := make([]string, len(ps))
	for i, p := range ps {
		need[i] = string(p)
	}
	a.deny(c, strings.Join(need, "|"), "")
	return errcode.New(errcode.Forbidden, "")
}

// requireAnyScope passes when the viewer holds one of ps in any scope. What
// they see inside is filtered by the handler.
func (a *API) requireAnyScope(ps ...rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, err := a.grants(c)
		if err != nil {
			respond.Fail(c, err)
			c.Abort()
			return
		}
		if slices.ContainsFunc(ps, g.HasAnyTarget) {
			c.Next()
			return
		}
		need := make([]string, len(ps))
		for i, p := range ps {
			need[i] = string(p)
		}
		a.deny(c, strings.Join(need, "|"), "")
		respond.FailCode(c, errcode.Forbidden, "")
		c.Abort()
	}
}

// targetOf resolves a service in env to what scoped grants look at: its
// project and its value of the catalog's batch dimension. A service missing
// from the catalog has neither, so only unscoped grants apply to it.
// targetStaleness bounds how old a snapshot may be when all that is wanted
// from it is a service's project and type. Those follow the Application's
// labels, so they change when somebody edits git, not from minute to minute;
// waiting for a fresh catalogue to learn them puts a cross-site fan-out in
// front of a button press.
const targetStaleness = 5 * time.Minute

func (a *API) targetOf(ctx context.Context, service, env string) rbac.Target {
	t := rbac.Target{Env: env}
	snap := a.Hub.Recent(targetStaleness)
	if snap == nil {
		var err error
		if snap, err = a.Hub.Snapshot(ctx, false); err != nil {
			return t
		}
	}
	svc := snap.Find(service)
	if svc == nil {
		return t
	}
	t.Project = svc.Project
	if cat, err := a.Settings.Catalog(ctx); err == nil && cat.BatchDimension != "" {
		t.Type = svc.Dimensions[cat.BatchDimension]
	}
	return t
}

// requireTarget checks an environment permission for one service, honouring
// project / type scopes. Refusals are audited: they mean misconfigured permissions or someone
// probing the boundary.
func (a *API) requireTarget(c *gin.Context, p rbac.Permission, service, env, target string) error {
	g, err := a.grants(c)
	if err != nil {
		return err
	}
	t := a.targetOf(c.Request.Context(), service, env)
	if g.HasTarget(p, t) {
		return nil
	}
	a.deny(c, string(p)+"@"+env+"/"+t.Project+"/"+t.Type, target)
	def, _ := rbac.Lookup(p)
	loc := i18n.From(c.Request.Context())
	scope := i18n.T(loc, "g.scopeEnv", env)
	if t.Project != "" {
		scope += i18n.T(loc, "g.scopeProject", t.Project)
	}
	if t.Type != "" {
		scope += " (" + a.typeLabel(c.Request.Context(), t.Type) + ")"
	}
	return errcode.NewKey(errcode.Forbidden, "g.noPermission", scope, def.Name, service)
}

// typeLabel renders a batch-dimension value the way the catalog names it.
func (a *API) typeLabel(ctx context.Context, value string) string {
	cat, err := a.Settings.Catalog(ctx)
	if err != nil {
		return value
	}
	for _, d := range cat.Dimensions {
		if d.Key != cat.BatchDimension {
			continue
		}
		for _, v := range d.Values {
			if v.Value == value && v.Name != "" {
				return v.Name
			}
		}
	}
	return value
}

func (a *API) deny(c *gin.Context, need, target string) {
	actor := audit.Actor{Sub: "anonymous", Name: "anonymous"}
	var groups []string
	if u := currentUser(c); u != nil {
		actor, groups = u.Actor(), slices.Concat(u.Groups, u.LocalGroups)
	}
	_ = a.PG.Audit.Write(c.Request.Context(), actor, "permission.denied", target, "", map[string]any{
		"need": need, "method": c.Request.Method, "path": c.Request.URL.Path, "groups": groups,
	})
}

// cookie reads a raw cookie value. gin's c.Cookie URL-unescapes the value,
// which turns "+" in base64 ciphertext into a space and breaks decryption.
func cookie(c *gin.Context, name string) (string, error) {
	ck, err := c.Request.Cookie(name)
	if err != nil {
		return "", err
	}
	return ck.Value, nil
}

func secure(c *gin.Context) bool {
	return c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
}

func setCookie(c *gin.Context, name, value, path string, maxAge int, sameSite http.SameSite) {
	// HttpOnly always; Secure whenever the request came over HTTPS; SameSite set by caller.
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: path, MaxAge: maxAge, //nolint:gosec // see comment above
		HttpOnly: true, Secure: secure(c), SameSite: sameSite})
}

func clearCookie(c *gin.Context, name, path string) {
	setCookie(c, name, "", path, -1, http.SameSiteLaxMode)
}

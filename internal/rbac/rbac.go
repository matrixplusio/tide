// Package rbac is Tide's authorization model: a fixed catalog of permissions,
// roles that group them, and bindings that grant a role to a subject within an
// environment scope. It is pure (no I/O) so it can be evaluated per request
// from a cached snapshot and tested exhaustively.
package rbac

import (
	"tide/internal/i18n"

	"slices"
	"strings"
	"time"
)

type Permission string

const (
	ServicesView        Permission = "services.view"
	ReleasesView        Permission = "releases.view"
	AuditView           Permission = "audit.view"
	UsersManage         Permission = "users.manage"
	RolesManage         Permission = "roles.manage"
	EnvironmentsManage  Permission = "environments.manage"
	NotificationsManage Permission = "notifications.manage"
	SettingsManage      Permission = "settings.manage"

	PodsView          Permission = "pods.view"
	ReleasesCreate    Permission = "releases.create"
	ReleasesRestart   Permission = "releases.restart"
	ReleasesSync      Permission = "releases.sync"
	ReleasesCancelAny Permission = "releases.cancel_any"
)

type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeEnv    Scope = "env"
)

// Def describes a permission. Name and Description are catalog keys: the
// permission list is shown to whoever is editing a role, so it is rendered in
// their language by the handler that serves it.
type Def struct {
	Key         Permission `json:"key"`
	Name        i18n.Key   `json:"-"`
	Description i18n.Key   `json:"-"`
	Scope       Scope      `json:"scope"`
}

// Catalog is every permission, in display order.
var Catalog = []Def{
	{ServicesView, "perm.servicesView.name", "perm.servicesView.desc", ScopeEnv},
	{ReleasesView, "perm.releasesView.name", "perm.releasesView.desc", ScopeEnv},
	{AuditView, "perm.auditView.name", "perm.auditView.desc", ScopeEnv},
	{UsersManage, "perm.usersManage.name", "perm.usersManage.desc", ScopeGlobal},
	{RolesManage, "perm.rolesManage.name", "perm.rolesManage.desc", ScopeGlobal},
	{EnvironmentsManage, "perm.environmentsManage.name", "perm.environmentsManage.desc", ScopeGlobal},
	{NotificationsManage, "perm.notificationsManage.name", "perm.notificationsManage.desc", ScopeGlobal},
	{SettingsManage, "perm.settingsManage.name", "perm.settingsManage.desc", ScopeGlobal},
	{PodsView, "perm.podsView.name", "perm.podsView.desc", ScopeEnv},
	{ReleasesCreate, "perm.releasesCreate.name", "perm.releasesCreate.desc", ScopeEnv},
	{ReleasesRestart, "perm.releasesRestart.name", "perm.releasesRestart.desc", ScopeEnv},
	{ReleasesSync, "perm.releasesSync.name", "perm.releasesSync.desc", ScopeEnv},
	{ReleasesCancelAny, "perm.releasesCancelAny.name", "perm.releasesCancelAny.desc", ScopeEnv},
}

func Lookup(p Permission) (Def, bool) {
	for _, d := range Catalog {
		if d.Key == p {
			return d, true
		}
	}
	return Def{}, false
}

// ManagePermissions are the ones that open the admin console.
var ManagePermissions = []Permission{UsersManage, RolesManage, EnvironmentsManage, NotificationsManage, SettingsManage}

const (
	TierDevelopment = "development"
	TierTesting     = "testing"
	TierStaging     = "staging"
	TierProduction  = "production"
)

type TierDef struct {
	Key  string   `json:"key"`
	Name i18n.Key `json:"-"`
}

var Tiers = []TierDef{{TierDevelopment, "tier.tierDevelopment"}, {TierTesting, "tier.tierTesting"}, {TierStaging, "tier.tierStaging"}, {TierProduction, "tier.tierProduction"}}

func ValidTier(t string) bool {
	return slices.ContainsFunc(Tiers, func(d TierDef) bool { return d.Key == t })
}

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	// AllPermissions in a role's permission list grants everything, including
	// permissions added in later versions.
	AllPermissions = "*"
	// SubjectAll matches every signed-in user; EnvAll matches every environment.
	SubjectAll = "*"
	EnvAll     = "*"
)

type Role struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Builtin      bool      `json:"builtin"`
	Permissions  []string  `json:"permissions"`
	BindingCount int       `json:"bindingCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func (r Role) grants(p Permission) bool {
	return slices.Contains(r.Permissions, AllPermissions) || slices.Contains(r.Permissions, string(p))
}

type Binding struct {
	ID          int64    `json:"id"`
	RoleID      string   `json:"roleId"`
	RoleName    string   `json:"roleName"`
	Subject     string   `json:"subject"`
	SubjectName string   `json:"subjectName"`
	Envs        []string `json:"envs"`
	// Projects and Types narrow environment permissions to services of these
	// projects / types (the catalog's batch dimension, e.g. backend). Empty or
	// "*" means all. Global permissions ignore them.
	Projects  []string  `json:"projects"`
	Types     []string  `json:"types"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
}

// Via says how a binding reached a user.
type Via string

const (
	ViaUser  Via = "user"
	ViaGroup Via = "group"
	ViaAll   Via = "all"
)

type EffectiveBinding struct {
	Binding
	Via Via `json:"via"`
}

// Env is what evaluation needs to know about an environment.
type Env struct {
	Name string
	Tier string
}

// Policy is a snapshot of roles and bindings.
type Policy struct {
	Roles    map[string]Role
	Bindings []Binding
}

// Subject identifies the person being authorized.
type Subject struct {
	Sub    string
	Groups []string // IdP groups ∪ local group memberships
}

// Match returns the bindings that apply to s.
func (p *Policy) Match(s Subject) []EffectiveBinding {
	var out []EffectiveBinding
	for _, b := range p.Bindings {
		switch {
		case b.Subject == SubjectAll:
			out = append(out, EffectiveBinding{b, ViaAll})
		case strings.HasPrefix(b.Subject, "user:") && b.Subject[len("user:"):] == s.Sub:
			out = append(out, EffectiveBinding{b, ViaUser})
		case strings.HasPrefix(b.Subject, "group:") && slices.Contains(s.Groups, b.Subject[len("group:"):]):
			out = append(out, EffectiveBinding{b, ViaGroup})
		}
	}
	return out
}

// Grants is what one user may do. Build it with Policy.Grants.
type Grants struct {
	global   map[Permission]bool
	bindings []EffectiveBinding
	roles    map[string]Role
	tiers    map[string]string
}

func (p *Policy) Grants(s Subject, envs []Env) *Grants {
	g := &Grants{global: map[Permission]bool{}, bindings: p.Match(s), roles: p.Roles, tiers: map[string]string{}}
	for _, e := range envs {
		g.tiers[e.Name] = e.Tier
	}
	for _, b := range g.bindings {
		role, ok := p.Roles[b.RoleID]
		if !ok {
			continue
		}
		for _, d := range Catalog {
			if d.Scope == ScopeGlobal && role.grants(d.Key) {
				g.global[d.Key] = true
			}
		}
	}
	return g
}

// Has reports a global permission.
func (g *Grants) Has(p Permission) bool { return g.global[p] }

// HasAny reports whether any of ps is held globally.
func (g *Grants) HasAny(ps ...Permission) bool {
	return slices.ContainsFunc(ps, g.Has)
}

// HasEnv reports an environment-scoped permission for env. An environment
// that is not configured has no tier, so only "*" or its exact name match.
func (g *Grants) HasEnv(p Permission, env string) bool {
	for _, b := range g.bindings {
		role, ok := g.roles[b.RoleID]
		if ok && role.grants(p) && EnvMatches(b.Envs, env, g.tiers[env]) {
			return true
		}
	}
	return false
}

// Target is a service in an environment, the unit scoped permissions apply to.
// Project and Type are empty when the catalog does not know them; then only
// bindings not narrowed by project / type match.
type Target struct {
	Env     string
	Project string
	Type    string
}

// HasAnyTarget reports whether the permission is held anywhere, in any
// scope: what a navigation entry or a list endpoint needs before filtering.
func (g *Grants) HasAnyTarget(p Permission) bool {
	for _, b := range g.bindings {
		if role, ok := g.roles[b.RoleID]; ok && role.grants(p) {
			return true
		}
	}
	return false
}

// Unrestricted reports a grant of p limited by nothing: every environment,
// every project, every type. Records that belong to no service (logins,
// settings changes) are only visible to such a grant.
func (g *Grants) Unrestricted(p Permission) bool {
	for _, b := range g.bindings {
		role, ok := g.roles[b.RoleID]
		if ok && role.grants(p) && slices.Contains(b.Envs, EnvAll) && ScopeMatches(b.Projects, "") && ScopeMatches(b.Types, "") {
			return true
		}
	}
	return false
}

// HasTarget reports an environment-scoped permission for one service.
func (g *Grants) HasTarget(p Permission, t Target) bool {
	for _, b := range g.bindings {
		role, ok := g.roles[b.RoleID]
		if ok && role.grants(p) && EnvMatches(b.Envs, t.Env, g.tiers[t.Env]) && ScopeMatches(b.Projects, t.Project) && ScopeMatches(b.Types, t.Type) {
			return true
		}
	}
	return false
}

// ScopeMatches evaluates a project / type list: empty or "*" matches all,
// otherwise the value must be listed (an unknown value matches only "all").
func ScopeMatches(list []string, value string) bool {
	if len(list) == 0 || slices.Contains(list, EnvAll) {
		return true
	}
	return value != "" && slices.Contains(list, value)
}

// Scoped is one environment grant with its scope, for clients that decide
// per service (the same evaluation as HasTarget).
type Scoped struct {
	Permissions []Permission `json:"permissions"`
	Envs        []string     `json:"envs"`
	Projects    []string     `json:"projects"`
	Types       []string     `json:"types"`
}

// ScopedGrants lists, per binding, the environment permissions it gives,
// with environments resolved to names (tier selectors expanded).
func (g *Grants) ScopedGrants(envs []Env) []Scoped {
	out := []Scoped{}
	for _, b := range g.bindings {
		role, ok := g.roles[b.RoleID]
		if !ok {
			continue
		}
		s := Scoped{Permissions: []Permission{}, Envs: []string{}, Projects: orEmpty(b.Projects), Types: orEmpty(b.Types)}
		for _, d := range Catalog {
			if d.Scope == ScopeEnv && role.grants(d.Key) {
				s.Permissions = append(s.Permissions, d.Key)
			}
		}
		for _, e := range envs {
			if EnvMatches(b.Envs, e.Name, e.Tier) {
				s.Envs = append(s.Envs, e.Name)
			}
		}
		if len(s.Permissions) > 0 && len(s.Envs) > 0 {
			out = append(out, s)
		}
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Global lists global permissions held, in catalog order.
func (g *Grants) Global() []Permission {
	out := []Permission{}
	for _, d := range Catalog {
		if g.global[d.Key] {
			out = append(out, d.Key)
		}
	}
	return out
}

// ForEnv lists environment permissions held in env, in catalog order.
func (g *Grants) ForEnv(env string) []Permission {
	out := []Permission{}
	for _, d := range Catalog {
		if d.Scope == ScopeEnv && g.HasEnv(d.Key, env) {
			out = append(out, d.Key)
		}
	}
	return out
}

func (g *Grants) Bindings() []EffectiveBinding { return g.bindings }

// EnvMatches evaluates an env selector list.
func EnvMatches(selectors []string, env, tier string) bool {
	for _, s := range selectors {
		switch {
		case s == EnvAll:
			return true
		case strings.HasPrefix(s, "tier:"):
			if tier != "" && s[len("tier:"):] == tier {
				return true
			}
		case s == env:
			return true
		}
	}
	return false
}

// Package access manages authorization data — local groups, roles and role
// bindings — and evaluates what a user may do. Evaluation uses a policy
// snapshot cached in process. A write bumps a generation counter inside its
// own transaction, so every replica notices on its next read; policyTTL is
// only a floor on how often an idle replica re-reads the counter.
package access

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"tide/internal/audit"
	"tide/internal/rbac"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

const policyTTL = 5 * time.Second

var (
	ErrRoleNotFound    = errors.New("role not found")
	ErrRoleBuiltin     = errors.New("builtin roles cannot be changed")
	ErrRoleExists      = errors.New("role id already exists")
	ErrBindingExists   = errors.New("binding already exists")
	ErrBindingNotFound = errors.New("binding not found")
	ErrGroupNotFound   = errors.New("group not found")
	ErrGroupExists     = errors.New("group already exists")
	ErrUserNotFound    = errors.New("user not found")
	ErrSelf            = errors.New("cannot perform this action on yourself")
	ErrLastAdmin       = errors.New("at least one usable administrator must remain")
)

type Service struct {
	PG       *pg.Store
	Settings *settings.Store

	mu       sync.Mutex
	policy   *rbac.Policy
	loadedAt time.Time
	gen      int64
}

func (s *Service) Invalidate() {
	s.mu.Lock()
	s.policy, s.gen = nil, 0
	s.mu.Unlock()
}

func (s *Service) Policy(ctx context.Context) (*rbac.Policy, error) {
	gen, err := s.PG.Cache.Generation(ctx, pg.ScopeAccess)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policy != nil && s.gen == gen && time.Since(s.loadedAt) < policyTTL {
		return s.policy, nil
	}
	p, err := s.PG.Access.Policy(ctx)
	if err != nil {
		return nil, err
	}
	s.policy, s.loadedAt, s.gen = p, time.Now(), gen
	return p, nil
}

// Subject builds the authorization subject for a user: IdP groups and local
// group memberships are one namespace.
func Subject(u *pg.User) rbac.Subject {
	groups := slices.Concat(u.Groups, u.LocalGroups)
	return rbac.Subject{Sub: u.Sub, Groups: groups}
}

// Grants evaluates u against the current policy and environments.
func (s *Service) Grants(ctx context.Context, u *pg.User) (*rbac.Grants, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return nil, err
	}
	envs, err := s.Settings.Environments(ctx)
	if err != nil {
		return nil, err
	}
	list := make([]rbac.Env, len(envs.Items))
	for i, e := range envs.Items {
		list[i] = rbac.Env{Name: e.Name, Tier: e.Tier}
	}
	return p.Grants(Subject(u), list), nil
}

// IsAdmin reports whether any binding grants u the admin role.
func (s *Service) IsAdmin(ctx context.Context, u *pg.User) (bool, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return false, err
	}
	for _, b := range p.Match(Subject(u)) {
		if b.RoleID == rbac.RoleAdmin {
			return true, nil
		}
	}
	return false, nil
}

// EnsureAdminLeft fails inside tx when no usable administrator would remain.
func EnsureAdminLeft(ctx context.Context, tx *pg.Store) error {
	n, err := tx.Access.CountUsableAdmins(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// ---- groups ----

// GroupNameRe is the one rule for group names; the HTTP layer checks path
// parameters against it too.
var GroupNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidGroupName(name string) bool { return GroupNameRe.MatchString(name) }

// write runs fn in a transaction and tells every replica that authorization
// data changed. The counter is bumped inside the same transaction, so a
// rolled-back change never invalidates anyone's cache and a committed one
// always does.
func (s *Service) write(ctx context.Context, fn func(tx *pg.Store) error) error {
	err := s.PG.Tx(ctx, func(tx *pg.Store) error {
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Cache.Bump(ctx, pg.ScopeAccess)
	})
	s.Invalidate()
	return err
}

func (s *Service) CreateGroup(ctx context.Context, actor audit.Actor, name, description string) error {
	if fe := validate.Collect(groupName("name", name), validate.MaxLen("description", description, 200, "label.description")); fe != nil {
		return fe
	}
	return s.write(ctx, func(tx *pg.Store) error {
		ok, err := tx.Access.CreateGroup(ctx, name, description)
		if err != nil {
			return err
		}
		if !ok {
			return ErrGroupExists
		}
		return tx.Audit.Write(ctx, actor, "group.create", "group:"+name, "", map[string]any{"description": description})
	})
}

func groupName(field, name string) *validate.FieldError {
	switch {
	case name == "":
		return validate.FieldKey(field, "a.groupNameRequired")
	case !ValidGroupName(name):
		return validate.FieldKey(field, "a.groupNameFormat")
	}
	return nil
}

func (s *Service) UpdateGroup(ctx context.Context, actor audit.Actor, name, description string) error {
	if fe := validate.Collect(validate.MaxLen("description", description, 200, "label.description")); fe != nil {
		return fe
	}
	return s.write(ctx, func(tx *pg.Store) error {
		ok, err := tx.Access.UpdateGroup(ctx, name, description)
		if err != nil {
			return err
		}
		if !ok {
			return ErrGroupNotFound
		}
		return tx.Audit.Write(ctx, actor, "group.update", "group:"+name, "", map[string]any{"description": description})
	})
}

// DeleteGroup removes the group and its memberships. Bindings to the group
// name stay: an IdP group of the same name may still rely on them. Group
// admins never count as the last usable administrator, so no check is needed.
func (s *Service) DeleteGroup(ctx context.Context, actor audit.Actor, name string) error {
	return s.write(ctx, func(tx *pg.Store) error {
		ok, err := tx.Access.DeleteGroup(ctx, name)
		if err != nil {
			return err
		}
		if !ok {
			return ErrGroupNotFound
		}
		return tx.Audit.Write(ctx, actor, "group.delete", "group:"+name, "", nil)
	})
}

func (s *Service) AddMembers(ctx context.Context, actor audit.Actor, group string, userIDs []int64) error {
	return s.write(ctx, func(tx *pg.Store) error {
		ok, err := tx.Access.GroupExists(ctx, group)
		if err != nil {
			return err
		}
		if !ok {
			return ErrGroupNotFound
		}
		found, err := tx.Access.AddMembers(ctx, group, userIDs)
		if err != nil {
			return err
		}
		if found != int64(len(userIDs)) {
			return validate.Errors{{Field: "userIds", Key: "a.someUsersMissing"}}
		}
		return tx.Audit.Write(ctx, actor, "group.members.add", "group:"+group, "", map[string]any{"userIds": userIDs})
	})
}

func (s *Service) RemoveMember(ctx context.Context, actor audit.Actor, group string, userID int64) error {
	return s.write(ctx, func(tx *pg.Store) error {
		ok, err := tx.Access.RemoveMember(ctx, group, userID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrUserNotFound
		}
		return tx.Audit.Write(ctx, actor, "group.members.remove", "group:"+group, "", map[string]any{"userId": userID})
	})
}

// ---- roles ----

// RoleIDRe is the one rule for role ids, shared with the HTTP layer.
var RoleIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

type RoleInput struct {
	ID          string
	Name        string
	Description string
	Permissions []string
}

func (in RoleInput) check(withID bool) error {
	var errs []*validate.FieldError
	if withID {
		switch {
		case in.ID == "":
			errs = append(errs, validate.FieldKey("id", "a.roleIdRequired"))
		case !RoleIDRe.MatchString(in.ID):
			errs = append(errs, validate.FieldKey("id", "a.roleIdFormat"))
		}
	}
	errs = append(errs,
		validate.Required("name", in.Name, "label.roleName"),
		validate.MaxLen("name", in.Name, 32, "label.roleName"),
		validate.MaxLen("description", in.Description, 200, "label.description"))
	if len(in.Permissions) == 0 {
		errs = append(errs, validate.FieldKey("permissions", "a.permissionsRequired"))
	}
	seen := map[string]bool{}
	for _, p := range in.Permissions {
		if _, ok := rbac.Lookup(rbac.Permission(p)); !ok {
			errs = append(errs, validate.FieldKey("permissions", "a.permissionUnknown", p))
		} else if seen[p] {
			errs = append(errs, validate.FieldKey("permissions", "a.permissionDup", p))
		}
		seen[p] = true
	}
	return validate.Collect(errs...)
}

func (s *Service) CreateRole(ctx context.Context, actor audit.Actor, in RoleInput) (*rbac.Role, error) {
	if err := in.check(true); err != nil {
		return nil, err
	}
	err := s.write(ctx, func(tx *pg.Store) error {
		ok, err := tx.Access.CreateRole(ctx, rbac.Role{ID: in.ID, Name: in.Name, Description: in.Description, Permissions: in.Permissions})
		if err != nil {
			return err
		}
		if !ok {
			return ErrRoleExists
		}
		return tx.Audit.Write(ctx, actor, "role.create", "role:"+in.ID, "", map[string]any{"name": in.Name, "permissions": in.Permissions})
	})
	if err != nil {
		return nil, err
	}
	return s.PG.Access.GetRole(ctx, in.ID)
}

func (s *Service) UpdateRole(ctx context.Context, actor audit.Actor, in RoleInput) (*rbac.Role, error) {
	if err := in.check(false); err != nil {
		return nil, err
	}
	err := s.write(ctx, func(tx *pg.Store) error {
		cur, err := tx.Access.GetRole(ctx, in.ID)
		if err != nil {
			return err
		}
		if cur == nil {
			return ErrRoleNotFound
		}
		if cur.Builtin {
			return ErrRoleBuiltin
		}
		if _, err := tx.Access.UpdateRole(ctx, rbac.Role{ID: in.ID, Name: in.Name, Description: in.Description, Permissions: in.Permissions}); err != nil {
			return err
		}
		return tx.Audit.Write(ctx, actor, "role.update", "role:"+in.ID, "", map[string]any{
			"before": map[string]any{"name": cur.Name, "permissions": cur.Permissions},
			"after":  map[string]any{"name": in.Name, "permissions": in.Permissions},
		})
	})
	if err != nil {
		return nil, err
	}
	return s.PG.Access.GetRole(ctx, in.ID)
}

func (s *Service) DeleteRole(ctx context.Context, actor audit.Actor, id string) error {
	return s.write(ctx, func(tx *pg.Store) error {
		cur, err := tx.Access.GetRole(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return ErrRoleNotFound
		}
		if cur.Builtin {
			return ErrRoleBuiltin
		}
		bindings, err := tx.Access.ListBindings(ctx, pg.BindingFilter{RoleID: id})
		if err != nil {
			return err
		}
		if _, err := tx.Access.DeleteRole(ctx, id); err != nil {
			return err
		}
		return tx.Audit.Write(ctx, actor, "role.delete", "role:"+id, "", map[string]any{"name": cur.Name, "bindings": bindings})
	})
}

// ---- bindings ----

var reGroupSubject = regexp.MustCompile(`^group:[^\s\x00-\x1f\x7f]{1,255}$`)

// CheckEnvSelectors validates an env selector list against configured
// environments. Unknown environment names are rejected so typos do not
// silently grant nothing.
func CheckEnvSelectors(field string, selectors []string, envs settings.Environments) []*validate.FieldError {
	var errs []*validate.FieldError
	if len(selectors) == 0 {
		return []*validate.FieldError{validate.FieldKey(field, "a.envScopeRequired")}
	}
	if slices.Contains(selectors, rbac.EnvAll) && len(selectors) > 1 {
		errs = append(errs, validate.FieldKey(field, "a.envScopeAllAlone"))
	}
	seen := map[string]bool{}
	for _, sel := range selectors {
		switch {
		case seen[sel]:
			errs = append(errs, validate.FieldKey(field, "a.envScopeDup", sel))
		case sel == rbac.EnvAll:
		case strings.HasPrefix(sel, "tier:"):
			if !rbac.ValidTier(strings.TrimPrefix(sel, "tier:")) {
				errs = append(errs, validate.FieldKey(field, "a.tierUnknown", strings.TrimPrefix(sel, "tier:")))
			}
		default:
			if _, ok := envs.Named(sel); !ok {
				errs = append(errs, validate.FieldKey(field, "a.envMissing", sel))
			}
		}
		seen[sel] = true
	}
	return errs
}

type BindingInput struct {
	RoleID   string
	Subject  string
	Envs     []string
	Projects []string
	Types    []string
}

// CheckScope validates a project / type list: "*" alone or names.
func CheckScope(field string, list []string) []*validate.FieldError {
	var errs []*validate.FieldError
	if len(list) > 50 {
		return []*validate.FieldError{validate.FieldKey(field, "a.max50")}
	}
	if slices.Contains(list, rbac.EnvAll) && len(list) > 1 {
		errs = append(errs, validate.FieldKey(field, "a.allAlone"))
	}
	seen := map[string]bool{}
	for i, v := range list {
		switch {
		case v == "" || len(v) > 64 || strings.ContainsAny(v, " \t\n"):
			errs = append(errs, validate.FieldKey(field+"."+strconv.Itoa(i), "a.entryFormat"))
		case seen[v]:
			errs = append(errs, validate.FieldKey(field+"."+strconv.Itoa(i), "a.entryDup", v))
		}
		seen[v] = true
	}
	return errs
}

func (s *Service) CreateBinding(ctx context.Context, actor audit.Actor, in BindingInput) (*rbac.Binding, error) {
	envs, err := s.Settings.Environments(ctx)
	if err != nil {
		return nil, err
	}
	errs := CheckEnvSelectors("envs", in.Envs, envs)
	errs = append(errs, CheckScope("projects", in.Projects)...)
	errs = append(errs, CheckScope("types", in.Types)...)
	if in.RoleID == rbac.RoleAdmin && (len(in.Projects) > 0 || len(in.Types) > 0) {
		errs = append(errs, validate.FieldKey("projects", "a.adminNoScope"))
	}
	switch {
	case in.Subject == rbac.SubjectAll:
		if in.RoleID == rbac.RoleAdmin {
			errs = append(errs, validate.FieldKey("subject", "a.adminNotEveryone"))
		}
	case strings.HasPrefix(in.Subject, "user:"):
		u, err := s.PG.Accounts.UserBySub(ctx, strings.TrimPrefix(in.Subject, "user:"))
		if err != nil {
			return nil, err
		}
		if u == nil {
			errs = append(errs, validate.FieldKey("subject", "a.userMissing"))
		}
	case reGroupSubject.MatchString(in.Subject):
	default:
		errs = append(errs, validate.FieldKey("subject", "a.subjectFormat"))
	}
	if err := validate.Collect(errs...); err != nil {
		return nil, err
	}
	var id int64
	err = s.write(ctx, func(tx *pg.Store) error {
		role, err := tx.Access.GetRole(ctx, in.RoleID)
		if err != nil {
			return err
		}
		if role == nil {
			return ErrRoleNotFound
		}
		if id, err = tx.Access.CreateBinding(ctx, in.RoleID, in.Subject, pg.BindingScope{Envs: in.Envs, Projects: in.Projects, Types: in.Types}, actor.Sub); err != nil {
			return err
		}
		if id == 0 {
			return ErrBindingExists
		}
		return tx.Audit.Write(ctx, actor, "role.bind", "role:"+in.RoleID, "", map[string]any{"subject": in.Subject, "envs": in.Envs, "projects": in.Projects, "types": in.Types})
	})
	if err != nil {
		return nil, err
	}
	return s.PG.Access.GetBinding(ctx, id)
}

func (s *Service) UpdateBinding(ctx context.Context, actor audit.Actor, id int64, scope pg.BindingScope) (*rbac.Binding, error) {
	envs, err := s.Settings.Environments(ctx)
	if err != nil {
		return nil, err
	}
	errs := CheckEnvSelectors("envs", scope.Envs, envs)
	errs = append(errs, CheckScope("projects", scope.Projects)...)
	errs = append(errs, CheckScope("types", scope.Types)...)
	if err := validate.Collect(errs...); err != nil {
		return nil, err
	}
	err = s.write(ctx, func(tx *pg.Store) error {
		cur, err := tx.Access.GetBinding(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return ErrBindingNotFound
		}
		if cur.RoleID == rbac.RoleAdmin && (len(scope.Projects) > 0 || len(scope.Types) > 0) {
			return validate.FieldKey("projects", "a.adminNoScope")
		}
		if _, err := tx.Access.UpdateBindingScope(ctx, id, scope); err != nil {
			return err
		}
		if cur.RoleID == rbac.RoleAdmin {
			if err := EnsureAdminLeft(ctx, tx); err != nil {
				return err
			}
		}
		return tx.Audit.Write(ctx, actor, "role.bind.update", "role:"+cur.RoleID, "", map[string]any{
			"subject": cur.Subject,
			"before":  map[string]any{"envs": cur.Envs, "projects": cur.Projects, "types": cur.Types},
			"after":   map[string]any{"envs": scope.Envs, "projects": scope.Projects, "types": scope.Types}})
	})
	if err != nil {
		return nil, err
	}
	return s.PG.Access.GetBinding(ctx, id)
}

func (s *Service) DeleteBinding(ctx context.Context, actor audit.Actor, id int64) error {
	return s.write(ctx, func(tx *pg.Store) error {
		cur, err := tx.Access.GetBinding(ctx, id)
		if err != nil {
			return err
		}
		if cur == nil {
			return ErrBindingNotFound
		}
		if cur.RoleID == rbac.RoleAdmin && cur.Subject == "user:"+actor.Sub {
			return ErrSelf
		}
		if _, err := tx.Access.DeleteBinding(ctx, id); err != nil {
			return err
		}
		if cur.RoleID == rbac.RoleAdmin {
			if err := EnsureAdminLeft(ctx, tx); err != nil {
				return err
			}
		}
		return tx.Audit.Write(ctx, actor, "role.unbind", "role:"+cur.RoleID, "", map[string]any{"subject": cur.Subject, "envs": cur.Envs})
	})
}

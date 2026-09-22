package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"tide/internal/rbac"
)

// Access covers local groups, roles and role bindings.
type Access struct {
	db *gorm.DB
}

type Group struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MemberCount int       `json:"memberCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (a *Access) ListGroups(ctx context.Context) ([]Group, error) {
	out := []Group{}
	err := a.db.WithContext(ctx).Raw(`SELECT g.name, g.description, g.created_at,
		(SELECT count(*) FROM group_members m JOIN users u ON u.id = m.user_id WHERE m.group_name = g.name) AS member_count
		FROM groups g ORDER BY g.name`).Scan(&out).Error
	return out, err
}

func (a *Access) GroupExists(ctx context.Context, name string) (bool, error) {
	var ok bool
	err := a.db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM groups WHERE name = $1)`, name).Scan(&ok).Error
	return ok, err
}

// CreateGroup returns false when the name is taken.
func (a *Access) CreateGroup(ctx context.Context, name, description string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`INSERT INTO groups (name, description) VALUES ($1, $2) ON CONFLICT DO NOTHING`, name, description)
	return res.RowsAffected == 1, res.Error
}

func (a *Access) UpdateGroup(ctx context.Context, name, description string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`UPDATE groups SET description = $2 WHERE name = $1`, name, description)
	return res.RowsAffected == 1, res.Error
}

func (a *Access) DeleteGroup(ctx context.Context, name string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`DELETE FROM groups WHERE name = $1`, name)
	return res.RowsAffected == 1, res.Error
}

// AddMembers adds existing users; it returns how many users exist among ids.
func (a *Access) AddMembers(ctx context.Context, group string, userIDs []int64) (int64, error) {
	var found int64
	if err := a.db.WithContext(ctx).Raw(`SELECT count(*) FROM users WHERE id = ANY($1)`, userIDs).Scan(&found).Error; err != nil {
		return 0, err
	}
	err := a.db.WithContext(ctx).Exec(`INSERT INTO group_members (group_name, user_id)
		SELECT $1, id FROM users WHERE id = ANY($2) ON CONFLICT DO NOTHING`, group, userIDs).Error
	return found, err
}

func (a *Access) RemoveMember(ctx context.Context, group string, userID int64) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`DELETE FROM group_members WHERE group_name = $1 AND user_id = $2`, group, userID)
	return res.RowsAffected == 1, res.Error
}

func (a *Access) GroupMembers(ctx context.Context, accounts *Accounts, group string) ([]User, error) {
	return accounts.queryUsers(ctx, `SELECT `+userColumns+` FROM users u
		JOIN group_members m ON m.user_id = u.id WHERE m.group_name = $1 ORDER BY u.name, u.id`, group)
}

func scanRole(permissions []byte, r *rbac.Role) error {
	r.Permissions = []string{}
	return json.Unmarshal(permissions, &r.Permissions)
}

func (a *Access) ListRoles(ctx context.Context) ([]rbac.Role, error) {
	return a.roles(ctx, ``)
}

func (a *Access) GetRole(ctx context.Context, id string) (*rbac.Role, error) {
	rs, err := a.roles(ctx, `WHERE r.id = $1`, id)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	return &rs[0], nil
}

func (a *Access) roles(ctx context.Context, where string, args ...any) ([]rbac.Role, error) {
	rows, err := a.db.WithContext(ctx).Raw(`SELECT r.id, r.name, r.description, r.builtin, r.permissions, r.created_at, r.updated_at,
		(SELECT count(*) FROM role_bindings b WHERE b.role_id = r.id)
		FROM roles r `+where+` ORDER BY r.builtin DESC, CASE r.id WHEN 'admin' THEN 0 WHEN 'operator' THEN 1 WHEN 'viewer' THEN 2 ELSE 3 END, r.id`, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []rbac.Role{}
	for rows.Next() {
		var r rbac.Role
		var perms []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.Builtin, &perms, &r.CreatedAt, &r.UpdatedAt, &r.BindingCount); err != nil {
			return nil, err
		}
		if err := scanRole(perms, &r); err != nil {
			return nil, fmt.Errorf("role %s: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRole returns false when the id is taken.
func (a *Access) CreateRole(ctx context.Context, r rbac.Role) (bool, error) {
	perms, _ := json.Marshal(r.Permissions)
	res := a.db.WithContext(ctx).Exec(`INSERT INTO roles (id, name, description, permissions) VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT DO NOTHING`, r.ID, r.Name, r.Description, string(perms))
	return res.RowsAffected == 1, res.Error
}

// UpdateRole never touches builtin roles.
func (a *Access) UpdateRole(ctx context.Context, r rbac.Role) (bool, error) {
	perms, _ := json.Marshal(r.Permissions)
	res := a.db.WithContext(ctx).Exec(`UPDATE roles SET name = $2, description = $3, permissions = $4::jsonb, updated_at = now()
		WHERE id = $1 AND NOT builtin`, r.ID, r.Name, r.Description, string(perms))
	return res.RowsAffected == 1, res.Error
}

func (a *Access) DeleteRole(ctx context.Context, id string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`DELETE FROM roles WHERE id = $1 AND NOT builtin`, id)
	return res.RowsAffected == 1, res.Error
}

type BindingFilter struct {
	RoleID  string
	Subject string
}

// bindingSelect resolves subject display names: user name for user:<sub>,
// the group name for group:<name>.
const bindingSelect = `SELECT b.id, b.role_id, r.name, b.subject,
	CASE WHEN b.subject = '*' THEN 'everyone'
	     WHEN b.subject LIKE 'user:%' THEN COALESCE((SELECT u.name FROM users u WHERE 'user:' || u.sub = b.subject), substr(b.subject, 6))
	     ELSE substr(b.subject, 7) END,
	b.envs, b.projects, b.types, b.created_at, b.created_by
	FROM role_bindings b JOIN roles r ON r.id = b.role_id`

func (a *Access) ListBindings(ctx context.Context, f BindingFilter) ([]rbac.Binding, error) {
	where, args := []string{"1=1"}, []any{}
	if f.RoleID != "" {
		args = append(args, f.RoleID)
		where = append(where, fmt.Sprintf("b.role_id = $%d", len(args)))
	}
	if f.Subject != "" {
		args = append(args, f.Subject)
		where = append(where, fmt.Sprintf("b.subject = $%d", len(args)))
	}
	return a.bindings(ctx, bindingSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY b.role_id, b.id`, args...)
}

func (a *Access) GetBinding(ctx context.Context, id int64) (*rbac.Binding, error) {
	bs, err := a.bindings(ctx, bindingSelect+` WHERE b.id = $1`, id)
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return &bs[0], nil
}

func (a *Access) bindings(ctx context.Context, query string, args ...any) ([]rbac.Binding, error) {
	rows, err := a.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []rbac.Binding{}
	for rows.Next() {
		var b rbac.Binding
		var envs, projects, types []byte
		if err := rows.Scan(&b.ID, &b.RoleID, &b.RoleName, &b.Subject, &b.SubjectName, &envs, &projects, &types, &b.CreatedAt, &b.CreatedBy); err != nil {
			return nil, err
		}
		b.Envs, b.Projects, b.Types = []string{}, []string{}, []string{}
		for _, f := range []struct {
			raw []byte
			dst *[]string
		}{{envs, &b.Envs}, {projects, &b.Projects}, {types, &b.Types}} {
			if err := json.Unmarshal(f.raw, f.dst); err != nil {
				return nil, fmt.Errorf("binding %d: %w", b.ID, err)
			}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BindingScope is where an environment grant applies.
type BindingScope struct {
	Envs, Projects, Types []string
}

func (s BindingScope) json() (envs, projects, types string) {
	enc := func(v []string) string {
		if v == nil {
			v = []string{}
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	return enc(s.Envs), enc(s.Projects), enc(s.Types)
}

// CreateBinding returns 0 when an identical grant (role, subject, scope) exists.
func (a *Access) CreateBinding(ctx context.Context, roleID, subject string, scope BindingScope, by string) (int64, error) {
	e, p, t := scope.json()
	var ids []int64
	err := a.db.WithContext(ctx).Raw(`INSERT INTO role_bindings (role_id, subject, envs, projects, types, created_by)
		VALUES ($1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6)
		ON CONFLICT DO NOTHING RETURNING id`, roleID, subject, e, p, t, by).Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// UpdateBindingScope changes where a grant applies. An identical grant
// already existing is reported by the unique index as an error.
func (a *Access) UpdateBindingScope(ctx context.Context, id int64, scope BindingScope) (bool, error) {
	e, p, t := scope.json()
	res := a.db.WithContext(ctx).Exec(`UPDATE role_bindings SET envs = $2::jsonb, projects = $3::jsonb, types = $4::jsonb WHERE id = $1`, id, e, p, t)
	return res.RowsAffected == 1, res.Error
}

func (a *Access) DeleteBinding(ctx context.Context, id int64) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`DELETE FROM role_bindings WHERE id = $1`, id)
	return res.RowsAffected == 1, res.Error
}

// CountUsableAdmins counts enabled users holding admin directly for every
// environment. Admin bindings are locked first so two concurrent removals
// cannot both pass the "not the last one" check. Group-granted admins do not
// count: the identity provider can take group membership away at any time.
func (a *Access) CountUsableAdmins(ctx context.Context) (int64, error) {
	if err := a.lockAdminBindings(ctx); err != nil {
		return 0, err
	}
	var n int64
	err := a.db.WithContext(ctx).Raw(`SELECT count(*) FROM role_bindings b JOIN users u ON b.subject = 'user:' || u.sub
		WHERE b.role_id = 'admin' AND b.envs @> '["*"]' AND NOT u.disabled`).Scan(&n).Error
	return n, err
}

func (a *Access) lockAdminBindings(ctx context.Context) error {
	var ids []int64
	return a.db.WithContext(ctx).Raw(`SELECT id FROM role_bindings WHERE role_id = 'admin' FOR UPDATE`).Scan(&ids).Error
}

// Policy loads every role and binding.
func (a *Access) Policy(ctx context.Context) (*rbac.Policy, error) {
	roles, err := a.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := a.bindings(ctx, bindingSelect+` ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	p := &rbac.Policy{Roles: map[string]rbac.Role{}, Bindings: bindings}
	for _, r := range roles {
		p.Roles[r.ID] = r
	}
	return p, nil
}

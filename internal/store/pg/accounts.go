package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Accounts covers users, local credentials and sessions. Login protection
// counters and captchas live in security.go on the same repository.
type Accounts struct {
	db *gorm.DB
}

type RoleRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// User is a person known to Tide, local or SSO.
type User struct {
	ID          int64     `json:"id"`
	Sub         string    `json:"sub"`
	Method      string    `json:"method"`
	Username    string    `json:"username,omitempty"`
	Name        string    `json:"name"`
	Email       string    `json:"email,omitempty"`
	Groups      []string  `json:"groups"`      // IdP groups at last SSO sign-in
	LocalGroups []string  `json:"localGroups"` // Tide group memberships
	Roles       []RoleRef `json:"roles"`       // roles bound directly to the user
	Disabled    bool      `json:"disabled"`
	// LarkID is the Lark user_id or open_id a notification mentions this
	// person by; LarkIDSource says whether the SSO token or an administrator
	// set it.
	LarkID       string `json:"larkId,omitempty"`
	LarkIDSource string `json:"larkIdSource,omitempty"`
	// TeamsID is the Microsoft Entra object id a Teams card mentions this
	// person by, with where it came from.
	TeamsID       string     `json:"teamsId,omitempty"`
	TeamsIDSource string     `json:"teamsIdSource,omitempty"`
	LastLoginAt   *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// userColumns selects a User; scanUsers reads rows produced with it.
const userColumns = `u.id, u.sub, u.method, COALESCE(u.username, ''), u.name, COALESCE(u.email, ''), u.idp_groups,
	COALESCE((SELECT jsonb_agg(gm.group_name ORDER BY gm.group_name) FROM group_members gm WHERE gm.user_id = u.id), '[]'),
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id', r.id, 'name', r.name) ORDER BY r.id)
	          FROM role_bindings b JOIN roles r ON r.id = b.role_id WHERE b.subject = 'user:' || u.sub), '[]'),
	u.disabled, COALESCE(u.lark_id, ''), COALESCE(u.lark_id_source, ''), COALESCE(u.teams_id, ''), COALESCE(u.teams_id_source, ''),
	u.last_login_at, u.created_at`

func (a *Accounts) queryUsers(ctx context.Context, query string, args ...any) ([]User, error) {
	rows, err := a.db.WithContext(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []User{}
	for rows.Next() {
		var u User
		var groups, local, roles []byte
		if err := rows.Scan(&u.ID, &u.Sub, &u.Method, &u.Username, &u.Name, &u.Email, &groups, &local, &roles,
			&u.Disabled, &u.LarkID, &u.LarkIDSource, &u.TeamsID, &u.TeamsIDSource, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Groups, u.LocalGroups, u.Roles = []string{}, []string{}, []RoleRef{}
		for _, j := range []struct {
			b []byte
			v any
		}{{groups, &u.Groups}, {local, &u.LocalGroups}, {roles, &u.Roles}} {
			if err := json.Unmarshal(j.b, j.v); err != nil {
				return nil, fmt.Errorf("user %d: %w", u.ID, err)
			}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (a *Accounts) oneUser(ctx context.Context, where string, arg any) (*User, error) {
	us, err := a.queryUsers(ctx, `SELECT `+userColumns+` FROM users u WHERE `+where, arg)
	if err != nil || len(us) == 0 {
		return nil, err
	}
	return &us[0], nil
}

func (a *Accounts) UserByID(ctx context.Context, id int64) (*User, error) {
	return a.oneUser(ctx, `u.id = $1`, id)
}

func (a *Accounts) UserBySub(ctx context.Context, sub string) (*User, error) {
	return a.oneUser(ctx, `u.sub = $1`, sub)
}

type UserFilter struct {
	Query    string
	Method   string // local / oidc / ""
	Disabled *bool
}

func (a *Accounts) ListUsers(ctx context.Context, f UserFilter, page, pageSize int) ([]User, int64, error) {
	where, args := []string{"1=1"}, []any{}
	if f.Query != "" {
		args = append(args, likePattern(f.Query))
		where = append(where, fmt.Sprintf(`(u.username ILIKE $%[1]d ESCAPE '\' OR u.name ILIKE $%[1]d ESCAPE '\'
			OR u.email ILIKE $%[1]d ESCAPE '\' OR u.sub ILIKE $%[1]d ESCAPE '\')`, len(args)))
	}
	if f.Method != "" {
		args = append(args, f.Method)
		where = append(where, fmt.Sprintf(`u.method = $%d`, len(args)))
	}
	if f.Disabled != nil {
		args = append(args, *f.Disabled)
		where = append(where, fmt.Sprintf(`u.disabled = $%d`, len(args)))
	}
	cond := strings.Join(where, " AND ")
	var total int64
	if err := a.db.WithContext(ctx).Raw(`SELECT count(*) FROM users u WHERE `+cond, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	args = append(args, pageSize, (page-1)*pageSize)
	users, err := a.queryUsers(ctx, fmt.Sprintf(`SELECT %s FROM users u WHERE %s
		ORDER BY u.disabled, COALESCE(u.last_login_at, u.created_at) DESC, u.id LIMIT $%d OFFSET $%d`,
		userColumns, cond, len(args)-1, len(args)), args...)
	return users, total, err
}

// LocalLogin is what password sign-in needs.
type LocalLogin struct {
	UserID       int64
	Sub          string
	Name         string
	PasswordHash string
	Disabled     bool
}

func (a *Accounts) LocalLogin(ctx context.Context, username string) (*LocalLogin, error) {
	var row LocalLogin
	res := a.db.WithContext(ctx).Raw(`SELECT u.id AS user_id, u.sub, u.name, c.password_hash, u.disabled
		FROM users u JOIN local_credentials c ON c.user_id = u.id WHERE u.username = $1`, username).Scan(&row)
	if res.Error != nil || res.RowsAffected == 0 {
		return nil, res.Error
	}
	return &row, nil
}

// CreateLocalUser returns 0 when the username is taken.
func (a *Accounts) CreateLocalUser(ctx context.Context, username, name, hash string) (int64, error) {
	var ids []int64
	err := a.db.WithContext(ctx).Raw(`INSERT INTO users (sub, method, username, name) VALUES ('local:' || $1, 'local', $1, $2)
		ON CONFLICT DO NOTHING RETURNING id`, username, name).Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	err = a.db.WithContext(ctx).Exec(`INSERT INTO local_credentials (user_id, password_hash) VALUES ($1, $2)`, ids[0], hash).Error
	return ids[0], err
}

// UpsertSSOUser records an SSO sign-in and returns the user id and whether
// the account is disabled.
func (a *Accounts) UpsertSSOUser(ctx context.Context, sub, name, email string, groups []string) (int64, bool, error) {
	g, _ := json.Marshal(groups)
	var row struct {
		ID       int64
		Disabled bool
	}
	err := a.db.WithContext(ctx).Raw(`INSERT INTO users (sub, method, name, email, idp_groups)
		VALUES ($1, 'oidc', $2, NULLIF($3, ''), $4::jsonb)
		ON CONFLICT (sub) DO UPDATE SET name = EXCLUDED.name, email = EXCLUDED.email, idp_groups = EXCLUDED.idp_groups
		RETURNING id, disabled`, sub, name, email, string(g)).Scan(&row).Error
	return row.ID, row.Disabled, err
}

// LarkIDPattern is what a Lark user_id or open_id looks like. Nothing else
// goes into a mention: the id is written into the message markup.
var LarkIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SetLarkIDFromSSO records the Lark id the SSO token carried. A token
// without one leaves whatever is there, so an id an administrator entered
// survives until the IdP starts sending its own.
func (a *Accounts) SetLarkIDFromSSO(ctx context.Context, userID int64, id string) error {
	if !LarkIDPattern.MatchString(id) {
		return nil
	}
	return a.db.WithContext(ctx).Exec(`UPDATE users SET lark_id = $2, lark_id_source = 'sso'
		WHERE id = $1 AND lark_id IS DISTINCT FROM $2`, userID, id).Error
}

// SetLarkID is an administrator setting or clearing a user's Lark id.
func (a *Accounts) SetLarkID(ctx context.Context, userID int64, id string) (bool, error) {
	if id != "" && !LarkIDPattern.MatchString(id) {
		return false, fmt.Errorf("lark id %q is not a Lark user_id or open_id", id)
	}
	res := a.db.WithContext(ctx).Exec(`UPDATE users SET lark_id = NULLIF($2, ''),
		lark_id_source = CASE WHEN $2 = '' THEN NULL ELSE 'manual' END WHERE id = $1`, userID, id)
	return res.RowsAffected > 0, res.Error
}

// TeamsIDPattern is a Microsoft Entra object id: a GUID, nothing else, for
// the same reason as LarkIDPattern.
var TeamsIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SetTeamsIDFromSSO is SetLarkIDFromSSO for the Teams id.
func (a *Accounts) SetTeamsIDFromSSO(ctx context.Context, userID int64, id string) error {
	if !TeamsIDPattern.MatchString(id) {
		return nil
	}
	return a.db.WithContext(ctx).Exec(`UPDATE users SET teams_id = $2, teams_id_source = 'sso'
		WHERE id = $1 AND teams_id IS DISTINCT FROM $2`, userID, id).Error
}

// SetTeamsID is an administrator setting or clearing a user's Teams id.
func (a *Accounts) SetTeamsID(ctx context.Context, userID int64, id string) (bool, error) {
	if id != "" && !TeamsIDPattern.MatchString(id) {
		return false, fmt.Errorf("teams id %q is not a Microsoft Entra object id", id)
	}
	res := a.db.WithContext(ctx).Exec(`UPDATE users SET teams_id = NULLIF($2, ''),
		teams_id_source = CASE WHEN $2 = '' THEN NULL ELSE 'manual' END WHERE id = $1`, userID, id)
	return res.RowsAffected > 0, res.Error
}

// LarkMentions returns the enabled users named by subs or in any of groups —
// IdP groups at their last sign-in and Tide groups alike, as approval rules
// name them — with their Lark and Teams ids, skipping those with neither.
func (a *Accounts) LarkMentions(ctx context.Context, subs, groups []string) ([]Mention, error) {
	if len(subs) == 0 && len(groups) == 0 {
		return nil, nil
	}
	if subs == nil {
		subs = []string{}
	}
	if groups == nil {
		groups = []string{}
	}
	var out []Mention
	err := a.db.WithContext(ctx).Raw(`SELECT DISTINCT COALESCE(u.lark_id, '') AS id, COALESCE(u.teams_id, '') AS teams_id, u.name FROM users u
		WHERE (u.lark_id IS NOT NULL OR u.teams_id IS NOT NULL) AND NOT u.disabled AND (
			u.sub = ANY($1)
			-- idp_groups can be JSON null for a user whose token had no groups.
			OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(u.idp_groups) = 'array'
				THEN u.idp_groups ELSE '[]' END) g WHERE g = ANY($2))
			OR EXISTS (SELECT 1 FROM group_members gm WHERE gm.user_id = u.id AND gm.group_name = ANY($2)))
		ORDER BY u.name LIMIT 50`, subs, groups).Scan(&out).Error
	return out, err
}

// Mention is a person a notification names: by their Lark id in Lark, by
// their Teams id in Teams. Either may be empty.
type Mention struct {
	ID      string `json:"id"`
	TeamsID string `json:"teamsId" gorm:"column:teams_id"`
	Name    string `json:"name"`
}

func (a *Accounts) TouchLogin(ctx context.Context, userID int64) error {
	return a.db.WithContext(ctx).Exec(`UPDATE users SET last_login_at = now() WHERE id = $1`, userID).Error
}

func (a *Accounts) SetName(ctx context.Context, userID int64, name string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`UPDATE users SET name = $2 WHERE id = $1`, userID, name)
	return res.RowsAffected == 1, res.Error
}

func (a *Accounts) PasswordHash(ctx context.Context, userID int64) (string, error) {
	var hashes []string
	err := a.db.WithContext(ctx).Raw(`SELECT password_hash FROM local_credentials WHERE user_id = $1`, userID).Scan(&hashes).Error
	if err != nil || len(hashes) == 0 {
		return "", err
	}
	return hashes[0], nil
}

func (a *Accounts) SetPasswordHash(ctx context.Context, userID int64, hash string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`UPDATE local_credentials SET password_hash = $2, password_changed_at = now() WHERE user_id = $1`, userID, hash)
	return res.RowsAffected == 1, res.Error
}

func (a *Accounts) SetDisabled(ctx context.Context, userID int64, disabled bool) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`UPDATE users SET disabled = $2 WHERE id = $1`, userID, disabled)
	return res.RowsAffected == 1, res.Error
}

// Session is one sign-in as shown to its owner.
type Session struct {
	ID        string    `json:"id"`
	Current   bool      `json:"current"`
	ClientIP  string    `json:"clientIp,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (a *Accounts) CreateSession(ctx context.Context, idHash string, userID int64, clientIP, userAgent string, ttl time.Duration) error {
	return a.db.WithContext(ctx).Exec(`INSERT INTO sessions (id, user_id, client_ip, user_agent, expires_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), now() + make_interval(secs => $5))`,
		idHash, userID, clientIP, userAgent, ttl.Seconds()).Error
}

// SessionUser resolves a live session to an enabled user; nil otherwise.
// Profile, groups and disabled state are read fresh on every request.
func (a *Accounts) SessionUser(ctx context.Context, idHash string) (*User, error) {
	return a.oneUser(ctx, `NOT u.disabled AND u.id = (SELECT s.user_id FROM sessions s WHERE s.id = $1 AND s.expires_at > now())`, idHash)
}

// TouchSession pushes a live session's expiry out by ttl, never past maxLife
// from when it started.
//
// Expiry used to be fixed at sign-in, so somebody was signed out in the
// middle of what they were doing and the clock did not care that they were
// doing it. Renewing on use fixes that, and the cap is what keeps it from
// meaning "never": several pages poll on a timer, so a tab left open would
// otherwise keep a session alive with nobody there.
//
// The write only happens in the last quarter of the window. Every request
// renewing would be a write per request for a value that barely moves.
//
// Best effort, like every other bookkeeping write on the request path: a
// failure here must not refuse a request that was properly authenticated.
func (a *Accounts) TouchSession(ctx context.Context, idHash string, ttl, maxLife time.Duration) error {
	return a.db.WithContext(ctx).Exec(`UPDATE sessions
		SET expires_at = LEAST(created_at + make_interval(secs => $3), now() + make_interval(secs => $2))
		WHERE id = $1 AND expires_at > now()
		  AND expires_at < now() + make_interval(secs => $2 * 0.75)
		  AND expires_at < created_at + make_interval(secs => $3)`,
		idHash, ttl.Seconds(), maxLife.Seconds()).Error
}

func (a *Accounts) ListSessions(ctx context.Context, userID int64) ([]Session, error) {
	out := []Session{}
	err := a.db.WithContext(ctx).Raw(`SELECT id, COALESCE(client_ip, '') AS client_ip, COALESCE(user_agent, '') AS user_agent, created_at, expires_at
		FROM sessions WHERE user_id = $1 AND expires_at > now() ORDER BY created_at DESC`, userID).Scan(&out).Error
	return out, err
}

func (a *Accounts) CountSessions(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := a.db.WithContext(ctx).Raw(`SELECT count(*) FROM sessions WHERE user_id = $1 AND expires_at > now()`, userID).Scan(&n).Error
	return n, err
}

// DeleteUserSession removes one session only if it belongs to userID.
func (a *Accounts) DeleteUserSession(ctx context.Context, userID int64, idHash string) (bool, error) {
	res := a.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE id = $1 AND user_id = $2`, idHash, userID)
	return res.RowsAffected == 1, res.Error
}

func (a *Accounts) DeleteSession(ctx context.Context, idHash string) error {
	return a.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE id = $1`, idHash).Error
}

func (a *Accounts) DeleteSessionsOf(ctx context.Context, userID int64) error {
	return a.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE user_id = $1`, userID).Error
}

func (a *Accounts) PurgeExpiredSessions(ctx context.Context) error {
	return a.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE expires_at < now() - interval '1 day'`).Error
}

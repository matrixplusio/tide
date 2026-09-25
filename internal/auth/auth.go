// Package auth handles sign-in (local accounts and SSO/OIDC), server-side
// sessions and local account management. It knows nothing about HTTP
// frameworks: callers set and read cookies with the tokens it returns.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/zap"

	"tide/internal/access"
	"tide/internal/audit"
	"tide/internal/crypto"
	"tide/internal/settings"
	"tide/internal/store/pg"
)

const (
	MethodLocal = "local"
	MethodOIDC  = "oidc"

	localSubPrefix = "local:"
)

var (
	ErrBadCredentials = errors.New("invalid credentials")
	ErrTooManyTries   = errors.New("too many sign-in attempts")
	ErrUserExists     = errors.New("username already exists")
	ErrNotLocal       = errors.New("not a local account")
	ErrProfileManaged = errors.New("sso profile is managed by the identity provider")
	ErrLoginFailed    = errors.New("sso login failed")
	ErrLocalAdminOnly = errors.New("local sign-in is limited to administrators")

	// Shared with access so handlers map one error per condition.
	ErrUserNotFound = access.ErrUserNotFound
	ErrSelf         = access.ErrSelf
	ErrLastAdmin    = access.ErrLastAdmin
)

// User is the signed-in person, read fresh from the database per request.
type User struct {
	pg.User
}

func (u *User) Actor() audit.Actor { return audit.Actor{Sub: u.Sub, Name: u.Name} }

func (u *User) IsLocal() bool { return u.Method == MethodLocal }

func LocalSub(username string) string { return localSubPrefix + username }

type Service struct {
	PG       *pg.Store
	Settings *settings.Store
	Box      *crypto.Box
	Access   *access.Service

	// Captcha renders login captchas; nil uses ImageCaptcha.
	Captcha CaptchaGenerator

	mu       sync.Mutex
	provider *oidc.Provider
	issuer   string
}

// Session is a newly created session: the cookie value and its lifetime.
type Session struct {
	Token string
	TTL   time.Duration
}

func (s *Service) sessionTTL(ctx context.Context) time.Duration {
	ttl, _ := s.sessionWindow(ctx)
	return ttl
}

// sessionWindow is how long a session lives unused, and how long it may live
// however much it is used.
func (s *Service) sessionWindow(ctx context.Context) (ttl, max time.Duration) {
	sec, err := s.Settings.Security(ctx)
	if err != nil || sec.SessionTTLMinutes <= 0 {
		sec = settings.DefaultSecurity()
	}
	ttl = time.Duration(sec.SessionTTLMinutes) * time.Minute
	hours := sec.SessionMaxHours
	if hours <= 0 {
		hours = settings.DefaultSecurity().SessionMaxHours
	}
	max = time.Duration(hours) * time.Hour
	// A cap below the idle window would expire a session that is being used
	// sooner than one that is not.
	if max < ttl {
		max = ttl
	}
	return ttl, max
}

const maxUserAgent = 256

// StartSession creates a session for userID inside tx and audits the sign-in.
// A fresh random token is issued on every sign-in (no session fixation).
func (s *Service) StartSession(ctx context.Context, tx *pg.Store, userID int64, actor audit.Actor, detail map[string]any) (*Session, error) {
	ttl := s.sessionTTL(ctx)
	token := randomToken()
	m := audit.MetaFrom(ctx)
	ua := m.UserAgent
	if r := []rune(ua); len(r) > maxUserAgent {
		ua = string(r[:maxUserAgent])
	}
	if err := tx.Accounts.CreateSession(ctx, hashToken(token), userID, m.ClientIP, ua, ttl); err != nil {
		return nil, err
	}
	if err := tx.Accounts.TouchLogin(ctx, userID); err != nil {
		return nil, err
	}
	if err := tx.Audit.Write(ctx, actor, "auth.login", actor.Sub, "", detail); err != nil {
		return nil, err
	}
	return &Session{Token: token, TTL: ttl}, nil
}

// UserFromToken resolves a session cookie value; nil when invalid.
func (s *Service) UserFromToken(ctx context.Context, token string) *User {
	if token == "" || len(token) > 128 {
		return nil
	}
	id := hashToken(token)
	u, err := s.PG.Accounts.SessionUser(ctx, id)
	if err != nil || u == nil {
		return nil
	}
	// Being used is what keeps a session alive; see TouchSession for why it
	// is capped and why it does not write on every request.
	ttl, max := s.sessionWindow(ctx)
	if err := s.PG.Accounts.TouchSession(ctx, id, ttl, max); err != nil {
		zap.L().Warn("session renewal failed", zap.Error(err))
	}
	return &User{*u}
}

// SessionID is the public id of the session behind a cookie value.
func SessionID(token string) string { return hashToken(token) }

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.PG.Accounts.DeleteSession(ctx, hashToken(token))
}

func (s *Service) PurgeExpired(ctx context.Context) error {
	return s.PG.Accounts.PurgeExpiredSessions(ctx)
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Sessions are stored by hash so a database read does not yield usable cookies.
func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// SafeReturn only allows same-origin relative paths (no open redirect).
func SafeReturn(ret string) string {
	if !strings.HasPrefix(ret, "/") || strings.HasPrefix(ret, "//") || strings.ContainsAny(ret, "\\\r\n") {
		return "/"
	}
	return ret
}

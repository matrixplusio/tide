package auth

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"

	"tide/internal/access"
	"tide/internal/audit"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

const bcryptCost = 12

// dummyHash equalizes timing between unknown users and wrong passwords.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("tide-timing-equalizer"), bcryptCost)

// CreateLocalUser validates and inserts an account inside tx and returns its
// id. Callers audit.
func CreateLocalUser(ctx context.Context, tx *pg.Store, username, name, password string) (int64, error) {
	if fe := validate.Collect(
		validate.Username("username", username),
		validate.DisplayName("name", name),
		validate.Password("password", username, password),
	); fe != nil {
		return 0, fe
	}
	if name == "" {
		name = username
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return 0, err
	}
	id, err := tx.Accounts.CreateLocalUser(ctx, username, name, string(h))
	if err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, ErrUserExists
	}
	return id, nil
}

// LoginInput is a local sign-in attempt.
type LoginInput struct {
	Username    string
	Password    string
	CaptchaID   string
	CaptchaCode string
	ClientIP    string
}

// PasswordLogin verifies a local account and starts a session.
//
// Protection, counted from the audit log over the configured window: after a
// few failures for the account or the client IP a captcha is required, after
// more the attempt is refused outright. When a captcha is required but
// missing or wrong the password is not checked at all, so a guess without a
// solved captcha reveals nothing. Unknown user, wrong password and disabled
// account are indistinguishable to the caller.
func (s *Service) PasswordLogin(ctx context.Context, in LoginInput) (*User, *Session, error) {
	st, err := s.loginState(ctx, in.Username, in.ClientIP)
	if err != nil {
		return nil, nil, err
	}
	if st.locked {
		s.auditLocal(ctx, in.Username, "auth.login.locked", "")
		return nil, nil, ErrTooManyTries
	}
	if st.captcha {
		if err := s.checkCaptcha(ctx, in.CaptchaID, in.CaptchaCode, in.ClientIP); err != nil {
			if errors.Is(err, ErrCaptchaInvalid) {
				s.auditLocal(ctx, in.Username, "auth.captcha.failed", "wrong or expired captcha")
			}
			return nil, nil, err
		}
	}
	row, err := s.PG.Accounts.LocalLogin(ctx, in.Username)
	if err != nil {
		return nil, nil, err
	}
	hash, reason := dummyHash, ""
	switch {
	case row == nil:
		reason = "unknown user"
	case row.Disabled:
		hash, reason = []byte(row.PasswordHash), "account disabled"
	default:
		hash = []byte(row.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) != nil && reason == "" {
		reason = "wrong password"
	}
	if reason != "" {
		s.auditLocal(ctx, in.Username, "auth.login.failed", reason)
		return nil, nil, ErrBadCredentials
	}
	u, err := s.PG.Accounts.UserByID(ctx, row.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u == nil {
		return nil, nil, ErrBadCredentials
	}
	user := &User{*u}
	sec, err := s.Settings.Security(ctx)
	if err != nil {
		return nil, nil, err
	}
	if sec.LocalLoginAdminsOnly {
		// Checked after the password so it reveals nothing to a guesser.
		admin, err := s.Access.IsAdmin(ctx, u)
		if err != nil {
			return nil, nil, err
		}
		if !admin {
			s.auditLocal(ctx, in.Username, "auth.login.denied", "local sign-in limited to administrators")
			return nil, nil, ErrLocalAdminOnly
		}
	}
	var sess *Session
	err = s.PG.Tx(ctx, func(tx *pg.Store) error {
		sess, err = s.StartSession(ctx, tx, u.ID, user.Actor(), map[string]any{"method": MethodLocal})
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return user, sess, nil
}

func (s *Service) auditLocal(ctx context.Context, username, action, reason string) {
	detail := map[string]any{"method": MethodLocal}
	if reason != "" {
		detail["reason"] = reason
	}
	_ = s.PG.Audit.Write(ctx, audit.Actor{Sub: LocalSub(username), Name: username}, action, LocalSub(username), "", detail)
}

// ChangePassword is the self-service path and requires the current password.
func (s *Service) ChangePassword(ctx context.Context, u *User, current, next string) error {
	if !u.IsLocal() {
		return ErrNotLocal
	}
	hash, err := s.PG.Accounts.PasswordHash(ctx, u.ID)
	if err != nil {
		return err
	}
	if hash == "" {
		return ErrUserNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return validate.Errors{{Field: "currentPassword", Key: "au.currentPasswordWrong"}}
	}
	if current == next {
		return validate.Errors{{Field: "newPassword", Key: "au.newPasswordSame"}}
	}
	return s.setPassword(ctx, u.Actor(), &u.User, next)
}

// target loads a user another person is acting on.
func (s *Service) target(ctx context.Context, id int64) (*pg.User, error) {
	u, err := s.PG.Accounts.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}
	return u, nil
}

// ResetPassword is the administrator path for someone else's local account.
func (s *Service) ResetPassword(ctx context.Context, admin *User, userID int64, next string) error {
	u, err := s.target(ctx, userID)
	if err != nil {
		return err
	}
	if u.ID == admin.ID {
		return ErrSelf
	}
	if u.Method != MethodLocal {
		return ErrNotLocal
	}
	return s.setPassword(ctx, admin.Actor(), u, next)
}

// setPassword replaces the hash and ends every session of the account.
func (s *Service) setPassword(ctx context.Context, actor audit.Actor, u *pg.User, next string) error {
	if fe := validate.Collect(validate.Password("newPassword", u.Username, next)); fe != nil {
		return fe
	}
	h, err := bcrypt.GenerateFromPassword([]byte(next), bcryptCost)
	if err != nil {
		return err
	}
	return s.PG.Tx(ctx, func(tx *pg.Store) error {
		ok, err := tx.Accounts.SetPasswordHash(ctx, u.ID, string(h))
		if err != nil {
			return err
		}
		if !ok {
			return ErrUserNotFound
		}
		if err := tx.Accounts.DeleteSessionsOf(ctx, u.ID); err != nil {
			return err
		}
		// Never the password or its hash.
		return tx.Audit.Write(ctx, actor, "user.password", u.Sub, "", map[string]any{"username": u.Username})
	})
}

// SetDisabled enables or disables an account (local or SSO); disabling ends
// its sessions and may not leave the instance without a usable administrator.
func (s *Service) SetDisabled(ctx context.Context, admin *User, userID int64, disabled bool) error {
	u, err := s.target(ctx, userID)
	if err != nil {
		return err
	}
	if disabled && u.ID == admin.ID {
		return ErrSelf
	}
	err = s.PG.Tx(ctx, func(tx *pg.Store) error {
		if _, err := tx.Accounts.SetDisabled(ctx, u.ID, disabled); err != nil {
			return err
		}
		if disabled {
			if err := access.EnsureAdminLeft(ctx, tx); err != nil {
				return err
			}
			if err := tx.Accounts.DeleteSessionsOf(ctx, u.ID); err != nil {
				return err
			}
		}
		action := "user.enable"
		if disabled {
			action = "user.disable"
		}
		if err := tx.Audit.Write(ctx, admin.Actor(), action, u.Sub, "", map[string]any{"name": u.Name}); err != nil {
			return err
		}
		// Every replica has its own idea of who may do what; disabling an
		// account has to reach all of them, not just this one.
		return tx.Cache.Bump(ctx, pg.ScopeAccess)
	})
	s.Access.Invalidate()
	return err
}

// RevokeSessions signs a user out everywhere.
func (s *Service) RevokeSessions(ctx context.Context, admin *User, userID int64) error {
	u, err := s.target(ctx, userID)
	if err != nil {
		return err
	}
	return s.PG.Tx(ctx, func(tx *pg.Store) error {
		if err := tx.Accounts.DeleteSessionsOf(ctx, u.ID); err != nil {
			return err
		}
		return tx.Audit.Write(ctx, admin.Actor(), "user.sessions.revoke", u.Sub, "", nil)
	})
}

// Rename changes a local account's display name. SSO names come from the IdP.
func (s *Service) Rename(ctx context.Context, actor *User, userID int64, name string) (*pg.User, error) {
	u, err := s.target(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Method != MethodLocal {
		return nil, ErrProfileManaged
	}
	if fe := validate.Collect(validate.Required("name", name, "label.personName"), validate.DisplayName("name", name)); fe != nil {
		return nil, fe
	}
	err = s.PG.Tx(ctx, func(tx *pg.Store) error {
		if _, err := tx.Accounts.SetName(ctx, u.ID, name); err != nil {
			return err
		}
		return tx.Audit.Write(ctx, actor.Actor(), "user.rename", u.Sub, "", map[string]any{"before": u.Name, "after": name})
	})
	if err != nil {
		return nil, err
	}
	return s.PG.Accounts.UserByID(ctx, u.ID)
}

func (s *Service) CreateUser(ctx context.Context, admin *User, username, name, password string) (*pg.User, error) {
	var id int64
	err := s.PG.Tx(ctx, func(tx *pg.Store) error {
		var err error
		if id, err = CreateLocalUser(ctx, tx, username, name, password); err != nil {
			return err
		}
		return tx.Audit.Write(ctx, admin.Actor(), "user.create", LocalSub(username), "",
			map[string]any{"username": username, "name": name})
	})
	if err != nil {
		return nil, err
	}
	return s.PG.Accounts.UserByID(ctx, id)
}

// DeleteOwnSession signs out one of the user's other sessions.
func (s *Service) DeleteOwnSession(ctx context.Context, u *User, currentToken, sessionID string) error {
	if sessionID == SessionID(currentToken) {
		return ErrSelf
	}
	ok, err := s.PG.Accounts.DeleteUserSession(ctx, u.ID, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSessionNotFound
	}
	return s.PG.Audit.Write(ctx, u.Actor(), "auth.session.revoke", u.Sub, "", map[string]any{"session": sessionID[:12]})
}

var ErrSessionNotFound = errors.New("session not found")

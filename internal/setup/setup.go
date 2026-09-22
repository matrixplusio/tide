// Package setup runs the first-deploy wizard: prove cluster access with the
// setup token printed to the pod log, create the first administrator with a
// password they choose (there is no default password), and you are in. SSO,
// upstreams and everything else are configured afterwards in settings.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"tide/internal/audit"
	"tide/internal/auth"
	"tide/internal/rbac"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

var ErrAlreadyInitialized = errors.New("already initialized")

type state struct {
	Initialized bool   `json:"initialized"`
	Token       string `json:"token" secret:"true"`
}

type Service struct {
	PG       *pg.Store
	Settings *settings.Store
	Auth     *auth.Service

	initialized atomic.Bool
}

// Init creates the setup state on first start. Every replica prints the same
// token because it is stored (encrypted), not generated per process.
func (s *Service) Init(ctx context.Context) error {
	var st state
	err := s.Settings.Load(ctx, settings.SectionSetup, &st)
	if errors.Is(err, settings.ErrNotConfigured) {
		b := make([]byte, 20)
		rand.Read(b)
		enc, encErr := s.Settings.Box.Encrypt(strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)))
		if encErr != nil {
			return encErr
		}
		raw, _ := json.Marshal(map[string]any{"initialized": false, "token": enc})
		if err := s.PG.Settings.PutIfAbsent(ctx, settings.SectionSetup, raw, "system:setup"); err != nil {
			return err
		}
		s.Settings.Invalidate()
		err = s.Settings.Load(ctx, settings.SectionSetup, &st)
	}
	if err != nil {
		return err
	}
	s.initialized.Store(st.Initialized)
	if !st.Initialized {
		// Every replica announces this, and every replica announces the same
		// value: the token is stored, not generated per process, and whichever
		// replica lost the race to create it re-reads the one that won. The
		// message says so because `kubectl logs` over three replicas shows
		// three lines, which reads as three tokens worth trying one by one.
		// Every replica has to say it — you cannot know in advance whose log
		// you will be reading.
		zap.L().Warn("tide is not initialized: open /setup and enter the setup token (the same token on every replica)",
			zap.String("setup_token", st.Token))
	}
	return nil
}

func (s *Service) Initialized() bool { return s.initialized.Load() }

// Refresh re-reads state; another replica may have finished setup.
func (s *Service) Refresh(ctx context.Context) error {
	s.Settings.Invalidate()
	var st state
	if err := s.Settings.Load(ctx, settings.SectionSetup, &st); err != nil {
		return err
	}
	s.initialized.Store(st.Initialized)
	return nil
}

// Setup token guesses allowed per client IP within TokenWindow.
const (
	TokenWindow      = 15 * time.Minute
	MaxTokenFailures = 10
)

// TokenAttemptsExceeded reports whether ip used up its setup-token guesses.
// Counted from the audit log, so the limit holds across replicas.
func (s *Service) TokenAttemptsExceeded(ctx context.Context, ip string) (bool, error) {
	n, err := s.PG.Accounts.AuditCountFromIP(ctx, "setup.token.failed", ip, TokenWindow)
	return n >= MaxTokenFailures, err
}

// AuditTokenFailure records a wrong setup token (counts toward the limit).
func (s *Service) AuditTokenFailure(ctx context.Context) {
	_ = s.PG.Audit.Write(ctx, audit.Actor{Sub: "anonymous", Name: "anonymous"}, "setup.token.failed", "", "", nil)
}

// CheckToken compares in constant time. Always false once initialized.
func (s *Service) CheckToken(ctx context.Context, token string) bool {
	if err := s.Refresh(ctx); err != nil || s.Initialized() {
		return false
	}
	var st state
	if err := s.Settings.Load(ctx, settings.SectionSetup, &st); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(token))), []byte(st.Token)) == 1
}

type AdminInput struct {
	Username        string
	Name            string
	Password        string
	ConfirmPassword string
}

// CreateAdmin creates the first administrator, marks the instance initialized
// and signs the administrator in, all in one transaction: two browsers racing
// through setup cannot both become admin.
func (s *Service) CreateAdmin(ctx context.Context, in AdminInput) (*auth.User, *auth.Session, error) {
	if err := validate.Collect(
		validate.Username("username", in.Username),
		validate.DisplayName("name", in.Name),
		validate.Password("password", in.Username, in.Password),
		validate.Confirm("confirmPassword", in.Password, in.ConfirmPassword),
	); err != nil {
		return nil, nil, err
	}
	name := in.Name
	if name == "" {
		name = in.Username
	}
	var sess *auth.Session
	var u *auth.User
	err := s.PG.Tx(ctx, func(tx *pg.Store) error {
		raw, err := tx.Settings.GetForUpdate(ctx, settings.SectionSetup)
		if err != nil {
			return err
		}
		var st map[string]any
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		if done, _ := st["initialized"].(bool); done {
			return ErrAlreadyInitialized
		}
		id, err := auth.CreateLocalUser(ctx, tx, in.Username, in.Name, in.Password)
		if err != nil {
			return err
		}
		if _, err := tx.Access.CreateBinding(ctx, rbac.RoleAdmin, "user:"+auth.LocalSub(in.Username), pg.BindingScope{Envs: []string{rbac.EnvAll}}, auth.LocalSub(in.Username)); err != nil {
			return err
		}
		st["initialized"] = true
		b, _ := json.Marshal(st)
		if err := tx.Settings.Put(ctx, settings.SectionSetup, b, auth.LocalSub(in.Username)); err != nil {
			return err
		}
		actor := audit.Actor{Sub: auth.LocalSub(in.Username), Name: name}
		if err := tx.Audit.Write(ctx, actor, "setup.admin", actor.Sub, "", map[string]any{"username": in.Username}); err != nil {
			return err
		}
		if err := tx.Audit.Write(ctx, actor, "setup.finish", "", "", nil); err != nil {
			return err
		}
		if sess, err = s.Auth.StartSession(ctx, tx, id, actor, map[string]any{"method": auth.MethodLocal}); err != nil {
			return err
		}
		pu, err := tx.Accounts.UserByID(ctx, id)
		if err != nil {
			return err
		}
		u = &auth.User{User: *pu}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	s.Settings.Invalidate()
	if s.Auth.Access != nil {
		s.Auth.Access.Invalidate()
	}
	s.initialized.Store(true)
	zap.L().Info("tide initialized", zap.String("admin", in.Username))
	return u, sess, nil
}

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"tide/internal/audit"
	"tide/internal/settings"
	"tide/internal/store/pg"
)

// ErrAccountDisabled: the SSO user was disabled in Tide.
var ErrAccountDisabled = errors.New("account disabled")

// ssoFlow is the per-login state kept in an encrypted, short-lived cookie.
type ssoFlow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Return   string `json:"r"`
	Expires  int64  `json:"e"`
}

// SSOConfigured reports whether SSO sign-in is available.
func (s *Service) SSOConfigured(ctx context.Context) bool {
	var c settings.OIDC
	return s.Settings.Load(ctx, settings.SectionOIDC, &c) == nil && c.Issuer != "" && c.ClientID != ""
}

// ResetProvider forces rediscovery after SSO settings change.
func (s *Service) ResetProvider() {
	s.mu.Lock()
	s.provider = nil
	s.mu.Unlock()
}

func (s *Service) oauth(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, settings.OIDC, error) {
	var c settings.OIDC
	if err := s.Settings.Load(ctx, settings.SectionOIDC, &c); err != nil {
		return nil, nil, c, fmt.Errorf("%w: SSO is not configured", ErrLoginFailed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider == nil || s.issuer != c.Issuer {
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		p, err := oidc.NewProvider(dctx, c.Issuer)
		if err != nil {
			return nil, nil, c, fmt.Errorf("%w: discovery %s: %w", ErrLoginFailed, c.Issuer, err)
		}
		s.provider, s.issuer = p, c.Issuer
	}
	scopes := c.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
	}
	oc := &oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURL,
		Endpoint: s.provider.Endpoint(), Scopes: scopes}
	return oc, s.provider.Verifier(&oidc.Config{ClientID: c.ClientID}), c, nil
}

// SSOStart returns the IdP authorization URL (code flow + PKCE) and the
// encrypted flow state to store in a short-lived cookie.
func (s *Service) SSOStart(ctx context.Context, returnPath string) (authURL, flowCookie string, err error) {
	oc, _, _, err := s.oauth(ctx)
	if err != nil {
		return "", "", err
	}
	f := ssoFlow{State: randomToken(), Nonce: randomToken(), Verifier: oauth2.GenerateVerifier(),
		Return: SafeReturn(returnPath), Expires: time.Now().Add(10 * time.Minute).Unix()}
	b, _ := json.Marshal(f)
	enc, err := s.Box.Encrypt(string(b))
	if err != nil {
		return "", "", err
	}
	return oc.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier)), enc, nil
}

// SSOFinish validates the callback against the flow cookie, verifies the ID
// token and starts a session. It returns where to send the browser next.
func (s *Service) SSOFinish(ctx context.Context, flowCookie string, q url.Values) (*User, *Session, string, error) {
	plain, err := s.Box.Decrypt(flowCookie)
	if err != nil || flowCookie == "" {
		return nil, nil, "", fmt.Errorf("%w: missing or invalid login state, start again", ErrLoginFailed)
	}
	var f ssoFlow
	if err := json.Unmarshal([]byte(plain), &f); err != nil || time.Now().Unix() > f.Expires {
		return nil, nil, "", fmt.Errorf("%w: login state expired", ErrLoginFailed)
	}
	if q.Get("state") == "" || q.Get("state") != f.State {
		return nil, nil, "", fmt.Errorf("%w: state mismatch", ErrLoginFailed)
	}
	if e := q.Get("error"); e != "" {
		return nil, nil, "", fmt.Errorf("%w: %s: %s", ErrLoginFailed, e, q.Get("error_description"))
	}
	oc, verifier, cfg, err := s.oauth(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	tok, err := oc.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: token exchange: %w", ErrLoginFailed, err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, nil, "", fmt.Errorf("%w: no id_token in response", ErrLoginFailed)
	}
	idt, err := verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: id token: %w", ErrLoginFailed, err)
	}
	if idt.Nonce != f.Nonce {
		return nil, nil, "", fmt.Errorf("%w: nonce mismatch", ErrLoginFailed)
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return nil, nil, "", err
	}
	sub, email := idt.Subject, str(claims, "email")
	grp := groups(claims, cfg.GroupsClaim)
	if strings.HasPrefix(sub, localSubPrefix) || len(sub) > 255 {
		// Never let an IdP subject impersonate a local account.
		return nil, nil, "", fmt.Errorf("%w: subject %q is not acceptable", ErrLoginFailed, sub)
	}
	name := firstNonEmpty(str(claims, "name"), str(claims, "preferred_username"), email, sub)
	if r := []rune(name); len(r) > 64 {
		name = string(r[:64])
	}
	var sess *Session
	var user *User
	err = s.PG.Tx(ctx, func(tx *pg.Store) error {
		id, disabled, err := tx.Accounts.UpsertSSOUser(ctx, sub, name, email, grp)
		if err != nil {
			return err
		}
		if disabled {
			return ErrAccountDisabled
		}
		actor := audit.Actor{Sub: sub, Name: name}
		if sess, err = s.StartSession(ctx, tx, id, actor, map[string]any{"method": MethodOIDC, "email": email, "groups": grp}); err != nil {
			return err
		}
		pu, err := tx.Accounts.UserByID(ctx, id)
		if err != nil {
			return err
		}
		user = &User{*pu}
		return nil
	})
	if errors.Is(err, ErrAccountDisabled) {
		_ = s.PG.Audit.Write(ctx, audit.Actor{Sub: sub, Name: name}, "auth.login.failed", sub, "", map[string]any{"method": MethodOIDC, "reason": "account disabled"})
		return nil, nil, "", fmt.Errorf("%w: account disabled", ErrLoginFailed)
	}
	if err != nil {
		return nil, nil, "", err
	}
	return user, sess, f.Return, nil
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func groups(m map[string]any, claim string) []string {
	if claim == "" {
		claim = "groups"
	}
	out := []string{}
	switch v := m[claim].(type) {
	case []any:
		for _, g := range v {
			if s, ok := g.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
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
	// Without openid there is no ID token, and so no identity at all.
	if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
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
		// The Lark id a notification mentions this person by, when the IdP
		// sends one. Anything not shaped like a Lark id is ignored, not
		// stored: it ends up inside message markup.
		if cfg.LarkIDClaim != "" {
			if err := tx.Accounts.SetLarkIDFromSSO(ctx, id, larkIDClaim(claims, cfg.LarkIDClaim)); err != nil {
				return err
			}
		}
		// Likewise the Teams id (a Microsoft Entra object id). A token
		// without the claim — anyone not yet linked to Teams — changes
		// nothing.
		if cfg.TeamsIDClaim != "" {
			if err := tx.Accounts.SetTeamsIDFromSSO(ctx, id, larkIDClaim(claims, cfg.TeamsIDClaim)); err != nil {
				return err
			}
		}
		actor := audit.Actor{Sub: sub, Name: name}
		// The claim names, never their values: which field an IdP puts its
		// roles in is what an administrator has to find out to configure
		// this, and a token holds things that do not belong in an audit log.
		detail := map[string]any{"method": MethodOIDC, "email": email, "groups": grp, "claims": claimNames(claims)}
		if sess, err = s.StartSession(ctx, tx, id, actor, detail); err != nil {
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

// Limits on what an IdP can make Tide store per user: a group is a name a
// role binding refers to, not a document.
const (
	maxGroups      = 100
	maxGroupLength = 255
)

// groups reads the roles or groups an IdP sends for a user. claim names the
// field, and a dotted name reaches into nested objects ("realm.roles") when
// no field has that exact name. IdPs disagree on the shape, so each of these
// is accepted:
//
//	["dev", "ops"]                        a list of names
//	"dev,ops" / "dev ops"                 one string, comma or space separated
//	[{"code": "dev"}, {"name": "ops"}]    a list of objects with a name in them
//
// Anything else contributes nothing — a missing role means fewer permissions,
// never more. Only a signed ID token reaches here.
func groups(m map[string]any, claim string) []string {
	if claim == "" {
		claim = "groups"
	}
	v, ok := m[claim]
	if !ok && strings.Contains(claim, ".") {
		v, ok = nested(m, strings.Split(claim, "."))
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || len(s) > maxGroupLength || seen[s] || len(out) >= maxGroups {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if !ok {
		return out
	}
	switch v := v.(type) {
	case []any:
		for _, g := range v {
			switch g := g.(type) {
			case string:
				add(g)
			case map[string]any:
				// The identifier first: a display name can change and a
				// binding made against it would silently stop matching.
				for _, k := range []string{"code", "value", "key", "id", "name", "role"} {
					if s, _ := g[k].(string); s != "" {
						add(s)
						break
					}
				}
			}
		}
	case string:
		for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			add(s)
		}
	}
	return out
}

func nested(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// claimNames lists the top-level claim names of a token, sorted, without
// their values.
// larkIDClaim reads an id claim (Lark, Teams) the way the groups claim is read: by name,
// or by a dotted path when no field has that name. Only a string counts.
func larkIDClaim(m map[string]any, claim string) string {
	v, ok := m[claim]
	if !ok && strings.Contains(claim, ".") {
		v, _ = nested(m, strings.Split(claim, "."))
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func claimNames(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if len(out) == 50 {
			break
		}
		out = append(out, k)
	}
	sort.Strings(out)
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

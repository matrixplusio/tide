package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"tide/internal/access"
	"tide/internal/auth"
	"tide/internal/catalog"
	"tide/internal/config"
	"tide/internal/crypto"
	"tide/internal/notify"
	"tide/internal/server"
	"tide/internal/server/api/errcode"
	v1 "tide/internal/server/api/v1"
	"tide/internal/settings"
	"tide/internal/setup"
	"tide/internal/testdb"
)

type envelope struct {
	Code errcode.Code    `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type client struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
	last    *httptest.ResponseRecorder
}

func (c *client) do(method, path string, body any) envelope {
	c.t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		raw, ok := body.(string)
		if !ok {
			b, _ := json.Marshal(body)
			raw = string(b)
		}
		r = httptest.NewRequest(method, path, strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Host = "tide.test"
	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	c.last = w
	for _, ck := range w.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	var env envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil && strings.HasPrefix(path, "/api/") {
		c.t.Fatalf("%s %s: response is not an envelope: %s", method, path, w.Body.String())
	}
	return env
}

func newClient(t *testing.T) (*client, *setup.Service) {
	s, _ := testdb.Setup(t)
	box, _ := crypto.New("ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM=")
	set := &settings.Store{PG: s, Box: box}
	acc := &access.Service{PG: s, Settings: set}
	a := &auth.Service{PG: s, Settings: set, Box: box, Access: acc, Captcha: fixedCaptcha{}}
	su := &setup.Service{PG: s, Settings: set, Auth: a}
	if err := su.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load("")
	// A stand-in for the embedded frontend, so the tests can tell "the SPA
	// answered" apart from "nothing answered".
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html>"))
	})
	eng, err := server.Engine(cfg, v1.Deps{PG: s, Settings: set, Auth: a, Access: acc, Setup: su, Hub: &catalog.Hub{Settings: set}, Notifier: &notify.Notifier{Settings: set}}, web)
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, h: eng, cookies: map[string]*http.Cookie{}}, su
}

func expect(t *testing.T, env envelope, code errcode.Code, fields ...string) {
	t.Helper()
	if env.Code != code {
		t.Fatalf("want code %d, got %d %q %s", code, env.Code, env.Msg, env.Data)
	}
	if len(fields) == 0 {
		return
	}
	var d struct {
		Fields []errcode.FieldError `json:"fields"`
	}
	json.Unmarshal(env.Data, &d)
	got := map[string]bool{}
	for _, f := range d.Fields {
		got[f.Field] = true
		if f.Msg == "" {
			t.Errorf("field %s has no message", f.Field)
		}
	}
	for _, f := range fields {
		if !got[f] {
			t.Errorf("missing field error %q; got %s", f, env.Data)
		}
	}
}

const pw = "correct horse battery"

type fixedCaptcha struct{}

func (fixedCaptcha) Generate() (string, string, error) {
	return "k7m2x", "data:image/png;base64,AAAA", nil
}

func TestAPIContract(t *testing.T) {
	c, su := newClient(t)

	// Before setup: business APIs are gated, discovery is open.
	expect(t, c.do("GET", "/api/v1/me", nil), errcode.SetupRequired)
	expect(t, c.do("GET", "/api/v1/auth/methods", nil), errcode.OK)
	if c.last.Header().Get("X-Request-ID") == "" || c.last.Header().Get("Content-Security-Policy") == "" ||
		c.last.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing request id or security headers: %v", c.last.Header())
	}

	// Strict JSON and CSRF.
	expect(t, c.do("POST", "/api/v1/setup/token", `{"token":`), errcode.BadRequest)
	expect(t, c.do("POST", "/api/v1/setup/token", `{"token":"x","extra":1}`), errcode.BadRequest)
	expect(t, c.do("POST", "/api/v1/setup/token", map[string]string{"token": "  "}), errcode.ValidationFailed, "token")
	expect(t, c.do("POST", "/api/v1/setup/token", map[string]string{"token": "wrong"}), errcode.ValidationFailed, "token")
	expect(t, c.do("POST", "/api/v1/setup/admin", map[string]string{"username": "admin"}), errcode.SetupTokenRequired)

	st, _ := su.Refresh(context.Background()), 0
	_ = st
	var token string
	{
		// read the token the same way an operator would: from state storage
		var s struct {
			Token string `json:"token" secret:"true"`
		}
		su.Settings.Invalidate()
		if err := su.Settings.Load(context.Background(), settings.SectionSetup, &s); err != nil {
			t.Fatal(err)
		}
		token = s.Token
	}
	expect(t, c.do("POST", "/api/v1/setup/token", map[string]string{"token": token}), errcode.OK)

	// Every field problem is reported at once.
	expect(t, c.do("POST", "/api/v1/setup/admin", map[string]string{"username": "Admin", "password": "short", "confirmPassword": "different"}),
		errcode.ValidationFailed, "username", "password", "confirmPassword")
	expect(t, c.do("POST", "/api/v1/setup/admin", map[string]string{"username": "admin", "password": pw, "confirmPassword": pw + "!"}),
		errcode.ValidationFailed, "confirmPassword")
	expect(t, c.do("POST", "/api/v1/setup/admin", map[string]string{"username": "admin", "password": pw}),
		errcode.ValidationFailed, "confirmPassword")
	expect(t, c.do("POST", "/api/v1/setup/admin", map[string]string{"username": "admin", "name": "管理员", "password": pw, "confirmPassword": pw}), errcode.OK)
	expect(t, c.do("GET", "/api/v1/setup/state", nil), errcode.NotFound)

	// Signed in as admin.
	expect(t, c.do("GET", "/api/v1/me", nil), errcode.OK)
	expect(t, c.do("GET", "/api/v1/nope", nil), errcode.NotFound)

	next := "another long passphrase"
	expect(t, c.do("POST", "/api/v1/users", map[string]any{"username": "ops", "password": next, "confirmPassword": next + "x"}), errcode.ValidationFailed, "confirmPassword")
	created := c.do("POST", "/api/v1/users", map[string]any{"username": "ops", "name": "运维", "password": next, "confirmPassword": next})
	expect(t, created, errcode.OK)
	var ops struct {
		ID  int64  `json:"id"`
		Sub string `json:"sub"`
	}
	json.Unmarshal(created.Data, &ops)
	var me struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
		Permissions []string `json:"permissions"`
	}
	json.Unmarshal(c.do("GET", "/api/v1/me", nil).Data, &me)
	// Global permissions are theadmin ones; viewing is scoped per environment.
	if len(me.Permissions) < 5 {
		t.Fatalf("admin permissions: %v", me.Permissions)
	}
	adminPath := "/api/v1/users/" + itoa(me.User.ID)
	opsPath := "/api/v1/users/" + itoa(ops.ID)
	expect(t, c.do("POST", "/api/v1/users", map[string]any{"username": "ops", "password": next, "confirmPassword": next}), errcode.ValidationFailed, "username")
	expect(t, c.do("PUT", opsPath+"/password", map[string]string{"newPassword": "yet another passphrase", "confirmPassword": "nope"}), errcode.ValidationFailed, "confirmPassword")
	expect(t, c.do("PUT", adminPath+"/password", map[string]string{"newPassword": next, "confirmPassword": next}), errcode.CannotModifySelf)
	expect(t, c.do("PUT", adminPath+"/disabled", map[string]any{"disabled": true}), errcode.CannotModifySelf)
	expect(t, c.do("PUT", "/api/v1/users/abc/disabled", map[string]any{"disabled": true}), errcode.ValidationFailed, "user")
	expect(t, c.do("GET", "/api/v1/users/999999", nil), errcode.UserNotFound)
	expect(t, c.do("GET", "/api/v1/users?method=ldap&status=x", nil), errcode.ValidationFailed, "method", "status")
	expect(t, c.do("GET", "/api/v1/users?q=%25", nil), errcode.OK)
	expect(t, c.do("PUT", opsPath, map[string]string{"name": ""}), errcode.ValidationFailed, "name")

	// Roles and bindings.
	expect(t, c.do("POST", "/api/v1/roles", map[string]any{"id": "Bad", "name": "", "permissions": []string{"nope"}}), errcode.ValidationFailed, "id", "name", "permissions")
	expect(t, c.do("PUT", "/api/v1/roles/admin", map[string]any{"name": "x", "permissions": []string{"audit.view"}}), errcode.RoleBuiltin)
	expect(t, c.do("DELETE", "/api/v1/roles/viewer", nil), errcode.RoleBuiltin)
	expect(t, c.do("POST", "/api/v1/roles", map[string]any{"id": "auditor", "name": "审计员", "permissions": []string{"audit.view"}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/roles", map[string]any{"id": "auditor", "name": "审计员", "permissions": []string{"audit.view"}}), errcode.ValidationFailed, "id")
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "admin", "subject": "*", "envs": []string{"*", "prod"}}), errcode.ValidationFailed, "subject", "envs")
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "ghost", "subject": "group:x", "envs": []string{"*"}}), errcode.RoleNotFound)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "auditor", "subject": "user:nobody", "envs": []string{"tier:moon"}}), errcode.ValidationFailed, "subject", "envs")
	// Nothing is granted by default: signed-in users see nothing until an
	// administrator grants a role. An identical grant clashes, a differently
	// scoped grant of the same role does not.
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "viewer", "subject": "*", "envs": []string{"*"}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "viewer", "subject": "*", "envs": []string{"*"}}), errcode.BindingExists)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "operator", "subject": "group:acme-backend", "envs": []string{"tier:testing"}, "projects": []string{"acme"}, "types": []string{"backend"}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "operator", "subject": "group:acme-backend", "envs": []string{"tier:staging"}, "projects": []string{"acme"}, "types": []string{"backend"}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "operator", "subject": "group:acme-backend", "envs": []string{"tier:testing"}, "projects": []string{"acme"}, "types": []string{"backend"}}), errcode.BindingExists)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "operator", "subject": "group:x", "envs": []string{"tier:testing"}, "projects": []string{"*", "acme"}, "types": []string{""}}), errcode.ValidationFailed, "projects", "types.0")
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "admin", "subject": "group:x", "envs": []string{"*"}, "projects": []string{"acme"}}), errcode.ValidationFailed, "projects")
	expect(t, c.do("GET", "/api/v1/rbac/permissions", nil), errcode.OK)
	var bindings struct {
		Items []struct {
			ID      int64  `json:"id"`
			Subject string `json:"subject"`
		} `json:"items"`
	}
	json.Unmarshal(c.do("GET", "/api/v1/role-bindings?role=admin", nil).Data, &bindings)
	if len(bindings.Items) != 1 {
		t.Fatalf("admin bindings: %+v", bindings)
	}
	expect(t, c.do("DELETE", "/api/v1/role-bindings/"+itoa(bindings.Items[0].ID), nil), errcode.CannotModifySelf)

	// Groups.
	expect(t, c.do("POST", "/api/v1/groups", map[string]string{"name": "bad name"}), errcode.ValidationFailed, "name")
	expect(t, c.do("POST", "/api/v1/groups", map[string]string{"name": "auditors", "description": "审计组"}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/groups", map[string]string{"name": "auditors"}), errcode.ValidationFailed, "name")
	expect(t, c.do("POST", "/api/v1/groups/auditors/members", map[string]any{"userIds": []int64{ops.ID, 999999}}), errcode.ValidationFailed, "userIds")
	expect(t, c.do("POST", "/api/v1/groups/auditors/members", map[string]any{"userIds": []int64{ops.ID}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/groups/ghost/members", map[string]any{"userIds": []int64{ops.ID}}), errcode.GroupNotFound)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "auditor", "subject": "group:auditors", "envs": []string{"*"}}), errcode.OK)

	// A non-admin: the default "*" operator binding plus auditor via group.
	opsClient := &client{t: t, h: c.h, cookies: map[string]*http.Cookie{}}
	expect(t, opsClient.do("POST", "/api/v1/auth/login", map[string]string{"username": "ops", "password": next}), errcode.OK)
	expect(t, opsClient.do("GET", "/api/v1/audit", nil), errcode.OK)
	expect(t, opsClient.do("GET", "/api/v1/settings", nil), errcode.Forbidden)
	expect(t, opsClient.do("PUT", "/api/v1/settings/system", map[string]any{"siteName": "x"}), errcode.Forbidden)
	expect(t, opsClient.do("GET", "/api/v1/users", nil), errcode.Forbidden)
	expect(t, opsClient.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "admin", "subject": "user:" + ops.Sub, "envs": []string{"*"}}), errcode.Forbidden)
	expect(t, opsClient.do("GET", "/api/v1/me/bindings", nil), errcode.OK)
	expect(t, opsClient.do("PUT", "/api/v1/me/profile", map[string]string{"name": "运维同学"}), errcode.OK)
	var sessions struct {
		Items []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	json.Unmarshal(opsClient.do("GET", "/api/v1/me/sessions", nil).Data, &sessions)
	if len(sessions.Items) != 1 || !sessions.Items[0].Current {
		t.Fatalf("sessions: %+v", sessions)
	}
	expect(t, opsClient.do("DELETE", "/api/v1/me/sessions/"+sessions.Items[0].ID, nil), errcode.CannotModifySelf)
	expect(t, opsClient.do("DELETE", "/api/v1/me/sessions/"+strings.Repeat("a", 64), nil), errcode.NotFound)
	// Removing the default binding takes release permission away at once.
	json.Unmarshal(c.do("GET", "/api/v1/role-bindings?role=viewer&subject=*", nil).Data, &bindings)
	expect(t, c.do("DELETE", "/api/v1/role-bindings/"+itoa(bindings.Items[0].ID), nil), errcode.OK)
	expect(t, opsClient.do("GET", "/api/v1/services", nil), errcode.Forbidden)
	expect(t, opsClient.do("POST", "/api/v1/releases", map[string]any{"env": "qa", "jiraTicket": "OPS-1", "reason": "fix it",
		"items": []map[string]any{{"service": "a", "freight": strings.Repeat("a", 40)}}}), errcode.Forbidden)
	// Restart is its own permission: a restarter cannot upgrade, and vice versa.
	expect(t, c.do("POST", "/api/v1/roles", map[string]any{"id": "restarter", "name": "重启者", "permissions": []string{"services.view", "releases.restart"}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/role-bindings", map[string]any{"roleId": "restarter", "subject": "user:" + ops.Sub, "envs": []string{"*"}}), errcode.OK)
	expect(t, opsClient.do("POST", "/api/v1/releases", map[string]any{"env": "qa", "jiraTicket": "OPS-1", "reason": "fix it",
		"items": []map[string]any{{"service": "a", "freight": strings.Repeat("a", 40)}}}), errcode.Forbidden)
	restartResp := opsClient.do("POST", "/api/v1/releases", map[string]any{"env": "qa", "jiraTicket": "OPS-1", "reason": "reload config",
		"items": []map[string]any{{"kind": "restart", "service": "a"}}})
	if restartResp.Code == errcode.Forbidden {
		t.Fatalf("restarter must pass the permission check: %s", restartResp.Msg)
	}
	// Disabling ends the session.
	expect(t, c.do("PUT", opsPath+"/disabled", map[string]any{"disabled": true}), errcode.OK)
	expect(t, opsClient.do("GET", "/api/v1/me", nil), errcode.Unauthorized)

	expect(t, c.do("PUT", "/api/v1/me/password", map[string]string{"currentPassword": "wrong wrong wrong", "newPassword": next, "confirmPassword": next}), errcode.ValidationFailed, "currentPassword")
	expect(t, c.do("PUT", "/api/v1/me/password", map[string]string{"currentPassword": pw, "newPassword": pw, "confirmPassword": pw}), errcode.ValidationFailed, "newPassword")
	expect(t, c.do("PUT", "/api/v1/me/password", map[string]string{"currentPassword": pw, "newPassword": "my-admin-secret", "confirmPassword": "my-admin-secret"}), errcode.ValidationFailed, "newPassword")

	expect(t, c.do("PUT", "/api/v1/settings/oidc", map[string]string{"issuer": "sso.example.com", "clientId": "", "redirectUrl": "https://tide/callback"}),
		errcode.ValidationFailed, "issuer", "clientId", "redirectUrl")
	expect(t, c.do("PUT", "/api/v1/settings/system", map[string]any{"siteName": "", "announcement": map[string]any{"enabled": true, "level": "loud"}}), errcode.ValidationFailed, "siteName", "announcement.level", "announcement.text")
	expect(t, c.do("PUT", "/api/v1/settings/security", map[string]any{"sessionTtlMinutes": 0, "loginWindowMinutes": 15, "captchaAfterUserFailures": 5, "captchaAfterIpFailures": 5,
		"lockAfterUserFailures": 5, "lockAfterIpFailures": 30}), errcode.ValidationFailed, "sessionTtlMinutes", "lockAfterUserFailures")
	expect(t, c.do("PUT", "/api/v1/settings/release", map[string]any{"confirmReadSeconds": 5, "confirmTtlMinutes": 10, "executeTimeoutMinutes": 15,
		"jiraProjects": []string{"ops", "1X"}, "freezes": []map[string]any{{"name": "春节", "envs": []string{"prod"}, "startsAt": "2026-02-01T00:00:00Z", "endsAt": "2026-01-01T00:00:00Z"}}}),
		errcode.ValidationFailed, "confirmReadSeconds", "jiraProjects.1", "freezes.0.envs", "freezes.0.endsAt")
	expect(t, c.do("PUT", "/api/v1/settings/environments", map[string]any{"items": []map[string]any{{"name": "dev", "tier": "development", "upstream": "ghost"}, {"name": "dev", "tier": "moon"}}}),
		errcode.ValidationFailed, "items.0.upstream", "items.1.name", "items.1.tier")
	// A verification source must be an earlier environment (no self, no cycles).
	expect(t, c.do("PUT", "/api/v1/settings/environments", map[string]any{"items": []map[string]any{{"name": "uat", "tier": "staging", "promotesFrom": "qa"}, {"name": "qa", "tier": "testing", "promotesFrom": "qa"}}}),
		errcode.ValidationFailed, "items.0.promotesFrom", "items.1.promotesFrom")
	expect(t, c.do("PUT", "/api/v1/settings/environments", map[string]any{"items": []map[string]any{{"name": "dev", "displayName": "开发", "tier": "development"}, {"name": "prod", "tier": "production"}}}), errcode.OK)
	// Jira is required only in production-tier environments and the reason
	// everywhere (default policy); both are reported at once.
	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "prod",
		"items": []map[string]any{{"service": "a", "freight": strings.Repeat("a", 40)}}}), errcode.ValidationFailed, "jiraTicket", "reason")
	if r := c.do("POST", "/api/v1/releases", map[string]any{"env": "dev", "reason": "fix it",
		"items": []map[string]any{{"service": "a", "freight": strings.Repeat("a", 40)}}}); r.Code == errcode.ValidationFailed {
		t.Fatalf("dev without Jira must pass validation: %s", r.Data)
	}
	// The request struct's own Check() reports through a different path than
	// the policy checks above; its messages must survive it too. They went
	// missing once, when rules started carrying catalog keys instead of text.
	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "dev", "jiraTicket": "bad-format", "reason": "ab",
		"items": []map[string]any{{"kind": "image", "service": "a", "freight": ""}}}),
		errcode.ValidationFailed, "jiraTicket", "reason", "items.0.freight")
	expect(t, c.do("PUT", "/api/v1/settings/notify", map[string]any{"channels": []map[string]any{{"name": "ops", "kind": "teams", "url": "ftp://x", "secret": "s", "enabled": true}},
		"rules": []map[string]any{{"name": "r", "enabled": true, "envs": []string{"staging"}, "events": []string{"release.exploded"}, "channels": []string{"nope"}}}}),
		errcode.ValidationFailed, "channels.0.url", "channels.0.secret", "rules.0.envs", "rules.0.events", "rules.0.channels")
	expect(t, c.do("PUT", "/api/v1/settings/notify", map[string]any{"channels": []map[string]any{{"name": "ops", "kind": "lark", "url": "https://open.larksuite.com/hook/x", "enabled": true}},
		"rules": []map[string]any{{"name": "prod", "enabled": true, "envs": []string{"tier:production"}, "events": []string{"release.failed"}, "channels": []string{"ops"}}}}), errcode.OK)
	expect(t, c.do("PUT", "/api/v1/settings/release", map[string]any{"confirmReadSeconds": 10, "confirmTtlMinutes": 10, "executeTimeoutMinutes": 15, "minSoakMinutes": 30, "multiVersionJump": 3,
		"jiraProjects": []string{"OPS"}, "freezes": []map[string]any{{"name": "封版", "envs": []string{"tier:production"}, "startsAt": "2020-01-01T00:00:00Z", "endsAt": "2099-01-01T00:00:00Z", "reason": "大促"}}}), errcode.OK)
	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "prod", "jiraTicket": "OPS-1", "reason": "fix it",
		"items": []map[string]any{{"service": "a", "freight": strings.Repeat("a", 40)}}}), errcode.EnvironmentFrozen)
	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "dev", "jiraTicket": "WEB-1", "reason": "fix it",
		"items": []map[string]any{{"service": "a", "freight": strings.Repeat("a", 40)}}}), errcode.ValidationFailed, "jiraTicket")
	expect(t, c.do("PUT", "/api/v1/settings/catalog", map[string]any{"serviceLabel": "bad key!", "dimensions": []map[string]any{
		{"key": "q", "name": "", "label": "x/role", "values": []map[string]string{{"value": "a b"}}},
		{"key": "tier", "name": "级别", "label": "x/role"}}}),
		errcode.ValidationFailed, "serviceLabel", "dimensions.0.key", "dimensions.0.name", "dimensions.0.values.0.value", "dimensions.1.label")
	expect(t, c.do("PUT", "/api/v1/settings/catalog", map[string]any{"dimensions": []map[string]any{
		{"key": "role", "name": "类型", "label": "example.com/role", "values": []map[string]string{{"value": "backend", "name": "后端"}}}}}), errcode.OK)
	var meApp struct {
		App struct {
			Dimensions []struct {
				Key string `json:"key"`
			} `json:"dimensions"`
		} `json:"app"`
	}
	json.Unmarshal(c.do("GET", "/api/v1/me", nil).Data, &meApp)
	if len(meApp.App.Dimensions) != 1 || meApp.App.Dimensions[0].Key != "role" {
		t.Fatalf("me dimensions: %+v", meApp)
	}
	var settingsView map[string]json.RawMessage
	json.Unmarshal(c.do("GET", "/api/v1/settings", nil).Data, &settingsView)
	if !strings.Contains(string(settingsView["notify"]), `"url":"••••••"`) || settingsView["security"] == nil || settingsView["upstreams"] == nil {
		t.Fatalf("settings view: %v", settingsView)
	}
	expect(t, c.do("PUT", "/api/v1/settings/upstreams", map[string]any{"items": []map[string]any{{"name": "onprem", "kargoUrl": "kargo", "argocdUrl": "https://a"}}}),
		errcode.ValidationFailed, "items.0.kargoUrl", "items.0.kargoToken", "items.0.argocdToken")
	expect(t, c.do("PUT", "/api/v1/settings/bogus", map[string]string{}), errcode.UnknownSettingsSection)

	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "qa", "jiraTicket": "ops 1", "reason": "x",
		"items": []map[string]any{{"service": "a", "freight": "zz"}}}), errcode.ValidationFailed, "jiraTicket", "reason", "items.0.freight")
	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "qa", "jiraTicket": "OPS-1", "reason": "fix it", "items": []any{}}), errcode.ValidationFailed, "items")
	expect(t, c.do("POST", "/api/v1/releases", map[string]any{"env": "qa", "jiraTicket": "OPS-1", "reason": "fix it", "items": []map[string]any{
		{"kind": "reboot", "service": "a"}, {"kind": "restart", "service": "b", "freight": strings.Repeat("a", 40)}, {"service": "c"}}}),
		errcode.ValidationFailed, "items.0.kind", "items.1.freight", "items.2.freight")
	expect(t, c.do("GET", "/api/v1/releases?status=bogus&page_size=1000", nil), errcode.ValidationFailed, "status", "page_size")
	expect(t, c.do("GET", "/api/v1/releases/../../etc", nil), errcode.NotFound)
	expect(t, c.do("GET", "/api/v1/releases/REL-1", nil), errcode.ValidationFailed, "id")
	expect(t, c.do("GET", "/api/v1/audit?since=2026-09-18T00:00:00Z&until=2026-09-17T00:00:00Z", nil), errcode.ValidationFailed, "since")

	// Insights: the period and the time zone are validated like any other
	// query parameter, and the report itself comes back through the envelope.
	expect(t, c.do("GET", "/api/v1/insights?from=2026-09-18T00:00:00Z&to=2026-09-17T00:00:00Z", nil), errcode.ValidationFailed, "from")
	expect(t, c.do("GET", "/api/v1/insights?from=2020-01-01T00:00:00Z", nil), errcode.ValidationFailed, "from")
	expect(t, c.do("GET", "/api/v1/insights?tz=Mars/Olympus", nil), errcode.ValidationFailed, "tz")
	expect(t, c.do("GET", "/api/v1/insights?tz=../../etc/passwd", nil), errcode.ValidationFailed, "tz")
	expect(t, c.do("GET", "/api/v1/insights?env=NOPE", nil), errcode.ValidationFailed, "env")

	// The SPA owns its own routing, so a page the server has no route for is
	// still a page: it must answer 200 with the app, not 404 with it. gin
	// marks the context 404 before NoRoute runs, and a body written after
	// that would otherwise ship "404" to every health check and crawler
	// while looking fine in a browser.
	for _, path := range []string{"/", "/services", "/releases/REL-1", "/insights"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s: did not serve the app: %q", path, w.Body.String())
		}
	}
	// An unknown API path is a real 404, in the envelope.
	expect(t, c.do("GET", "/api/v1/nope", nil), errcode.NotFound)
	// The release history is readable even with no upstream configured; only
	// catalog coverage is missing, so the page still answers.
	expect(t, c.do("GET", "/api/v1/insights", nil), errcode.OK)
	expect(t, c.do("GET", "/api/v1/releases?page=1&page_size=5", nil), errcode.OK)
	expect(t, c.do("GET", "/api/v1/services", nil), errcode.NoUpstreams)

	// Cross-site write is refused even with a valid session.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader("{}"))
	r.Host = "tide.test"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	var env envelope
	json.Unmarshal(w.Body.Bytes(), &env)
	expect(t, env, errcode.CrossSiteBlocked)
	// Form posts are not JSON.
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("username=a&password=b"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	json.Unmarshal(w.Body.Bytes(), &env)
	expect(t, env, errcode.UnsupportedMedia)

	// Password change ends the session; the new password works.
	expect(t, c.do("PUT", "/api/v1/me/password", map[string]string{"currentPassword": pw, "newPassword": next, "confirmPassword": next}), errcode.OK)
	expect(t, c.do("GET", "/api/v1/me", nil), errcode.Unauthorized)
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": ""}), errcode.ValidationFailed, "password")
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": pw}), errcode.InvalidCredentials)
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "still wrong pw"}), errcode.InvalidCredentials)
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "still wrong pw"}), errcode.InvalidCredentials)
	// Third failure reached: the right password alone is no longer enough.
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": next}), errcode.CaptchaRequired)
	ch := c.do("GET", "/api/v1/auth/challenge?username=admin", nil)
	expect(t, ch, errcode.OK)
	var challenge struct {
		CaptchaRequired bool   `json:"captchaRequired"`
		CaptchaID       string `json:"captchaId"`
	}
	json.Unmarshal(ch.Data, &challenge)
	if !challenge.CaptchaRequired || challenge.CaptchaID == "" {
		t.Fatalf("challenge: %s", ch.Data)
	}
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": next, "captchaId": "not-a-uuid", "captchaCode": "x"}), errcode.ValidationFailed, "captchaId")
	expect(t, c.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": next, "captchaId": challenge.CaptchaID, "captchaCode": "k7m2x"}), errcode.OK)
	expect(t, c.do("GET", "/api/v1/audit?action=auth.login&page_size=5", nil), errcode.OK)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

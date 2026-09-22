package v1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"tide/internal/i18n"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
)

func TestFieldPath(t *testing.T) {
	cases := map[string][2]string{
		"loginReq.username|用户名":                     {"username", "用户名"},
		"Upstreams.items|上游[0].kargoUrl|Kargo 地址":   {"items.0.kargoUrl", "Kargo 地址"},
		"createReleaseReq.items|发布条目[2].freight|制品": {"items.2.freight", "制品"},
	}
	for ns, want := range cases {
		path, label := fieldPath(ns)
		if path != want[0] || label != want[1] {
			t.Errorf("%s → %q %q", ns, path, label)
		}
	}
}

// Regression: gin's c.Cookie URL-unescapes values, turning "+" in base64
// ciphertext into a space and randomly breaking setup and SSO cookies.
func TestCookieKeepsRawValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "x", Value: "enc:v1:ab+c/d=="})
	if v, err := cookie(c, "x"); err != nil || v != "enc:v1:ab+c/d==" {
		t.Fatalf("got %q %v", v, err)
	}
}

// Regression: a request the browser abandoned was reported as an internal
// error with a stack trace in the logs.
func TestFailOnClientCancelIsNotInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/services", nil).WithContext(ctx)
	cancel()
	respond.Fail(c, fmt.Errorf("list apps: %w", context.Canceled))
	if w.Code != respond.StatusClientClosedRequest || w.Body.Len() != 0 {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

// Regression: a rule that reports a catalog key rather than finished text lost
// it in bindJSON's Check() branch, and the field arrived at the client with an
// empty message. The handler tests that would have caught this skip without a
// database, so this one runs without one.
func TestBindJSONKeepsFieldKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"env":"dev","jiraTicket":"bad-format","reason":"ab","title":"t",` +
		`"items":[{"kind":"image","service":"a","freight":""}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	var req createReleaseReq
	err := bindJSON(c, &req)
	if err == nil {
		t.Fatal("want validation errors")
	}
	e := errcode.From(err)
	if e.Code != errcode.ValidationFailed {
		t.Fatalf("code %d", e.Code)
	}
	if len(e.Fields) == 0 {
		t.Fatal("no field errors")
	}
	for _, f := range e.Fields {
		if f.Msg == "" && f.Key == "" {
			t.Errorf("field %q carries neither a message nor a key", f.Field)
		}
		// Whatever the field carries must render in both languages.
		for _, l := range i18n.Locales() {
			text := f.Msg
			if text == "" {
				text = i18n.T(l, f.Key, f.Args...)
			}
			if text == "" || text == string(f.Key) {
				t.Errorf("field %q renders as %q in %s", f.Field, text, l)
			}
		}
	}
}

// Regression: when labels first became catalog keys they carried the "label."
// prefix into the namespace, and fieldPath — which splits on dots — turned
// "username" into "username.username", so a form could no longer attach the
// error to the field it belongs to. The tag holds the key's last segment only.
func TestFieldPathIgnoresDotsInLabels(t *testing.T) {
	for _, tc := range []struct{ ns, path, label string }{
		{"loginReq.username|username", "username", "username"},
		{"createReleaseReq.items[0].freight|freight", "items.0.freight", "freight"},
		{"upstreamsReq.items|items[2].kargoUrl|kargoToken", "items.2.kargoUrl", "kargoToken"},
		{"req.plain", "plain", ""},
	} {
		path, label := fieldPath(tc.ns)
		if path != tc.path || label != tc.label {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", tc.ns, path, label, tc.path, tc.label)
		}
	}
}

// Regression: the reason a body would not parse was rendered in the default
// language and then embedded in a localised sentence, so an English client got
// "Malformed request: 包含未知字段 nope".
func TestBodyErrorsRenderInOneLanguage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"username":"a","nope":1}`, `{"username":`, ``} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		var req loginReq
		err := bindJSON(c, &req)
		if err == nil {
			t.Fatalf("%q: want an error", body)
		}
		e := errcode.From(err)
		for _, l := range i18n.Locales() {
			text := e.Text(l)
			if text == "" {
				t.Errorf("%q: empty message in %s", body, l)
			}
			if l == i18n.En && strings.ContainsFunc(text, func(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }) {
				t.Errorf("%q: English message contains Chinese: %q", body, text)
			}
		}
	}
}

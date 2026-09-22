package validate

import (
	"strings"
	"testing"

	"tide/internal/i18n"
)

func TestPassword(t *testing.T) {
	ok := []string{"correct horse battery", "Tide-Ops-2026!", "密码足够长的中文口令也可以用"}
	for _, p := range ok {
		if e := Password("password", "admin", p); e != nil {
			t.Errorf("%q rejected: %s", p, e.Text(i18n.Default))
		}
	}
	bad := map[string]string{
		"":                      "请输入",
		"short":                 "至少 12 位",
		"            ":          "空白",
		" leading-space-pw":     "首尾",
		"aaaaaaaaaaaaaa":        "不同的字符",
		"abababababab":          "不同的字符",
		"abcdefghijklmn":        "连续",
		"123456789012":          "连续",
		"987654321098":          "连续",
		"Password1234":          "常见弱密码",
		"my-admin-password":     "用户名",
		strings.Repeat("长", 25): "字节",
	}
	for p, want := range bad {
		e := Password("password", "admin", p)
		if e == nil || !strings.Contains(e.Text(i18n.Default), want) {
			t.Errorf("%q: want %q, got %v", p, want, e)
		}
	}
}

func TestConfirmAndUsername(t *testing.T) {
	if Confirm("c", "a", "") == nil || Confirm("c", "a", "b") == nil || Confirm("c", "a", "a") != nil {
		t.Error("confirm")
	}
	for u, valid := range map[string]bool{"admin": true, "ops.lead": true, "a": false, "Admin": false, "1ops": false, "ops lead": false} {
		if (Username("u", u) == nil) != valid {
			t.Errorf("username %q", u)
		}
	}
}

func TestHTTPURL(t *testing.T) {
	for s, valid := range map[string]bool{
		"https://kargo.example.com": true, "https://kargo.example.test": true, "http://registry.svc:5001": true,
		"kargo.example.com": false, "ftp://x": false, "https://u:p@x.com": false, "https://x.com/?a=1": false,
		"https://x.com/#a": false,
	} {
		if (HTTPURL("f", s, true, BaseURL) == nil) != valid {
			t.Errorf("base %q", s)
		}
	}
	// Endpoint URLs keep the query and fragment: a webhook token often lives
	// there. Everything else stays as strict as a base URL.
	for s, valid := range map[string]bool{
		"https://hook.example.com/send?key=abc": true, "https://hook.example.com/p#f": true,
		"hook.example.com": false, "ftp://x": false, "https://u:p@x.com": false,
	} {
		if (HTTPURL("f", s, true, AnyURL) == nil) != valid {
			t.Errorf("any %q", s)
		}
	}
	if HTTPURL("f", "", false, BaseURL) != nil || HTTPURL("f", "", true, BaseURL) == nil {
		t.Error("required")
	}
}

// The rules in this package carry catalog keys, so the same failure renders in
// whatever language the request asked for.
func TestRulesRenderInEnglish(t *testing.T) {
	cases := []struct {
		e    *FieldError
		want string
	}{
		{Password("password", "admin", "short"), "A password needs at least 12 characters (currently 5)"},
		{Password("password", "admin", "my-admin-password"), "A password cannot contain the username"},
		{Username("username", "9bad"), "A username starts with a lowercase letter"},
		{Confirm("confirm", "a", "b"), "The two passwords do not match"},
		{HTTPURL("url", "https://u:p@x.com", true, BaseURL), "use the credential fields"},
		{MaxLen("description", strings.Repeat("x", 201), 200, "label.description"), "the description cannot be longer than 200 characters"},
		{Required("name", "", "label.roleName"), "Enter a role name"},
	}
	for _, c := range cases {
		if c.e == nil {
			t.Fatalf("want a failure for %q", c.want)
		}
		if got := c.e.Text(i18n.En); !strings.Contains(got, c.want) {
			t.Errorf("en: got %q, want it to contain %q", got, c.want)
		}
		// The same failure still reads Chinese for a Chinese client.
		if got := c.e.Text(i18n.ZhCN); got == c.e.Text(i18n.En) {
			t.Errorf("%q renders the same in both locales", c.e.Field)
		}
	}
}

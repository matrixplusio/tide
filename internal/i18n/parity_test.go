package i18n

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// CONVENTIONS.md §8: a rule the client mirrors must read the same on both sides in
// the same language. The client keeps its wording in web/src/locales; this
// compares the entries that describe the shared rules.
//
// Placeholders differ by design — Go uses %s and %d, the web client uses named
// {{...}} — so they are normalised away before comparing.
func TestSharedRuleWordingMatchesTheWebClient(t *testing.T) {
	pairs := map[Key]string{
		"v.usernameRequired":    "usernameRequired",
		"v.usernameFormat":      "username",
		"v.passwordRequired":    "passwordRequired",
		"v.passwordTooShort":    "passwordTooShort",
		"v.passwordTooLong":     "passwordTooLong",
		"v.passwordAllSpace":    "passwordAllSpace",
		"v.passwordEdgeSpace":   "passwordEdgeSpace",
		"v.passwordFewDistinct": "passwordFewDistinct",
		"v.passwordSequence":    "passwordSequence",
		"v.passwordCommon":      "passwordCommon",
		"v.passwordHasUsername": "passwordHasUsername",
		"v.confirmRequired":     "confirmRequired",
		"v.passwordMismatch":    "passwordMismatch",
		"v.urlRequired":         "urlRequired",
		"v.urlInvalid":          "urlInvalid",
		"v.urlCredentials":      "urlCredentials",
		"v.urlQuery":            "urlQuery",
	}
	for _, tc := range []struct {
		locale Locale
		file   string
	}{
		{ZhCN, "../../web/src/locales/zh-CN.ts"},
		{En, "../../web/src/locales/en.ts"},
	} {
		web := webValidateMessages(t, tc.file)
		for goKey, webKey := range pairs {
			got, ok := web[webKey]
			if !ok {
				t.Errorf("%s: the web client has no validate.%s", tc.locale, webKey)
				continue
			}
			if want := normalisePlaceholders(T(tc.locale, goKey)); want != normalisePlaceholders(got) {
				t.Errorf("%s %s:\n  server: %q\n  client: %q", tc.locale, goKey, T(tc.locale, goKey), got)
			}
		}
	}
}

var webEntry = regexp.MustCompile(`(?m)^\s{4}([A-Za-z][A-Za-z0-9]*): '((?:[^'\\]|\\.)*)',`)

// webValidateMessages reads the `validate: { ... }` block of a locale file.
func webValidateMessages(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(b)
	start := strings.Index(src, "validate: {")
	if start < 0 {
		t.Fatalf("%s: no validate block", path)
	}
	end := strings.Index(src[start:], "\n  },")
	if end < 0 {
		t.Fatalf("%s: unterminated validate block", path)
	}
	out := map[string]string{}
	for _, m := range webEntry.FindAllStringSubmatch(src[start:start+end], -1) {
		out[m[1]] = strings.ReplaceAll(m[2], `\'`, `'`)
	}
	if len(out) == 0 {
		t.Fatalf("%s: parsed no validate messages", path)
	}
	return out
}

var goVerb = regexp.MustCompile(`%(\[\d+\])?[-+# 0-9.*]*[a-zA-Z]`)
var webVar = regexp.MustCompile(`\{\{[^}]+\}\}`)

func normalisePlaceholders(s string) string {
	s = goVerb.ReplaceAllString(s, "{}")
	return webVar.ReplaceAllString(s, "{}")
}

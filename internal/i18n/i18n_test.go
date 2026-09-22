package i18n

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every locale must define every key zh-CN does. A gap would ship a Chinese
// sentence into an English response, which is worse than a wrong translation
// because it looks like a bug in the data.
func TestCatalogsAgree(t *testing.T) {
	for _, l := range Locales() {
		if l == Default {
			continue
		}
		for k := range zhCN {
			if !Has(l, k) {
				t.Errorf("%s is missing %q", l, k)
			}
		}
		for k := range catalogs[l] {
			if !Has(Default, k) {
				t.Errorf("%s defines %q, which %s does not", l, k, Default)
			}
		}
	}
}

// A message taking arguments must take the same ones in every locale, or a
// translation silently drops a value. Languages order a sentence differently,
// so a translation may use explicit argument indexes (%[2]s); both forms are
// counted the same way here.
func TestVerbsMatch(t *testing.T) {
	for k := range catalogs[Default] {
		want := verbs(catalogs[Default][k])
		for _, l := range Locales() {
			if l == Default || !Has(l, k) {
				continue
			}
			if got := verbs(catalogs[l][k]); got != want {
				t.Errorf("%q: %s takes %d args, %s takes %d", k, Default, want, l, got)
			}
		}
	}
}

var verbRe = regexp.MustCompile(`%(\[\d+\])?[-+# 0-9.*]*[a-zA-Z]`)

// verbs counts the arguments a format consumes. A translation may repeat one
// argument (%[2]s twice) where the reference locale names it once, so an
// explicit index counts as the position it points at, not as another argument.
func verbs(format string) int {
	found := verbRe.FindAllStringSubmatch(strings.ReplaceAll(format, "%%", ""), -1)
	n, indexed := 0, false
	for _, m := range found {
		if m[1] == "" {
			continue
		}
		indexed = true
		if i, err := strconv.Atoi(strings.Trim(m[1], "[]")); err == nil && i > n {
			n = i
		}
	}
	if indexed {
		return n
	}
	return len(found)
}

// Rendering every message with arguments of the types its verbs ask for
// catches a format Go cannot satisfy — a stray verb, or an index pointing past
// the arguments — which would otherwise reach a user as "%!s(MISSING)".
func TestEveryMessageRenders(t *testing.T) {
	for _, l := range Locales() {
		for k := range catalogs[l] {
			out := T(l, k, sampleArgs(catalogs[l][k])...)
			if strings.Contains(out, "%!") || strings.Contains(out, "(MISSING)") || strings.Contains(out, "EXTRA") {
				t.Errorf("%s %q renders as %q", l, k, out)
			}
		}
	}
}

// sampleArgs builds one argument per verb, of the kind that verb formats.
// Explicit indexes (%[2]d) place the type at the position they point at.
func sampleArgs(format string) []any {
	found := verbRe.FindAllStringSubmatch(strings.ReplaceAll(format, "%%", ""), -1)
	out := make([]any, len(found))
	for i, m := range found {
		pos := i
		if m[1] != "" {
			n, err := strconv.Atoi(strings.Trim(m[1], "[]"))
			if err != nil || n < 1 || n > len(found) {
				continue
			}
			pos = n - 1
		}
		if strings.HasSuffix(m[0], "d") {
			out[pos] = 1
			continue
		}
		out[pos] = "a"
	}
	for i, v := range out {
		if v == nil {
			out[i] = "a"
		}
	}
	return out
}

func TestTFallsBackAndNeverPanics(t *testing.T) {
	if got := T(En, "err.unauthorized"); got != "Please sign in" {
		t.Errorf("en: %q", got)
	}
	// An unknown key returns itself rather than an empty message.
	if got := T(En, "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key: %q", got)
	}
	if got := T(En, "err.internalWithRequestID", "abc"); !strings.Contains(got, "abc") {
		t.Errorf("args: %q", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	if got := From(context.Background()); got != Default {
		t.Errorf("bare context: %q", got)
	}
	if got := From(With(context.Background(), En)); got != En {
		t.Errorf("round trip: %q", got)
	}
}

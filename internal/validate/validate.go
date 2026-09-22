// Package validate holds input rules shared by the API. The web UI mirrors
// them (web/src/validate.ts) for immediate feedback; the server is authoritative.
package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"tide/internal/i18n"
)

// FieldError is a validation failure tied to one input, so the UI can show
// the message next to it.
//
// Rules run far from the request, so a failure carries a catalog Key and its
// arguments and is rendered into words at the edge (respond.Fail). Message is
// the pre-i18n path and is still used by rules that have not been migrated; it
// goes away when the last one has (docs/designs/i18n.md, phase 2).
type FieldError struct {
	Field   string
	Key     i18n.Key
	Args    []any
	Message string
}

// Text renders the failure in l.
func (e *FieldError) Text(l i18n.Locale) string {
	if e.Key != "" {
		return i18n.T(l, e.Key, e.Args...)
	}
	return e.Message
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Text(i18n.Default) }

// FieldKey reports a failure by catalog key; prefer it over Field.
func FieldKey(field string, key i18n.Key, args ...any) *FieldError {
	return &FieldError{Field: field, Key: key, Args: args}
}

// Field reports a failure with an already-rendered message. Only for rules
// not yet migrated to keys.
func Field(field, format string, args ...any) *FieldError {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Errors is a set of field errors returned together.
type Errors []FieldError

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i, f := range e {
		parts[i] = f.Field + ": " + f.Text(i18n.Default)
	}
	return strings.Join(parts, "; ")
}

// Collect returns the non-nil results as Errors, or nil when all passed.
// Use it so a form gets every problem at once, not one per round-trip.
func Collect(errs ...*FieldError) error {
	var out Errors
	for _, e := range errs {
		if e != nil {
			out = append(out, *e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// One is a single-field Errors.
func One(field, msg string) error { return Errors{{Field: field, Message: msg}} }

func DisplayName(field, s string) *FieldError {
	switch {
	case utf8.RuneCountInString(s) > 64:
		return FieldKey(field, "v.displayNameTooLong", 64)
	case strings.ContainsFunc(s, unicode.IsControl):
		return FieldKey(field, "v.displayNameControlChars")
	}
	return nil
}

// MaxLen limits free text.
// MaxLen reports an over-long field; what names it as a label key.
func MaxLen(field, s string, n int, what i18n.Key) *FieldError {
	if utf8.RuneCountInString(s) > n {
		return FieldKey(field, "v.tooLong", what, n)
	}
	return nil
}

const (
	PasswordMinLen   = 12
	PasswordMaxBytes = 72 // bcrypt ignores anything longer
)

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,31}$`)

func Username(field, s string) *FieldError {
	switch {
	case s == "":
		return FieldKey(field, "v.usernameRequired")
	case !usernameRe.MatchString(s):
		return FieldKey(field, "v.usernameFormat")
	}
	return nil
}

// common is a short deny list of passwords that pass the length rule but are
// the first things anyone tries.
var common = []string{
	"123456789012", "1234567890123", "12345678901234", "qwertyuiopas", "qwertyuiop123",
	"password1234", "password12345", "passw0rd1234", "adminadmin123", "administrator",
	"1qaz2wsx3edc", "1q2w3e4r5t6y", "abcdefghijkl", "abc123456789", "iloveyou1234",
	"tide12345678", "changeme1234", "welcome12345", "letmein12345", "p@ssw0rd1234",
}

// Password enforces length over complexity (NIST 800-63B): long enough, not
// trivially guessable, not derived from the username.
func Password(field, username, pw string) *FieldError {
	n := utf8.RuneCountInString(pw)
	lower := strings.ToLower(pw)
	switch {
	case pw == "":
		return FieldKey(field, "v.passwordRequired")
	case n < PasswordMinLen:
		return FieldKey(field, "v.passwordTooShort", PasswordMinLen, n)
	case len(pw) > PasswordMaxBytes:
		return FieldKey(field, "v.passwordTooLong", PasswordMaxBytes)
	case strings.TrimSpace(pw) == "":
		return FieldKey(field, "v.passwordAllSpace")
	case strings.TrimSpace(pw) != pw:
		return FieldKey(field, "v.passwordEdgeSpace")
	case distinctRunes(pw) < 4:
		return FieldKey(field, "v.passwordFewDistinct")
	case isSequence(lower):
		return FieldKey(field, "v.passwordSequence")
	case slices.Contains(common, lower):
		return FieldKey(field, "v.passwordCommon")
	case username != "" && len(username) >= 3 && strings.Contains(lower, strings.ToLower(username)):
		return FieldKey(field, "v.passwordHasUsername")
	}
	return nil
}

func Confirm(field, pw, confirm string) *FieldError {
	switch {
	case confirm == "":
		return FieldKey(field, "v.confirmRequired")
	case pw != confirm:
		return FieldKey(field, "v.passwordMismatch")
	}
	return nil
}

func distinctRunes(s string) int {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen)
}

// isSequence catches abcdefghijkl / 123456789012 / 987654321098 style input.
func isSequence(s string) bool {
	rs := []rune(s)
	if len(rs) < 2 {
		return false
	}
	step := rs[1] - rs[0]
	if step != 1 && step != -1 {
		return false
	}
	for i := 2; i < len(rs); i++ {
		d := rs[i] - rs[i-1]
		// allow 9→0 / 0→9 wraparound in digit runs
		if d != step && (!unicode.IsDigit(rs[i]) || d != -9*step) {
			return false
		}
	}
	return true
}

// URLKind says whether a field is a base address or an arbitrary endpoint.
// Base addresses (API roots, OIDC issuers) are ones Tide appends paths to, so
// a query or fragment there would be silently dropped. Endpoint addresses
// (webhooks, registries) may carry both: a webhook token often lives in the
// query string.
type URLKind bool

const (
	BaseURL URLKind = true
	AnyURL  URLKind = false
)

// HTTPURL requires an absolute http(s) URL with a host and no credentials.
// The messages mirror web/src/lib/validation.ts.
func HTTPURL(field, s string, required bool, kind URLKind) *FieldError {
	if s == "" {
		if required {
			return FieldKey(field, "v.urlRequired")
		}
		return nil
	}
	u, err := url.Parse(s)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return FieldKey(field, "v.urlInvalid")
	case u.User != nil:
		return FieldKey(field, "v.urlCredentials")
	case kind == BaseURL && (u.RawQuery != "" || u.Fragment != ""):
		return FieldKey(field, "v.urlQuery")
	}
	return nil
}

// Required reports an empty field. what names it, as a label key, because the
// noun is part of the sentence and languages put it in different places.
func Required(field, s string, what i18n.Key) *FieldError {
	if strings.TrimSpace(s) == "" {
		return FieldKey(field, "v.required", what)
	}
	return nil
}

// First returns the first non-nil error.
func First(errs ...*FieldError) *FieldError {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// Package i18n renders the user-facing messages the API returns.
//
// Messages are produced deep in the domain and validation code, where the
// request's language is not known, so errors carry a Key and arguments and are
// rendered once at the edge (respond.Fail), which does know the language.
//
// Not everything here is translated: upstream error text is passed through
// verbatim (CONVENTIONS.md §4.2) and audit records stay English on purpose, because
// they are evidence and should read the same for everyone.
package i18n

import (
	"context"
	"fmt"
)

// Locale is a language Tide serves. Adding one means adding a catalog and
// making TestCatalogsAgree pass.
type Locale string

const (
	ZhCN Locale = "zh-CN"
	En   Locale = "en"

	// Default is used when the request asks for a language we do not serve.
	Default = ZhCN
)

// Key identifies a message. Keys are stable identifiers, not English text, so
// that changing the wording of a message is not a code change everywhere.
type Key string

var catalogs = map[Locale]map[Key]string{
	ZhCN: merge(zhCN, zhCNValidate, zhCNLabels, zhCNSettings, zhCNReleases, zhCNBind, zhCNRBAC, zhCNAccess, zhCNNotify, zhCNMisc, zhCNPlan, zhCNCI),
	En:   merge(en, enValidate, enLabels, enSettings, enReleases, enBind, enRBAC, enAccess, enNotify, enMisc, enPlan, enCI),
}

// merge folds the per-area catalogs into one table per locale. A key defined
// twice is a mistake, so it panics at init rather than letting one area's
// wording silently win.
func merge(parts ...map[Key]string) map[Key]string {
	out := map[Key]string{}
	for _, p := range parts {
		for k, v := range p {
			if _, dup := out[k]; dup {
				panic("i18n: duplicate key " + string(k))
			}
			out[k] = v
		}
	}
	return out
}

// T renders a message. A key missing from the requested locale falls back to
// the default one; a key missing from both returns the key itself, so a gap
// shows up as obviously wrong text rather than an empty message or a panic.
// TestCatalogsAgree is what keeps that from happening in the first place.
func T(l Locale, k Key, args ...any) string {
	format, ok := catalogs[l][k]
	if !ok {
		if format, ok = catalogs[Default][k]; !ok {
			return string(k)
		}
	}
	if len(args) == 0 {
		return format
	}
	// An argument may itself be a key (a field's label, say). Render it in the
	// same locale, so a sentence and the noun inside it never disagree.
	rendered := make([]any, len(args))
	for i, a := range args {
		if k, ok := a.(Key); ok {
			rendered[i] = T(l, k)
			continue
		}
		rendered[i] = a
	}
	return fmt.Sprintf(format, rendered...)
}

// Has reports whether the locale's own catalog defines the key.
func Has(l Locale, k Key) bool {
	_, ok := catalogs[l][k]
	return ok
}

// Locales lists what Tide serves, default first.
func Locales() []Locale { return []Locale{ZhCN, En} }

type ctxKey struct{}

// With carries the request's locale down to wherever a message is rendered.
func With(ctx context.Context, l Locale) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the context's locale, or the default outside a request.
func From(ctx context.Context) Locale {
	if l, ok := ctx.Value(ctxKey{}).(Locale); ok {
		return l
	}
	return Default
}

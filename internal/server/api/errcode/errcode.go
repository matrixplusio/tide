// Package errcode is the canonical table of API result codes (CONVENTIONS.md §4.3,
// docs/api.md). Mirror every change in web/src/lib/errcode.ts.
//
//	0          success
//	1000–1999  generic
//	2000–2999  auth / accounts / setup
//	3000–3999  releases
//	4000–4999  upstreams / service catalog
//	5000–5999  settings
//	9000–9999  reserved
//
// Codes are a contract: never renumber or reuse one. Never invent messages at
// call sites when the default fits.
package errcode

import (
	"errors"
	"fmt"
	"net/http"

	"tide/internal/i18n"
)

type Code int

const OK Code = 0

// 1000–1999 generic
const (
	BadRequest       Code = 1001
	Unauthorized     Code = 1002
	Forbidden        Code = 1003
	NotFound         Code = 1004
	Conflict         Code = 1005
	RateLimited      Code = 1006
	ValidationFailed Code = 1007
	UnsupportedMedia Code = 1008
	CrossSiteBlocked Code = 1009
	Internal         Code = 1099
)

// 2000–2999 auth / accounts / setup
const (
	InvalidCredentials Code = 2001
	LoginThrottled     Code = 2002
	SetupRequired      Code = 2003
	SetupTokenInvalid  Code = 2004
	AlreadyInitialized Code = 2005
	SetupTokenRequired Code = 2006
	UserExists         Code = 2010
	UserNotFound       Code = 2011
	CannotModifySelf   Code = 2012
	LastAdminProtected Code = 2013
	SSOPasswordManaged Code = 2014
	CaptchaRequired    Code = 2015
	CaptchaInvalid     Code = 2016
	SSOProfileManaged  Code = 2017
	LocalLoginAdmins   Code = 2018
	RoleNotFound       Code = 2020
	RoleBuiltin        Code = 2021
	RoleExists         Code = 2022
	BindingExists      Code = 2023
	BindingNotFound    Code = 2024
	GroupNotFound      Code = 2025
	GroupExists        Code = 2026
	CITokenInvalid     Code = 2030
)

// 3000–3999 releases
const (
	ReleaseNotFound     Code = 3001
	TargetBusy          Code = 3002
	ConfirmTooEarly     Code = 3003
	ConfirmExpired      Code = 3004
	InvalidTransition   Code = 3005
	DigestMismatch      Code = 3006
	ArtifactUnavailable Code = 3007
	EnvironmentFrozen   Code = 3008
	OrderBlocked        Code = 3009
	ThresholdBlocked    Code = 3010
	NotApprover         Code = 3011
	SelfApproval        Code = 3012
	ApprovalExpired     Code = 3013
	AlreadyDecided      Code = 3014
	CIDisabled          Code = 3020
)

// 4000–4999 upstreams / service catalog
const (
	NoUpstreams         Code = 4001
	UpstreamError       Code = 4002
	UpstreamTimeout     Code = 4003
	UpstreamCheckFailed Code = 4004
	ServiceNotFound     Code = 4005
	ServiceNotDeployed  Code = 4006
	NotManagedByKargo   Code = 4007
	ServiceConflict     Code = 4008
)

// 5000–5999 settings
const (
	UnknownSettingsSection Code = 5001
	NotifyTestFailed       Code = 5002
)

type meta struct {
	status int
	key    i18n.Key
}

var table = map[Code]meta{
	OK:               {http.StatusOK, i18n.Key("err.ok")},
	BadRequest:       {http.StatusBadRequest, i18n.Key("err.badRequest")},
	Unauthorized:     {http.StatusUnauthorized, i18n.Key("err.unauthorized")},
	Forbidden:        {http.StatusForbidden, i18n.Key("err.forbidden")},
	NotFound:         {http.StatusNotFound, i18n.Key("err.notFound")},
	Conflict:         {http.StatusConflict, i18n.Key("err.conflict")},
	RateLimited:      {http.StatusTooManyRequests, i18n.Key("err.rateLimited")},
	ValidationFailed: {http.StatusBadRequest, i18n.Key("err.validationFailed")},
	UnsupportedMedia: {http.StatusUnsupportedMediaType, i18n.Key("err.unsupportedMedia")},
	CrossSiteBlocked: {http.StatusForbidden, i18n.Key("err.crossSiteBlocked")},
	Internal:         {http.StatusInternalServerError, i18n.Key("err.internal")},

	InvalidCredentials: {http.StatusUnauthorized, i18n.Key("err.invalidCredentials")},
	LoginThrottled:     {http.StatusTooManyRequests, i18n.Key("err.loginThrottled")},
	SetupRequired:      {http.StatusConflict, i18n.Key("err.setupRequired")},
	SetupTokenInvalid:  {http.StatusForbidden, i18n.Key("err.setupTokenInvalid")},
	AlreadyInitialized: {http.StatusConflict, i18n.Key("err.alreadyInitialized")},
	SetupTokenRequired: {http.StatusForbidden, i18n.Key("err.setupTokenRequired")},
	UserExists:         {http.StatusConflict, i18n.Key("err.userExists")},
	UserNotFound:       {http.StatusNotFound, i18n.Key("err.userNotFound")},
	CannotModifySelf:   {http.StatusBadRequest, i18n.Key("err.cannotModifySelf")},
	LastAdminProtected: {http.StatusBadRequest, i18n.Key("err.lastAdminProtected")},
	SSOPasswordManaged: {http.StatusBadRequest, i18n.Key("err.ssoPasswordManaged")},
	CaptchaRequired:    {http.StatusBadRequest, i18n.Key("err.captchaRequired")},
	CaptchaInvalid:     {http.StatusBadRequest, i18n.Key("err.captchaInvalid")},
	SSOProfileManaged:  {http.StatusBadRequest, i18n.Key("err.ssoProfileManaged")},
	LocalLoginAdmins:   {http.StatusForbidden, i18n.Key("err.localLoginAdmins")},
	RoleNotFound:       {http.StatusNotFound, i18n.Key("err.roleNotFound")},
	RoleBuiltin:        {http.StatusBadRequest, i18n.Key("err.roleBuiltin")},
	RoleExists:         {http.StatusConflict, i18n.Key("err.roleExists")},
	BindingExists:      {http.StatusConflict, i18n.Key("err.bindingExists")},
	BindingNotFound:    {http.StatusNotFound, i18n.Key("err.bindingNotFound")},
	GroupNotFound:      {http.StatusNotFound, i18n.Key("err.groupNotFound")},
	GroupExists:        {http.StatusConflict, i18n.Key("err.groupExists")},
	CITokenInvalid:     {http.StatusUnauthorized, i18n.Key("err.ciTokenInvalid")},

	ReleaseNotFound:     {http.StatusNotFound, i18n.Key("err.releaseNotFound")},
	TargetBusy:          {http.StatusConflict, i18n.Key("err.targetBusy")},
	ConfirmTooEarly:     {http.StatusTooEarly, i18n.Key("err.confirmTooEarly")},
	ConfirmExpired:      {http.StatusGone, i18n.Key("err.confirmExpired")},
	InvalidTransition:   {http.StatusConflict, i18n.Key("err.invalidTransition")},
	DigestMismatch:      {http.StatusConflict, i18n.Key("err.digestMismatch")},
	ArtifactUnavailable: {http.StatusBadRequest, i18n.Key("err.artifactUnavailable")},
	EnvironmentFrozen:   {http.StatusLocked, i18n.Key("err.environmentFrozen")},
	OrderBlocked:        {http.StatusConflict, i18n.Key("err.orderBlocked")},
	ThresholdBlocked:    {http.StatusUnprocessableEntity, i18n.Key("err.thresholdBlocked")},
	NotApprover:         {http.StatusForbidden, i18n.Key("err.notApprover")},
	SelfApproval:        {http.StatusForbidden, i18n.Key("err.selfApproval")},
	ApprovalExpired:     {http.StatusConflict, i18n.Key("err.approvalExpired")},
	AlreadyDecided:      {http.StatusConflict, i18n.Key("err.alreadyDecided")},
	CIDisabled:          {http.StatusForbidden, i18n.Key("err.ciDisabled")},

	NoUpstreams:         {http.StatusServiceUnavailable, i18n.Key("err.noUpstreams")},
	UpstreamError:       {http.StatusBadGateway, i18n.Key("err.upstreamError")},
	UpstreamTimeout:     {http.StatusGatewayTimeout, i18n.Key("err.upstreamTimeout")},
	UpstreamCheckFailed: {http.StatusUnprocessableEntity, i18n.Key("err.upstreamCheckFailed")},
	ServiceNotFound:     {http.StatusNotFound, i18n.Key("err.serviceNotFound")},
	ServiceNotDeployed:  {http.StatusNotFound, i18n.Key("err.serviceNotDeployed")},
	NotManagedByKargo:   {http.StatusBadRequest, i18n.Key("err.notManagedByKargo")},
	ServiceConflict:     {http.StatusConflict, i18n.Key("err.serviceConflict")},

	UnknownSettingsSection: {http.StatusNotFound, i18n.Key("err.unknownSettingsSection")},
	NotifyTestFailed:       {http.StatusBadGateway, i18n.Key("err.notifyTestFailed")},
}

func (c Code) HTTPStatus() int {
	if m, ok := table[c]; ok {
		return m.status
	}
	return http.StatusInternalServerError
}

// DefaultMsg renders the code's message in l. Callers that have no request
// context (domain error mapping) use i18n.Default and are re-rendered by
// respond.Fail when the error carries no explicit message.
func (c Code) DefaultMsg(l i18n.Locale) string { return i18n.T(l, table[c].key) }

// Key is the catalog key for the code's default message.
func (c Code) Key() i18n.Key { return table[c].key }

// Known reports whether c is in the table (used by tests).
func Known(c Code) bool { _, ok := table[c]; return ok }

// All returns the table for documentation tests.
func All() map[Code]int {
	out := map[Code]int{}
	for c, m := range table {
		out[c] = m.status
	}
	return out
}

// FieldError is one entry of data.fields on ValidationFailed.
type FieldError struct {
	Field string `json:"field"`
	Msg   string `json:"msg"`
	// Key renders Msg at the edge when the message is not already words.
	// Field errors are built deep in the validation code, which has no
	// request and so cannot know the language.
	Key  i18n.Key `json:"-"`
	Args []any    `json:"-"`
}

// Error is an API error: a code, a user-safe message, and optional data.
type Error struct {
	Code Code
	Msg  string
	// Key and Args render Msg at the edge, for errors built where the
	// request's language is not known.
	Key    i18n.Key
	Args   []any
	Fields []FieldError // ValidationFailed only
	Data   any          // only for codes documented to carry data (e.g. UpstreamCheckFailed)
	cause  error
}

func (e *Error) Error() string {
	msg := e.Msg
	if msg == "" {
		msg = e.Code.DefaultMsg(i18n.Default)
	}
	if e.cause != nil {
		return fmt.Sprintf("%d %s: %v", e.Code, msg, e.cause)
	}
	return fmt.Sprintf("%d %s", e.Code, msg)
}

func (e *Error) Unwrap() error { return e.cause }

// Text is the user-facing message in l: the explicit one when the error
// carries words of its own (an upstream's text), otherwise the code's default.
// Read this rather than Msg, which is empty whenever the default applies.
func (e *Error) Text(l i18n.Locale) string {
	switch {
	case e.Msg != "":
		return e.Msg
	case e.Key != "":
		return i18n.T(l, e.Key, e.Args...)
	}
	return e.Code.DefaultMsg(l)
}

// New builds an error; an empty msg uses the default for code.
func New(code Code, msg string, args ...any) *Error {
	// An empty msg stays empty: respond.Fail fills in the code's default in
	// the request's language. A msg given here is already user-facing text
	// (an upstream's own words, for instance) and is passed through.
	if msg != "" && len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	return &Error{Code: code, Msg: msg}
}

// NewKey builds an error whose message is a catalog key, rendered at the edge.
func NewKey(code Code, key i18n.Key, args ...any) *Error {
	return &Error{Code: code, Key: key, Args: args}
}

// WrapKey is Wrap for a message that is a catalog key.
func WrapKey(code Code, cause error, key i18n.Key, args ...any) *Error {
	e := NewKey(code, key, args...)
	e.cause = cause
	return e
}

// Wrap attaches the underlying cause for logs; clients only see msg.
func Wrap(code Code, cause error, msg string) *Error {
	e := New(code, msg)
	e.cause = cause
	return e
}

func (e *Error) WithData(d any) *Error {
	e.Data = d
	return e
}

// Invalid is a ValidationFailed error for the given fields.
func Invalid(fields ...FieldError) *Error {
	return &Error{Code: ValidationFailed, Fields: fields}
}

// Field is shorthand for a single-field validation error.
func Field(field, msg string, args ...any) *Error {
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	return Invalid(FieldError{Field: field, Msg: msg})
}

// Mapper converts domain errors; it returns nil for errors it does not know.
type Mapper func(error) *Error

var mappers []Mapper

// Register adds a domain error mapper. Call from package init only.
func Register(m Mapper) { mappers = append(mappers, m) }

// From resolves any error into an API error. Unknown errors become Internal.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	for _, m := range mappers {
		if e := m(err); e != nil {
			return e
		}
	}
	return Wrap(Internal, err, "")
}

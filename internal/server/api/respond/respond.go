// Package respond emits the {code, msg, data} envelope (CONVENTIONS.md §4.2).
// Handlers under /api/v1 must use these helpers; direct c.JSON is forbidden.
package respond

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tide/internal/i18n"
	"tide/internal/server/api/errcode"
)

// StatusClientClosedRequest is logged for requests the client abandoned.
const StatusClientClosedRequest = 499

type envelope struct {
	Code errcode.Code `json:"code"`
	Msg  string       `json:"msg"`
	Data any          `json:"data"`
}

type fieldsData struct {
	Fields []errcode.FieldError `json:"fields"`
}

// PageData is the data shape for paginated lists.
type PageData[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

func OK(c *gin.Context, data any) {
	c.Header("Cache-Control", "no-store")
	c.JSON(200, envelope{Code: errcode.OK, Msg: "ok", Data: data})
}

func Page[T any](c *gin.Context, items []T, total int64, page, pageSize int) {
	if items == nil {
		items = []T{}
	}
	OK(c, PageData[T]{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// Fail resolves err through errcode.From and aborts with the error envelope.
// Internal errors are logged with the request id and never shown verbatim.
func Fail(c *gin.Context, err error) {
	// The client went away (navigation, closed tab): nobody reads a response
	// and it is not a server fault. 499 mirrors nginx's "client closed request".
	if errors.Is(err, context.Canceled) && c.Request.Context().Err() != nil {
		c.AbortWithStatus(StatusClientClosedRequest)
		return
	}
	e := errcode.From(err)
	loc0 := i18n.From(c.Request.Context())
	var data any
	switch {
	case e.Code == errcode.ValidationFailed:
		data = fieldsData{Fields: localiseFields(loc0, e.Fields)}
	case e.Data != nil:
		data = e.Data
	}
	// The one place a message becomes words: everything upstream of here
	// carries a code (and, from phase 2 on, a key), never a rendered sentence.
	loc := loc0
	msg := e.Text(loc)
	if e.Code == errcode.Internal {
		rid := c.GetString("request_id")
		zap.L().Error("internal error", zap.String("request_id", rid), zap.String("path", c.Request.URL.Path),
			zap.String("user_id", c.GetString("user_id")), zap.Error(errors.Unwrap(e)))
		msg = i18n.T(loc, "err.internalWithRequestID", rid)
	}
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(e.Code.HTTPStatus(), envelope{Code: e.Code, Msg: msg, Data: data})
}

// FailCode is shorthand for Fail(c, errcode.New(code, msg)).
func FailCode(c *gin.Context, code errcode.Code, msg string, args ...any) {
	Fail(c, errcode.New(code, msg, args...))
}

// localiseFields renders the field errors that carry a key; ones that already
// hold words (an upstream's own text) are left alone.
func localiseFields(l i18n.Locale, fields []errcode.FieldError) []errcode.FieldError {
	out := make([]errcode.FieldError, len(fields))
	for i, f := range fields {
		if f.Msg == "" && f.Key != "" {
			f.Msg = i18n.T(l, f.Key, f.Args...)
		}
		out[i] = f
	}
	return out
}

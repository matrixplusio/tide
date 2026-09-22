// Package middleware holds cross-cutting HTTP concerns that do not depend on
// Tide's domain services.
package middleware

import (
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/text/language"

	"tide/internal/i18n"
	"tide/internal/metrics"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
)

const RequestIDHeader = "X-Request-ID"

// RequestID reuses a well-formed incoming id (≤ 64 visible ASCII) or makes one.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(RequestIDHeader)
		if rid == "" || len(rid) > 64 || strings.ContainsFunc(rid, func(r rune) bool { return r < 0x21 || r > 0x7e }) {
			rid = uuid.NewString()
		}
		c.Set("request_id", rid)
		c.Header(RequestIDHeader, rid)
		c.Next()
	}
}

// Recover turns a panic into a logged Internal error envelope.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				zap.L().Error("panic recovered", zap.Any("panic", v), zap.String("stack", string(debug.Stack())),
					zap.String("path", c.Request.URL.Path), zap.String("request_id", c.GetString("request_id")))
				respond.FailCode(c, errcode.Internal, "")
			}
		}()
		c.Next()
	}
}

// AccessLog writes one line per request. API requests log at info (4xx warn,
// 5xx error); static assets and probes at debug. 401 and setup-pending 409
// are normal flow and stay at info.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		p := c.Request.URL.Path
		status := c.Writer.Status()
		metrics.HTTP(c.FullPath(), c.Request.Method, status, time.Since(start))
		level := zap.InfoLevel
		switch {
		case status == respond.StatusClientClosedRequest:
			level = zap.InfoLevel
		case status >= 500:
			level = zap.ErrorLevel
		case status >= 400 && status != http.StatusUnauthorized && status != http.StatusConflict:
			level = zap.WarnLevel
		case !strings.HasPrefix(p, "/api/"):
			level = zap.DebugLevel
		}
		if ce := zap.L().Check(level, "http"); ce != nil {
			fields := []zap.Field{
				zap.String("request_id", c.GetString("request_id")),
				zap.String("method", c.Request.Method),
				zap.String("path", p),
				zap.Int("status", status),
				zap.Int("bytes", c.Writer.Size()),
				zap.Int64("duration_ms", time.Since(start).Milliseconds()),
				zap.String("client_ip", c.ClientIP()),
			}
			if uid := c.GetString("user_id"); uid != "" {
				fields = append(fields, zap.String("user_id", uid))
			}
			ce.Write(fields...)
		}
	}
}

// SecurityHeaders sets CSP and hardening headers. dev relaxes CSP for Vite HMR.
func SecurityHeaders(dev bool) gin.HandlerFunc {
	csp := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
		"connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	if dev {
		csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
			"connect-src 'self' ws: wss:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
	}
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		c.Next()
	}
}

// MaxBodyBytes bounds request bodies.
const MaxBodyBytes = 1 << 20

// CSRF guards cookie-authenticated writes: unsafe methods must send JSON (a
// plain HTML form cannot) and, when the browser sends Origin or Referer, it
// must match the host. It also caps the body size.
func CSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)
		if c.Request.ContentLength != 0 && !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			respond.FailCode(c, errcode.UnsupportedMedia, "")
			return
		}
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = c.GetHeader("Referer")
		}
		if origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, c.Request.Host) {
				zap.L().Warn("cross-site write blocked", zap.String("origin", origin), zap.String("host", c.Request.Host),
					zap.String("path", c.Request.URL.Path), zap.String("request_id", c.GetString("request_id")))
				respond.FailCode(c, errcode.CrossSiteBlocked, "")
				return
			}
		}
		c.Next()
	}
}

// Locale resolves the request's language from Accept-Language and carries it
// down the context, so messages produced deep in the domain can be rendered in
// it at the edge. Anything we do not serve falls back to i18n.Default.
func Locale() gin.HandlerFunc {
	tags := make([]language.Tag, 0, len(i18n.Locales()))
	for _, l := range i18n.Locales() {
		tags = append(tags, language.Make(string(l)))
	}
	matcher := language.NewMatcher(tags)
	return func(c *gin.Context) {
		loc := i18n.Default
		if h := c.GetHeader("Accept-Language"); h != "" {
			if _, idx, conf := matcher.Match(parseAcceptLanguage(h)...); conf != language.No {
				loc = i18n.Locales()[idx]
			}
		}
		c.Request = c.Request.WithContext(i18n.With(c.Request.Context(), loc))
		c.Next()
	}
}

// parseAcceptLanguage is tolerant: a malformed header is a client's problem,
// not a reason to fail the request.
func parseAcceptLanguage(h string) []language.Tag {
	tags, _, err := language.ParseAcceptLanguage(h)
	if err != nil {
		return nil
	}
	return tags
}

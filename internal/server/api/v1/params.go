package v1

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"tide/internal/access"
	"tide/internal/i18n"
	"tide/internal/server/api/errcode"
)

// Path and query values are untrusted input like any body: each has a
// whitelist rule, and anything else is a ValidationFailed error.

var (
	reDNSName   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	reDNSLabel  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	reEnv       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	reReleaseID = regexp.MustCompile(`^REL-[0-9]{8}-[0-9]{3,6}$`)
	reSessionID = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reAction    = regexp.MustCompile(`^[a-z.]{0,64}$`)
	reSection   = regexp.MustCompile(`^[a-z]{1,32}$`)
	reFreight   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	reDigest    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// IANA zone names: "Asia/Shanghai", "UTC", "America/Argentina/Salta".
	reTimezone = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+_-]*(/[A-Za-z0-9+_-]+){0,2}$`)
)

type params struct {
	c      *gin.Context
	fields []errcode.FieldError
}

func newParams(c *gin.Context) *params { return &params{c: c} }

// bad records a rejected parameter by catalog key; it is rendered at the edge
// like every other field error.
func (p *params) bad(field string, key i18n.Key, args ...any) {
	p.fields = append(p.fields, errcode.FieldError{Field: field, Key: key, Args: args})
}

// err returns the collected validation error, or nil.
func (p *params) err() error {
	if len(p.fields) == 0 {
		return nil
	}
	return errcode.Invalid(p.fields...)
}

func (p *params) path(name string, re *regexp.Regexp, what i18n.Key) string {
	v := p.c.Param(name)
	if !re.MatchString(v) {
		p.bad(name, "p.format", what)
	}
	return v
}

// hasPath reports that a route segment is present at all. Routes share
// handlers (/services/:service and /services/:service/envs/:env), so a
// handler has to ask before validating.
func (p *params) hasPath(name string) bool { return p.c.Param(name) != "" }

func (p *params) service() string   { return p.path("service", reDNSName, "label.serviceName") }
func (p *params) env() string       { return p.path("env", reEnv, "label.envName") }
func (p *params) pod() string       { return p.path("pod", reDNSName, "label.podName") }
func (p *params) releaseID() string { return p.path("id", reReleaseID, "label.releaseId") }
func (p *params) session() string   { return p.path("session", reSessionID, "label.sessionId") }
func (p *params) group() string     { return p.path("group", access.GroupNameRe, "label.groupName") }
func (p *params) role() string      { return p.path("role", access.RoleIDRe, "label.roleId") }

// id reads a positive integer path parameter such as :user or :binding.
func (p *params) id(name string, what i18n.Key) int64 {
	v := p.c.Param(name)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || len(v) > 18 {
		p.bad(name, "p.format", what)
		return 0
	}
	return n
}

// text reads an optional free-text query value with a length cap.
func (p *params) text(name string, limit int) string {
	v := strings.TrimSpace(p.c.Query(name))
	if utf8.RuneCountInString(v) > limit {
		p.bad(name, "p.tooLong")
	}
	return v
}

func (p *params) match(name string, re *regexp.Regexp, what i18n.Key) string {
	v := strings.TrimSpace(p.c.Query(name))
	if v != "" && !re.MatchString(v) {
		p.bad(name, "p.format", what)
	}
	return v
}

func (p *params) intRange(name string, def, lo, hi int) int {
	raw := p.c.Query(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		p.bad(name, "p.intRange", name, lo, hi)
		return def
	}
	return n
}

func (p *params) boolean(name string) bool {
	switch p.c.Query(name) {
	case "", "false", "0":
		return false
	case "true", "1":
		return true
	}
	p.bad(name, "p.bool", name)
	return false
}

// booleanOr is boolean for a flag that is on unless switched off.
func (p *params) booleanOr(name string, def bool) bool {
	switch p.c.Query(name) {
	case "":
		return def
	case "false", "0":
		return false
	case "true", "1":
		return true
	}
	p.bad(name, "p.bool", name)
	return def
}

func (p *params) time(name string) *time.Time {
	raw := p.c.Query(name)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		p.bad(name, "p.time")
		return nil
	}
	return &t
}

// location resolves an IANA time zone name. Grouping releases by weekday and
// hour only means something in the viewer's own zone. The name is looked up
// in the tzdata the binary carries, so an unknown one is a field error rather
// than a silent fallback to UTC.
func (p *params) location(name string) *time.Location {
	raw := p.c.Query(name)
	if raw == "" {
		return time.UTC
	}
	if len(raw) > 64 || !reTimezone.MatchString(raw) {
		p.bad(name, "p.timezone")
		return time.UTC
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		p.bad(name, "p.timezone")
		return time.UTC
	}
	return loc
}

func (p *params) uuid(name string) string {
	v := p.c.Query(name)
	if _, err := uuid.Parse(v); err != nil {
		p.bad(name, "p.uuid", name)
	}
	return v
}

func (p *params) container() string {
	v := p.c.Query("container")
	if v != "" && !reDNSLabel.MatchString(v) {
		p.bad("container", "p.format", i18n.Key("label.containerName"))
	}
	return v
}

// enumList parses a comma-separated list restricted to allowed values.
func (p *params) enumList(name string, allowed []string) []string {
	raw := p.c.Query(name)
	if raw == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(raw, ",") {
		if !slices.Contains(allowed, v) {
			p.bad(name, "p.badValue", name, v)
			continue
		}
		out = append(out, v)
	}
	return out
}

// page returns page and page_size per CONVENTIONS.md §4.5.
func (p *params) page() (int, int) {
	return p.intRange("page", 1, 1, 100000), p.intRange("page_size", 20, 1, 100)
}

// enum reads an optional query value restricted to allowed values.
func (p *params) enum(name string, allowed []string) string {
	v := p.c.Query(name)
	if v != "" && !slices.Contains(allowed, v) {
		p.bad(name, "p.oneof", name, strings.Join(allowed, " / "))
	}
	return v
}

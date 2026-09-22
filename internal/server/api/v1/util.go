package v1

import (
	"strconv"
	"time"

	"tide/internal/audit"
)

func itoa(i int) string { return strconv.Itoa(i) }

func auditFilter(jira, service, env, actor, action string, since, until *time.Time) audit.Filter {
	return audit.Filter{Jira: jira, Service: service, Env: env, Actor: actor, Action: action, Since: since, Until: until}
}

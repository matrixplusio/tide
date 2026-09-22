package pg

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"tide/internal/insights"
)

// Insights aggregates the release history. Every query here is read-only and
// scoped the same way the release list is: a caller that may only see some
// services passes them in, and an empty (non-nil) list matches nothing.
type Insights struct{ db *gorm.DB }

// InsightsFilter narrows a report. Services nil means "no restriction";
// Services empty means the viewer may see nothing.
type InsightsFilter struct {
	Range    insights.Range
	Services []string
	Env      string
	// Location is the time zone weekday/hour grouping and day boundaries use.
	Location *time.Location
	// TopN caps every "top" list.
	TopN int
}

func (f InsightsFilter) topN() int {
	if f.TopN <= 0 {
		return 10
	}
	return f.TopN
}

func (f InsightsFilter) tz() string {
	if f.Location == nil {
		return "UTC"
	}
	return f.Location.String()
}

// scope builds the WHERE fragment every query shares. The releases table is
// aliased r; callers that also need items join them themselves.
func (f InsightsFilter) scope(next func(any) string) string {
	w := "r.created_at >= " + next(f.Range.From) + " AND r.created_at < " + next(f.Range.To)
	if f.Env != "" {
		w += " AND r.env = " + next(f.Env)
	}
	if f.Services != nil {
		// A release is in scope when it touches a visible service. An empty
		// list makes this false for every row, which is what "may see
		// nothing" should mean.
		w += " AND EXISTS (SELECT 1 FROM release_items i WHERE i.release_id = r.id" +
			" AND i.payload->>'service' = ANY(" + next(f.Services) + "))"
	}
	return w
}

// argf collects bind parameters in order.
type argf struct{ args []any }

func (a *argf) next(v any) string {
	a.args = append(a.args, v)
	return fmt.Sprintf("$%d", len(a.args))
}

// Report runs every aggregate for one filter. The queries are independent;
// they run in sequence because a report is asked for rarely and a single
// connection keeps the numbers consistent with each other.
func (s *Insights) Report(ctx context.Context, f InsightsFilter, withPeople bool) (*insights.Report, error) {
	rep := &insights.Report{Range: f.Range}
	steps := []struct {
		name string
		fn   func(context.Context, InsightsFilter, *insights.Report) error
	}{
		{"totals", s.totals},
		{"dwell", s.confirmDwell},
		{"anomalies", s.anomalies},
		{"approvals", s.approvals},
		{"sources", s.sources},
		{"services", s.services},
		{"environments", s.environments},
		{"kinds", s.kinds},
		{"weekly", s.weekly},
		{"rollbacks", s.rollbacks},
		{"repeats", s.repeats},
		{"jira", s.jiraReuse},
	}
	for _, st := range steps {
		if err := st.fn(ctx, f, rep); err != nil {
			return nil, fmt.Errorf("insights %s: %w", st.name, err)
		}
	}
	if withPeople {
		if err := s.people(ctx, f, rep); err != nil {
			return nil, fmt.Errorf("insights people: %w", err)
		}
	}
	return rep, nil
}

func (s *Insights) totals(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT
		count(*) AS releases,
		count(*) FILTER (WHERE r.status = 'succeeded') AS succeeded,
		count(*) FILTER (WHERE r.status = 'failed')    AS failed,
		count(*) FILTER (WHERE r.status = 'cancelled') AS cancelled,
		count(*) FILTER (WHERE r.status = 'rejected')  AS rejected,
		count(*) FILTER (WHERE r.status IN ('draft','confirming','approving','executing')) AS in_flight
	FROM releases r WHERE ` + f.scope(a.next)
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Totals).Error
}

// confirmDwell buckets submitted_at → confirmed_at. The countdown blocks the
// button for the first N seconds, so a spike in the bucket right after N is
// the interesting signal: the sheet was waited out, not read.
func (s *Insights) confirmDwell(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT
		count(*) FILTER (WHERE d <  15) AS b15,
		count(*) FILTER (WHERE d >= 15 AND d < 30)  AS b30,
		count(*) FILTER (WHERE d >= 30 AND d < 60)  AS b60,
		count(*) FILTER (WHERE d >= 60 AND d < 300) AS b300,
		count(*) FILTER (WHERE d >= 300) AS rest
	FROM (
		SELECT EXTRACT(EPOCH FROM (r.confirmed_at - r.submitted_at)) AS d
		FROM releases r
		WHERE ` + f.scope(a.next) + `
		  AND r.confirmed_at IS NOT NULL AND r.submitted_at IS NOT NULL
		  AND r.source = 'ui'
	) t`
	var row struct{ B15, B30, B60, B300, Rest int64 }
	if err := s.db.WithContext(ctx).Raw(q, a.args...).Scan(&row).Error; err != nil {
		return err
	}
	rep.Process.ConfirmDwell = []insights.Bucket{
		{Label: "<15s", Upper: 15, Count: row.B15},
		{Label: "15–30s", Upper: 30, Count: row.B30},
		{Label: "30–60s", Upper: 60, Count: row.B60},
		{Label: "1–5min", Upper: 300, Count: row.B300},
		{Label: "≥5min", Upper: 0, Count: row.Rest},
	}
	return nil
}

// anomalies: how often the confirmation sheet flagged something and the
// release went ahead regardless.
func (s *Insights) anomalies(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT
		count(*) AS with_anomaly,
		count(*) FILTER (WHERE r.confirmed_at IS NOT NULL) AS went_ahead
	FROM releases r
	WHERE ` + f.scope(a.next) + `
	  AND EXISTS (
		SELECT 1 FROM release_items i
		WHERE i.release_id = r.id
		  AND jsonb_array_length(COALESCE(i.payload->'anomalies', '[]'::jsonb)) > 0)`
	// Scanned into a flat struct: gorm treats a slice field on the
	// destination as a relation and refuses the scan.
	var counts struct{ WithAnomaly, WentAhead int64 }
	if err := s.db.WithContext(ctx).Raw(q, a.args...).Scan(&counts).Error; err != nil {
		return err
	}
	rep.Process.Anomalies.WithAnomaly = counts.WithAnomaly
	rep.Process.Anomalies.WentAhead = counts.WentAhead

	b := &argf{}
	q = `SELECT an->>'code' AS code, count(*) AS count
	FROM releases r
	JOIN release_items i ON i.release_id = r.id
	CROSS JOIN LATERAL jsonb_array_elements(COALESCE(i.payload->'anomalies', '[]'::jsonb)) AS an
	WHERE ` + f.scope(b.next) + ` AND an->>'code' IS NOT NULL
	GROUP BY 1 ORDER BY count DESC, code`
	rep.Process.Anomalies.ByCode = []insights.CodeCount{}
	return s.db.WithContext(ctx).Raw(q, b.args...).Scan(&rep.Process.Anomalies.ByCode).Error
}

func (s *Insights) approvals(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	// A release that entered approval has an approval_rule. Decisions live in
	// release_approvals; "expired" is one nobody decided before the deadline,
	// which the executor turns into a cancellation.
	q := `SELECT
		count(*) AS requested,
		count(*) FILTER (WHERE d.decision = 'approve') AS approved,
		count(*) FILTER (WHERE d.decision = 'reject')  AS rejected,
		count(*) FILTER (WHERE d.decision IS NULL AND r.status = 'cancelled') AS expired,
		COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY d.wait), 0) AS median_seconds,
		COALESCE(percentile_cont(0.9) WITHIN GROUP (ORDER BY d.wait), 0) AS p90_seconds
	FROM releases r
	LEFT JOIN LATERAL (
		SELECT ra.decision, EXTRACT(EPOCH FROM (ra.decided_at - r.confirmed_at)) AS wait
		FROM release_approvals ra
		WHERE ra.release_id = r.id
		ORDER BY ra.decided_at DESC LIMIT 1
	) d ON TRUE
	WHERE ` + f.scope(a.next) + ` AND r.approval_rule IS NOT NULL`
	if err := s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Process.Approvals).Error; err != nil {
		return err
	}

	b := &argf{}
	// Releases a person both created and confirmed. Not a fault by itself —
	// most environments have no approval rule — but it counts how often one
	// release passed through a single pair of hands.
	q = `SELECT count(*) FROM releases r
	WHERE ` + f.scope(b.next) + `
	  AND r.confirmed_by IS NOT NULL AND r.confirmed_by = r.created_by`
	return s.db.WithContext(ctx).Raw(q, b.args...).Scan(&rep.Process.Approvals.SelfConfirmed).Error
}

func (s *Insights) sources(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT r.source,
		count(*) AS total,
		count(*) FILTER (WHERE r.status = 'succeeded') AS succeeded,
		count(*) FILTER (WHERE r.status = 'failed')    AS failed
	FROM releases r WHERE ` + f.scope(a.next) + `
	GROUP BY 1 ORDER BY 1`
	rep.Process.Sources = []insights.SourceStats{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Process.Sources).Error
}

func (s *Insights) services(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	// Counted per item, so a batch release of eight services counts once for
	// each of them — that is what "which services move" means here.
	q := `SELECT i.payload->>'service' AS service,
		count(*) AS total,
		count(*) FILTER (WHERE r.status = 'succeeded') AS succeeded,
		count(*) FILTER (WHERE r.status = 'failed')    AS failed
	FROM releases r
	JOIN release_items i ON i.release_id = r.id
	WHERE ` + f.scope(a.next) + ` AND i.payload->>'service' IS NOT NULL
	GROUP BY 1 ORDER BY total DESC, service
	LIMIT ` + a.next(f.topN())
	rep.Activity.Services = []insights.ServiceStats{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Activity.Services).Error
}

func (s *Insights) environments(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT r.env,
		count(*) AS total,
		count(*) FILTER (WHERE r.status = 'succeeded') AS succeeded,
		count(*) FILTER (WHERE r.status = 'failed')    AS failed
	FROM releases r WHERE ` + f.scope(a.next) + `
	GROUP BY 1 ORDER BY total DESC, env`
	rep.Activity.Environments = []insights.EnvStats{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Activity.Environments).Error
}

func (s *Insights) kinds(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT i.kind,
		count(*) AS total,
		count(*) FILTER (WHERE r.status = 'succeeded') AS succeeded,
		count(*) FILTER (WHERE r.status = 'failed')    AS failed
	FROM releases r
	JOIN release_items i ON i.release_id = r.id
	WHERE ` + f.scope(a.next) + `
	GROUP BY 1 ORDER BY total DESC, kind`
	rep.Activity.Kinds = []insights.KindStats{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Activity.Kinds).Error
}

// weekly groups by weekday and hour in the viewer's zone, which is the only
// way "a release at 2am on Saturday" reads as unusual rather than as UTC noon.
func (s *Insights) weekly(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	tz := a.next(f.tz())
	q := `SELECT
		EXTRACT(DOW  FROM r.created_at AT TIME ZONE ` + tz + `)::int AS weekday,
		EXTRACT(HOUR FROM r.created_at AT TIME ZONE ` + tz + `)::int AS hour,
		count(*) AS count
	FROM releases r WHERE ` + f.scope(a.next) + `
	GROUP BY 1, 2 ORDER BY 1, 2`
	rep.Activity.Weekly = []insights.Slot{}
	if err := s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Activity.Weekly).Error; err != nil {
		return err
	}
	for _, sl := range rep.Activity.Weekly {
		if sl.Weekday == 0 || sl.Weekday == 6 || sl.Hour < 9 || sl.Hour >= 19 {
			rep.Risk.AfterHours += sl.Count
		}
	}
	return nil
}

func (s *Insights) people(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	// Workload, not a ranking: one row per person with what they carried.
	// Ordered by name so the query itself does not suggest a leaderboard.
	q := `WITH scoped AS (SELECT r.* FROM releases r WHERE ` + f.scope(a.next) + `),
	acted AS (
		SELECT created_by AS sub, created_by_name AS name,
		       1 AS created, 0 AS confirmed, 0 AS approved, 0 AS rejected,
		       CASE WHEN status = 'failed' THEN 1 ELSE 0 END AS failed
		FROM scoped
		UNION ALL
		SELECT confirmed_by, created_by_name, 0, 1, 0, 0, 0
		FROM scoped WHERE confirmed_by IS NOT NULL
		UNION ALL
		SELECT ra.sub, ra.name, 0, 0,
		       CASE WHEN ra.decision = 'approve' THEN 1 ELSE 0 END,
		       CASE WHEN ra.decision = 'reject'  THEN 1 ELSE 0 END, 0
		FROM release_approvals ra JOIN scoped s ON s.id = ra.release_id
	)
	SELECT sub,
		max(name) AS name,
		bool_or(sub LIKE 'ci:%') AS token,
		sum(created)   AS created,
		sum(confirmed) AS confirmed,
		sum(approved)  AS approved,
		sum(rejected)  AS rejected,
		sum(failed)    AS failed
	FROM acted GROUP BY sub ORDER BY name, sub
	LIMIT ` + a.next(200)
	rep.Activity.People = []insights.PersonStats{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Activity.People).Error
}

// rollbacks counts releases carrying a rollback anomaly, which the planner
// raises when the target artifact is older than what the environment runs.
func (s *Insights) rollbacks(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT count(DISTINCT r.id)
	FROM releases r
	JOIN release_items i ON i.release_id = r.id
	CROSS JOIN LATERAL jsonb_array_elements(COALESCE(i.payload->'anomalies', '[]'::jsonb)) AS an
	WHERE ` + f.scope(a.next) + ` AND an->>'code' = 'rollback'`
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Risk.Rollbacks).Error
}

// repeats finds a service released to the same environment three or more
// times in one day: usually firefighting, occasionally someone using a shared
// environment as a workbench.
func (s *Insights) repeats(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	tz := a.next(f.tz())
	q := `SELECT i.payload->>'service' AS service, r.env,
		to_char(r.created_at AT TIME ZONE ` + tz + `, 'YYYY-MM-DD') AS day,
		count(*) AS count
	FROM releases r
	JOIN release_items i ON i.release_id = r.id
	WHERE ` + f.scope(a.next) + ` AND i.payload->>'service' IS NOT NULL
	GROUP BY 1, 2, 3 HAVING count(*) >= 3
	ORDER BY count DESC, day DESC, service
	LIMIT ` + a.next(f.topN())
	rep.Risk.Repeats = []insights.Repeat{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Risk.Repeats).Error
}

func (s *Insights) jiraReuse(ctx context.Context, f InsightsFilter, rep *insights.Report) error {
	a := &argf{}
	q := `SELECT r.jira_ticket AS jira, count(*) AS count
	FROM releases r
	WHERE ` + f.scope(a.next) + ` AND r.jira_ticket <> ''
	GROUP BY 1 HAVING count(*) > 1
	ORDER BY count DESC, jira
	LIMIT ` + a.next(f.topN())
	rep.Risk.JiraReuse = []insights.JiraCount{}
	return s.db.WithContext(ctx).Raw(q, a.args...).Scan(&rep.Risk.JiraReuse).Error
}

// ReleasedServices lists the distinct services the range covers. Coverage is
// this set against the catalog, and the catalog lives in memory rather than in
// this database, so the caller does that subtraction.
func (s *Insights) ReleasedServices(ctx context.Context, f InsightsFilter) ([]string, error) {
	a := &argf{}
	q := `SELECT DISTINCT i.payload->>'service' AS service
	FROM releases r
	JOIN release_items i ON i.release_id = r.id
	WHERE ` + f.scope(a.next) + ` AND i.payload->>'service' IS NOT NULL`
	names := []string{}
	if err := s.db.WithContext(ctx).Raw(q, a.args...).Scan(&names).Error; err != nil {
		return nil, err
	}
	return names, nil
}

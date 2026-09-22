package pg_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tide/internal/audit"
	"tide/internal/insights"
	"tide/internal/release"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

var bob = audit.Actor{Sub: "local:bob", Name: "Bob"}

// anomalyInput is input() plus anomalies on the item, which is what the
// confirmation sheet highlights and what the insights report counts.
func anomalyInput(service, env string, codes ...string) release.CreateInput {
	an := make([]release.Anomaly, len(codes))
	for i, c := range codes {
		an[i] = release.Anomaly{Code: c, Message: c}
	}
	p, _ := json.Marshal(release.ImagePayload{
		Upstream: "local", Project: "sample-pipeline", Stage: env, Service: service, Env: env,
		Freight:   strings.Repeat("a", 40),
		To:        release.Artifact{Digest: "sha256:" + strings.Repeat("b", 64), Tag: "20260916151427-46b7619f-0022"},
		Anomalies: an,
	})
	return release.CreateInput{Env: env, JiraTicket: "OPS-1", Reason: "test",
		Items: []release.ItemInput{{Kind: release.KindImage, Sequence: 1, Payload: p}}}
}

// wideRange covers everything a test could have written.
func wideRange() insights.Range {
	return insights.Range{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)}
}

func report(t *testing.T, s *pg.Store, f pg.InsightsFilter, people bool) *insights.Report {
	t.Helper()
	if f.Range.To.IsZero() {
		f.Range = wideRange()
	}
	rep, err := s.Insights.Report(context.Background(), f, people)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestInsightsCountsAndScope(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()

	// Two releases that finish, one that stays in confirming.
	for _, tc := range []struct {
		service, env string
		status       release.Status
	}{
		{"insights-a", "qa", release.Succeeded},
		{"insights-b", "qa", release.Failed},
		{"insights-c", "uat", release.Succeeded},
	} {
		r, err := s.Releases.Create(ctx, alice, input(tc.service, tc.env))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Submit(ctx, alice, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Confirm(ctx, alice, r.ID, 0, time.Hour, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Releases.Finish(ctx, r.ID, tc.status); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.Releases.Create(ctx, alice, input("insights-d", "qa"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, pending.ID); err != nil {
		t.Fatal(err)
	}

	rep := report(t, s, pg.InsightsFilter{}, false)
	if rep.Totals.Releases != 4 || rep.Totals.Succeeded != 2 || rep.Totals.Failed != 1 || rep.Totals.InFlight != 1 {
		t.Errorf("totals = %+v", rep.Totals)
	}
	if len(rep.Activity.Services) != 4 {
		t.Errorf("services = %+v", rep.Activity.Services)
	}
	if len(rep.Activity.Environments) != 2 {
		t.Errorf("environments = %+v", rep.Activity.Environments)
	}

	t.Run("environment filter", func(t *testing.T) {
		rep := report(t, s, pg.InsightsFilter{Env: "uat"}, false)
		if rep.Totals.Releases != 1 || rep.Totals.Succeeded != 1 {
			t.Errorf("uat totals = %+v", rep.Totals)
		}
	})

	t.Run("visible services", func(t *testing.T) {
		rep := report(t, s, pg.InsightsFilter{Services: []string{"insights-a"}}, false)
		if rep.Totals.Releases != 1 {
			t.Errorf("scoped totals = %+v", rep.Totals)
		}
	})

	// An empty (non-nil) list is "this viewer may see nothing", which must
	// not fall through to "no restriction".
	t.Run("no visible services", func(t *testing.T) {
		rep := report(t, s, pg.InsightsFilter{Services: []string{}}, false)
		if rep.Totals.Releases != 0 || len(rep.Activity.Services) != 0 {
			t.Errorf("empty scope leaked: %+v %+v", rep.Totals, rep.Activity.Services)
		}
	})

	t.Run("range excludes", func(t *testing.T) {
		old := insights.Range{From: time.Now().Add(-48 * time.Hour), To: time.Now().Add(-24 * time.Hour)}
		rep := report(t, s, pg.InsightsFilter{Range: old}, false)
		if rep.Totals.Releases != 0 {
			t.Errorf("out-of-range totals = %+v", rep.Totals)
		}
	})
}

func TestInsightsAnomaliesAndRollbacks(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()

	// One release flagged and confirmed anyway, one flagged and cancelled.
	went, err := s.Releases.Create(ctx, alice, anomalyInput("anom-a", "qa", "rollback", "short_soak"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, went.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Confirm(ctx, alice, went.ID, 0, time.Hour, nil); err != nil {
		t.Fatal(err)
	}

	stopped, err := s.Releases.Create(ctx, alice, anomalyInput("anom-b", "qa", "rollback"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, stopped.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Cancel(ctx, alice, stopped.ID, "decided against it"); err != nil {
		t.Fatal(err)
	}

	// A release with no anomalies must not be counted.
	clean, err := s.Releases.Create(ctx, alice, input("anom-c", "qa"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, clean.ID); err != nil {
		t.Fatal(err)
	}

	rep := report(t, s, pg.InsightsFilter{}, false)
	a := rep.Process.Anomalies
	if a.WithAnomaly != 2 || a.WentAhead != 1 {
		t.Errorf("anomalies = %+v, want 2 flagged / 1 went ahead", a)
	}
	byCode := map[string]int64{}
	for _, c := range a.ByCode {
		byCode[c.Code] = c.Count
	}
	if byCode["rollback"] != 2 || byCode["short_soak"] != 1 {
		t.Errorf("byCode = %+v", a.ByCode)
	}
	if rep.Risk.Rollbacks != 2 {
		t.Errorf("rollbacks = %d, want 2", rep.Risk.Rollbacks)
	}
}

func TestInsightsApprovals(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	rule := &release.ApprovalRule{Name: "prod", Approvers: []string{"user:local:bob"},
		Mode: release.ApproveAny, TimeoutMinutes: 60}

	mk := func(service string, approve bool) string {
		r, err := s.Releases.Create(ctx, alice, input(service, "prod"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Submit(ctx, alice, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Confirm(ctx, alice, r.ID, 0, time.Hour, rule); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Releases.Decide(ctx, bob, r.ID, nil, approve, ""); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	mk("appr-a", true)
	mk("appr-b", false)

	rep := report(t, s, pg.InsightsFilter{}, false)
	ap := rep.Process.Approvals
	if ap.Requested != 2 || ap.Approved != 1 || ap.Rejected != 1 {
		t.Errorf("approvals = %+v", ap)
	}
	// Decisions were made right after confirmation, so the wait is small but
	// must be a real measurement, not a zero default.
	if ap.MedianSeconds < 0 || ap.MedianSeconds > 60 {
		t.Errorf("median wait = %v, want a small positive number", ap.MedianSeconds)
	}
	// alice created and confirmed both; bob decided them.
	if ap.SelfConfirmed != 2 {
		t.Errorf("selfConfirmed = %d, want 2", ap.SelfConfirmed)
	}
}

func TestInsightsPeopleAndSources(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	for _, actor := range []audit.Actor{alice, alice, bob} {
		r, err := s.Releases.Create(ctx, actor, input("people-"+actor.Sub[6:]+time.Now().Format("150405.000000"), "qa"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Submit(ctx, actor, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Confirm(ctx, actor, r.ID, 0, time.Hour, nil); err != nil {
			t.Fatal(err)
		}
	}

	rep := report(t, s, pg.InsightsFilter{}, true)
	got := map[string]insights.PersonStats{}
	for _, p := range rep.Activity.People {
		got[p.Sub] = p
	}
	if got[alice.Sub].Created != 2 || got[alice.Sub].Confirmed != 2 {
		t.Errorf("alice = %+v", got[alice.Sub])
	}
	if got[bob.Sub].Created != 1 {
		t.Errorf("bob = %+v", got[bob.Sub])
	}
	// Machine subjects are marked so the client can keep them out of a
	// comparison between people.
	for _, p := range rep.Activity.People {
		if p.Token {
			t.Errorf("%s marked as a token, but only people released here", p.Sub)
		}
	}
	// Ordered by name, not by volume: the query must not read as a ranking.
	names := make([]string, len(rep.Activity.People))
	for i, p := range rep.Activity.People {
		names[i] = p.Name
	}
	if len(names) >= 2 && names[0] > names[1] {
		t.Errorf("people not ordered by name: %v", names)
	}

	// Without the flag the section is left out entirely.
	if rep := report(t, s, pg.InsightsFilter{}, false); rep.Activity.People != nil {
		t.Errorf("people returned without the flag: %+v", rep.Activity.People)
	}

	// Everything was created through the UI, so that is the only source.
	if len(rep.Process.Sources) != 1 || rep.Process.Sources[0].Source != "ui" {
		t.Errorf("sources = %+v", rep.Process.Sources)
	}
}

func TestInsightsReleasedServicesAndJira(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	// The same service twice, in different environments: one in-flight change
	// per service and environment is a hard rule, and "distinct services" has
	// to see through the repetition anyway.
	for _, tc := range []struct{ svc, env string }{{"cov-a", "qa"}, {"cov-b", "qa"}, {"cov-a", "uat"}} {
		r, err := s.Releases.Create(ctx, alice, input(tc.svc, tc.env))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Submit(ctx, alice, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Confirm(ctx, alice, r.ID, 0, time.Hour, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Releases.Finish(ctx, r.ID, release.Succeeded); err != nil {
			t.Fatal(err)
		}
	}

	names, err := s.Insights.ReleasedServices(ctx, pg.InsightsFilter{Range: wideRange()})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Errorf("released services = %v, want the two distinct names", names)
	}

	// All three share OPS-1, which is exactly the reuse the report flags.
	rep := report(t, s, pg.InsightsFilter{}, false)
	if len(rep.Risk.JiraReuse) != 1 || rep.Risk.JiraReuse[0].Count != 3 {
		t.Errorf("jira reuse = %+v", rep.Risk.JiraReuse)
	}
	// Three releases of cov-a + cov-b on the same day, and cov-a alone has
	// two — below the threshold of three, so nothing is flagged.
	if len(rep.Risk.Repeats) != 0 {
		t.Errorf("repeats = %+v, want none", rep.Risk.Repeats)
	}
}

func TestInsightsTimeZoneGrouping(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	r, err := s.Releases.Create(ctx, alice, input("tz-a", "qa"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, r.ID); err != nil {
		t.Fatal(err)
	}

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	utc := report(t, s, pg.InsightsFilter{Location: time.UTC}, false)
	local := report(t, s, pg.InsightsFilter{Location: shanghai}, false)
	if len(utc.Activity.Weekly) != 1 || len(local.Activity.Weekly) != 1 {
		t.Fatalf("weekly buckets: utc=%+v local=%+v", utc.Activity.Weekly, local.Activity.Weekly)
	}
	want := (utc.Activity.Weekly[0].Hour + 8) % 24
	if local.Activity.Weekly[0].Hour != want {
		t.Errorf("Shanghai hour = %d, want %d (UTC %d + 8)",
			local.Activity.Weekly[0].Hour, want, utc.Activity.Weekly[0].Hour)
	}
}

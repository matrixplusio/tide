package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tide/internal/audit"
	"tide/internal/release"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

var alice = audit.Actor{Sub: "local:alice", Name: "Alice"}

func input(service, env string) release.CreateInput {
	p, _ := json.Marshal(release.ImagePayload{
		Upstream: "local", Project: "sample-pipeline", Stage: env, Service: service, Env: env,
		Freight: strings.Repeat("a", 40), To: release.Artifact{Digest: "sha256:" + strings.Repeat("b", 64), Tag: "20260916151427-46b7619f-0022"},
	})
	return release.CreateInput{Env: env, JiraTicket: "OPS-1", Reason: "test",
		Items: []release.ItemInput{{Kind: release.KindImage, Sequence: 1, Payload: p}}}
}

func TestAuditIsAppendOnly(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	if err := s.Audit.Write(ctx, alice, "test", "x", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE audit_log SET actor = 'mallory'`, `DELETE FROM audit_log`, `TRUNCATE audit_log`} {
		err := s.DB().Exec(sql).Error
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s as tide_app: want permission denied, got %v", sql, err)
		}
	}
}

func TestJiraIsOptionalInStorage(t *testing.T) {
	s, _ := testdb.Setup(t)
	in := input("svc-j", "dev")
	in.JiraTicket = ""
	r, err := s.Releases.Create(context.Background(), alice, in)
	if err != nil || r.JiraTicket != "" {
		t.Fatalf("release without Jira: %+v %v", r, err)
	}
	in.JiraTicket = "not a ticket"
	if _, err := s.Releases.Create(context.Background(), alice, in); !errors.Is(err, release.ErrInvalid) {
		t.Fatalf("malformed Jira: %v", err)
	}
}

func TestAuditMetaAndLikeEscaping(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := audit.WithMeta(context.Background(), audit.Meta{RequestID: "rid-1", ClientIP: "10.1.2.3"})
	s.Audit.Write(ctx, alice, "release.create", "REL-1", "OPS-100", nil)
	s.Audit.Write(ctx, alice, "release.create", "REL-2", "OPSX100", nil)
	items, total, err := s.Audit.List(ctx, audit.Filter{Jira: "OPS_100"}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("underscore must be literal, matched %d", total)
	}
	items, total, _ = s.Audit.List(ctx, audit.Filter{Jira: "OPS-100"}, 1, 20)
	if total != 1 || items[0].RequestID != "rid-1" || items[0].ClientIP != "10.1.2.3" {
		t.Fatalf("got %d %+v", total, items)
	}
}

func TestConfirmEnforcedServerSide(t *testing.T) {
	s, owner := testdb.Setup(t)
	ctx := context.Background()
	r, err := s.Releases.Create(ctx, alice, input("svc-a", "qa"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.ID, "REL-") || r.Status != release.Draft {
		t.Fatalf("unexpected %+v", r)
	}
	if _, err := s.Releases.Confirm(ctx, alice, r.ID, 10*time.Second, 10*time.Minute, nil); !errors.Is(err, release.ErrInvalidTransition) {
		t.Fatalf("confirm from draft: %v", err)
	}
	if _, err := s.Releases.Submit(ctx, alice, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Confirm(ctx, alice, r.ID, 10*time.Second, 10*time.Minute, nil); !errors.Is(err, release.ErrTooEarly) {
		t.Fatalf("immediate confirm: want ErrTooEarly, got %v", err)
	}
	items, _, _ := s.Audit.List(ctx, audit.Filter{Action: "release.confirm.denied"}, 1, 20)
	if len(items) != 1 {
		t.Errorf("denied confirm must be audited, got %d", len(items))
	}
	owner.Exec(`UPDATE releases SET submitted_at = now() - interval '11 seconds' WHERE id = $1`, r.ID)
	got, err := s.Releases.Confirm(ctx, alice, r.ID, 10*time.Second, 10*time.Minute, nil)
	if err != nil || got.Status != release.Executing || got.ConfirmedAt == nil {
		t.Fatalf("after window: %+v %v", got, err)
	}
	owner.Exec(`UPDATE releases SET submitted_at = now() - interval '11 minutes', status = 'confirming' WHERE id = $1`, r.ID)
	if _, err := s.Releases.Confirm(ctx, alice, r.ID, 10*time.Second, 10*time.Minute, nil); !errors.Is(err, release.ErrConfirmExpired) {
		t.Fatalf("stale confirm: %v", err)
	}
}

func TestOneInFlightPerTarget(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	const n = 8
	ids := make([]string, n)
	for i := range ids {
		r, err := s.Releases.Create(ctx, alice, input("svc-b", "uat"))
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = r.ID
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Releases.Submit(ctx, alice, ids[i])
		}()
	}
	wg.Wait()
	ok, busy := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, release.ErrTargetBusy):
			busy++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || busy != n-1 {
		t.Fatalf("concurrent submits: %d ok, %d busy", ok, busy)
	}
	other, _ := s.Releases.Create(ctx, alice, input("svc-b", "prod"))
	if _, err := s.Releases.Submit(ctx, alice, other.ID); err != nil {
		t.Fatalf("other env is another target: %v", err)
	}
	for i, err := range errs {
		if err == nil {
			s.Releases.Cancel(ctx, alice, ids[i], "test")
		}
	}
	again, _ := s.Releases.Create(ctx, alice, input("svc-b", "uat"))
	if _, err := s.Releases.Submit(ctx, alice, again.ID); err != nil {
		t.Fatalf("cancel must free the target: %v", err)
	}
	// A restart is a change to the same target: it waits for the upgrade too.
	rp, _ := json.Marshal(release.RestartPayload{Upstream: "local", Service: "svc-b", Env: "uat", App: "svc-b-uat",
		Workloads: []release.Workload{{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "ns", Name: "svc-b"}}})
	restart, err := s.Releases.Create(ctx, alice, release.CreateInput{Env: "uat", JiraTicket: "OPS-2", Reason: "reload config",
		Items: []release.ItemInput{{Kind: release.KindRestart, Sequence: 1, Payload: rp}}})
	if err != nil {
		t.Fatal(err)
	}
	if restart.Title != "svc-b 重启 @ uat" {
		t.Errorf("restart title: %q", restart.Title)
	}
	if _, err := s.Releases.Submit(ctx, alice, restart.ID); !errors.Is(err, release.ErrTargetBusy) {
		t.Fatalf("restart during upgrade: %v", err)
	}
}

func TestFailedIsTerminalAndPaging(t *testing.T) {
	s, owner := testdb.Setup(t)
	ctx := context.Background()
	r, _ := s.Releases.Create(ctx, alice, input("svc-c", "qa"))
	s.Releases.Submit(ctx, alice, r.ID)
	owner.Exec(`UPDATE releases SET submitted_at = now() - interval '11 seconds' WHERE id = $1`, r.ID)
	if _, err := s.Releases.Confirm(ctx, alice, r.ID, 10*time.Second, 10*time.Minute, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Releases.Finish(ctx, r.ID, release.Failed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, r.ID); !errors.Is(err, release.ErrInvalidTransition) {
		t.Errorf("resubmit failed: %v", err)
	}
	if err := s.Releases.Finish(ctx, r.ID, release.Succeeded); !errors.Is(err, release.ErrInvalidTransition) {
		t.Errorf("failed → succeeded: %v", err)
	}
	for range 3 {
		s.Releases.Create(ctx, alice, input("svc-d", "qa"))
	}
	list, total, err := s.Releases.List(ctx, pg.ReleaseFilter{Env: "qa"}, 2, 2)
	if err != nil || total != 4 || len(list) != 2 || len(list[0].Items) != 1 {
		t.Fatalf("page 2: total=%d len=%d err=%v", total, len(list), err)
	}
	failed, total, _ := s.Releases.List(ctx, pg.ReleaseFilter{Statuses: []release.Status{release.Failed}}, 1, 20)
	if total != 1 || failed[0].ID != r.ID {
		t.Fatalf("status filter: %d", total)
	}
}

func TestApprovalFlow(t *testing.T) {
	s, owner := testdb.Setup(t)
	ctx := context.Background()
	bob := audit.Actor{Sub: "local:bob", Name: "Bob"}
	carol := audit.Actor{Sub: "local:carol", Name: "Carol"}
	eve := audit.Actor{Sub: "local:eve", Name: "Eve"}
	rule := &release.ApprovalRule{Name: "prod", Approvers: []string{"group:leaders"}, Mode: release.ApproveCount, MinApprovals: 2, TimeoutMinutes: 60}
	leaders := []string{"leaders"}

	confirmed := func(service string) *release.Release {
		t.Helper()
		r, err := s.Releases.Create(ctx, alice, input(service, "prod"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Releases.Submit(ctx, alice, r.ID); err != nil {
			t.Fatal(err)
		}
		r, err = s.Releases.Confirm(ctx, alice, r.ID, 0, time.Hour, rule)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := confirmed("svc-approve")
	if r.Status != release.Approving || r.ApprovalRule == nil || r.ApprovalRule.MinApprovals != 2 || r.ApprovalExpiresAt == nil {
		t.Fatalf("confirmed into approving with a rule snapshot: %+v", r)
	}
	if _, _, err := s.Releases.Decide(ctx, alice, r.ID, leaders, true, ""); !errors.Is(err, release.ErrSelfApproval) {
		t.Fatalf("creator approving own release: %v", err)
	}
	if _, _, err := s.Releases.Decide(ctx, eve, r.ID, []string{"dev"}, true, ""); !errors.Is(err, release.ErrNotApprover) {
		t.Fatalf("non-approver: %v", err)
	}
	got, started, err := s.Releases.Decide(ctx, bob, r.ID, leaders, true, "lgtm")
	if err != nil || started || got.Status != release.Approving || len(got.Approvals) != 1 {
		t.Fatalf("first of two approvals: %+v started=%v err=%v", got, started, err)
	}
	if _, _, err := s.Releases.Decide(ctx, bob, r.ID, leaders, true, ""); !errors.Is(err, release.ErrAlreadyDecided) {
		t.Fatalf("second decision by the same person: %v", err)
	}
	got, started, err = s.Releases.Decide(ctx, carol, r.ID, leaders, true, "")
	if err != nil || !started || got.Status != release.Executing {
		t.Fatalf("second approval starts the release: %+v started=%v err=%v", got, started, err)
	}

	r = confirmed("svc-reject")
	got, _, err = s.Releases.Decide(ctx, bob, r.ID, leaders, false, "wrong version")
	if err != nil || got.Status != release.Rejected || got.Items[0].Status != release.ItemCancelled || got.Approvals[0].Note != "wrong version" {
		t.Fatalf("rejection ends the release and frees the target: %+v %v", got, err)
	}
	// the target is free again
	if _, err := s.Releases.Create(ctx, alice, input("svc-reject", "prod")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, got.ID); err == nil {
		t.Fatal("a rejected release is terminal")
	}

	r = confirmed("svc-expire")
	if err := owner.Exec(`UPDATE releases SET approval_expires_at = now() - interval '1 minute' WHERE id = ?`, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Releases.Decide(ctx, bob, r.ID, leaders, true, ""); !errors.Is(err, release.ErrApprovalExpired) {
		t.Fatalf("approving after the window: %v", err)
	}
	expired, err := s.Releases.ExpireApprovals(ctx)
	if err != nil || len(expired) != 1 || expired[0].Status != release.Cancelled {
		t.Fatalf("expired approvals are cancelled: %+v %v", expired, err)
	}

	for _, sql := range []string{`UPDATE release_approvals SET decision = 'approve'`, `DELETE FROM release_approvals`} {
		if err := s.DB().Exec(sql).Error; err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s as tide_app: want permission denied, got %v", sql, err)
		}
	}
}

func TestListFilters(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	bob := audit.Actor{Sub: "local:bob", Name: "Bob Li"}
	rule := &release.ApprovalRule{Name: "prod", Approvers: []string{"group:leaders"}, Mode: release.ApproveAny, TimeoutMinutes: 60}
	mk := func(actor audit.Actor, service, env string) *release.Release {
		t.Helper()
		r, err := s.Releases.Create(ctx, actor, input(service, env))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	mk(alice, "order-api", "dev")
	mk(bob, "order-web", "qa")
	p := mk(alice, "task_api", "prod")
	if _, err := s.Releases.Submit(ctx, alice, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Confirm(ctx, alice, p.ID, 0, time.Hour, rule); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Releases.Decide(ctx, bob, p.ID, []string{"leaders"}, false, "no"); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	for name, tc := range map[string]struct {
		f    pg.ReleaseFilter
		want int64
	}{
		"service substring":          {pg.ReleaseFilter{ServiceLike: "order"}, 2},
		"service exact":              {pg.ReleaseFilter{Service: "order"}, 0},
		"service exact name":         {pg.ReleaseFilter{Service: "order-web"}, 1},
		"service like is escaped":    {pg.ReleaseFilter{ServiceLike: "k_a"}, 1},
		"service underscore literal": {pg.ReleaseFilter{ServiceLike: "r_w"}, 0},
		"services set":               {pg.ReleaseFilter{Services: []string{"order-web", "task_api"}}, 2},
		"empty services set":         {pg.ReleaseFilter{Services: []string{}}, 0},
		"created by":                 {pg.ReleaseFilter{CreatedBy: alice.Sub}, 2},
		"creator name":               {pg.ReleaseFilter{Creator: "bob l"}, 1},
		"creator sub":                {pg.ReleaseFilter{Creator: "local:ali"}, 2},
		"decided by":                 {pg.ReleaseFilter{DecidedBy: bob.Sub}, 1},
		"kind restart":               {pg.ReleaseFilter{Kind: release.KindRestart}, 0},
		"kind image + rejected":      {pg.ReleaseFilter{Kind: release.KindImage, Statuses: []release.Status{release.Rejected}}, 1},
		"since future":               {pg.ReleaseFilter{Since: &future}, 0},
		"until future":               {pg.ReleaseFilter{Until: &future}, 3},
	} {
		if _, total, err := s.Releases.List(ctx, tc.f, 1, 20); err != nil || total != tc.want {
			t.Errorf("%s: total %d err %v, want %d", name, total, err, tc.want)
		}
	}
}

func TestAuditServiceScope(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	write := func(action string, detail map[string]any) {
		t.Helper()
		if err := s.Audit.Write(ctx, alice, action, "t", "", detail); err != nil {
			t.Fatal(err)
		}
	}
	write("release.create", map[string]any{"service": "order-api", "env": "qa"})
	write("release.create", map[string]any{"items": []map[string]any{{"service": "task-api"}, {"service": "order-web"}}})
	write("release.create", map[string]any{"items": map[string]any{"service": "task-api"}}) // an object, not a list
	write("user.login", nil)                                                                // belongs to no service

	for name, tc := range map[string]struct {
		services []string
		want     int64
	}{
		"unrestricted":       {nil, 4},
		"one service":        {[]string{"order-api"}, 1},
		"service in a batch": {[]string{"order-web"}, 1},
		"two services":       {[]string{"order-api", "task-api"}, 2},
		"nothing visible":    {[]string{}, 0},
	} {
		_, total, err := s.Audit.List(ctx, audit.Filter{Services: tc.services}, 1, 20)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total != tc.want {
			t.Errorf("%s: total %d, want %d", name, total, tc.want)
		}
	}
	// Two conditions that each repeat their placeholder (pre-existing pattern).
	if _, _, err := s.Audit.List(ctx, audit.Filter{Service: "order-api", Env: "qa"}, 1, 20); err != nil {
		t.Fatalf("service + env filter: %v", err)
	}
	// A service filter on top of the scope: both must hold.
	if _, total, err := s.Audit.List(ctx, audit.Filter{Services: []string{"order-api"}, Service: "task-api"}, 1, 20); err != nil || total != 0 {
		t.Fatalf("scope + service filter: total %d err %v", total, err)
	}
	if _, total, err := s.Audit.List(ctx, audit.Filter{Services: []string{"order-api"}, Service: "order-api"}, 1, 20); err != nil || total != 1 {
		t.Fatalf("scope + matching service filter: total %d err %v", total, err)
	}
}

func TestCountTodayRespectsScope(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	for _, svc := range []string{"order-api", "task-api"} {
		if _, err := s.Releases.Create(ctx, alice, input(svc, "qa")); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		services []string
		want     int64
	}{
		"everything visible": {nil, 2},
		"one project":        {[]string{"order-api"}, 1},
		"nothing visible":    {[]string{}, 0},
	} {
		total, _, err := s.Releases.CountToday(ctx, tc.services)
		if err != nil || total != tc.want {
			t.Errorf("%s: total %d err %v, want %d", name, total, err, tc.want)
		}
	}
}

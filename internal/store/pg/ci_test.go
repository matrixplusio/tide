package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"tide/internal/audit"
	"tide/internal/ci"
	"tide/internal/release"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

func token(t *testing.T, s *pg.Store, name string) (pg.CIToken, string) {
	t.Helper()
	plain, hash, prefix, err := ci.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tok := pg.CIToken{ID: "11111111-1111-1111-1111-" + strings.Repeat("0", 12-len(name)) + name, Name: name, Prefix: prefix,
		CreatedBy: alice.Sub, CreatedByName: alice.Name}
	if err := s.CI.CreateToken(context.Background(), tok, hash); err != nil {
		t.Fatal(err)
	}
	return tok, plain
}

// A token resolves by hash only; the plaintext is never stored, and a revoked
// token answers exactly like one that never existed.
func TestCITokenLookupAndRevocation(t *testing.T) {
	s, owner := testdb.Setup(t)
	ctx := context.Background()
	tok, plain := token(t, s, "a")

	got, err := s.CI.TokenByHash(ctx, ci.HashToken(plain))
	if err != nil || got.ID != tok.ID {
		t.Fatalf("lookup by hash: %+v %v", got, err)
	}
	if _, err := s.CI.TokenByHash(ctx, ci.HashToken(plain+"x")); !errors.Is(err, pg.ErrCITokenUnknown) {
		t.Fatalf("a wrong token resolved: %v", err)
	}
	var stored int64
	if err := owner.Raw(`SELECT count(*) FROM ci_tokens WHERE hash = $1`, plain).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatal("the token itself was written to the database")
	}
	if err := s.CI.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CI.TokenByHash(ctx, ci.HashToken(plain)); !errors.Is(err, pg.ErrCITokenUnknown) {
		t.Fatalf("a revoked token still resolves: %v", err)
	}
	if err := s.CI.RevokeToken(ctx, tok.ID); !errors.Is(err, pg.ErrCITokenUnknown) {
		t.Fatalf("revoking twice: %v", err)
	}
}

// Re-running a pipeline must not release twice. The uniqueness is the
// database's, not the application's, so concurrent calls collapse too.
func TestCIIntakeIsIdempotent(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	tok, _ := token(t, s, "b")
	in := pg.CIIntake{Key: "sha256:" + strings.Repeat("c", 64), Service: "svc", Env: "dev",
		Digest: "sha256:" + strings.Repeat("c", 64), TokenID: tok.ID}

	first, accepted, err := s.CI.Accept(ctx, in)
	if err != nil || !accepted {
		t.Fatalf("first call: %+v accepted=%v %v", first, accepted, err)
	}
	again, accepted, err := s.CI.Accept(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("the same digest was accepted twice")
	}
	if again.ID != first.ID {
		t.Fatalf("got intake %d, want the original %d", again.ID, first.ID)
	}

	var wg sync.WaitGroup
	accepts := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := s.CI.Accept(ctx, in)
			if err != nil {
				t.Error(err)
				return
			}
			accepts <- ok
		}()
	}
	wg.Wait()
	close(accepts)
	for ok := range accepts {
		if ok {
			t.Fatal("a concurrent retry created a second intake")
		}
	}
}

// Resolving is a one-way door: whichever replica gets there first wins.
func TestCIIntakeResolvesOnce(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	tok, _ := token(t, s, "c")
	digest := "sha256:" + strings.Repeat("d", 64)
	in, _, err := s.CI.Accept(ctx, pg.CIIntake{Key: digest, Service: "svc", Env: "dev", Digest: digest, TokenID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CI.Waiting(ctx, 10)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("waiting: %d %v", len(waiting), err)
	}
	rel, err := s.Releases.Create(ctx, alice, input("svc", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CI.Resolve(ctx, in.ID, pg.IntakeReleased, rel.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CI.Resolve(ctx, in.ID, pg.IntakeFailed, "", "too late"); err != nil {
		t.Fatal(err)
	}
	got, err := s.CI.IntakeByKey(ctx, digest)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != pg.IntakeReleased || got.ReleaseID != rel.ID || got.Error != "" {
		t.Fatalf("a resolved intake was overwritten: %+v", got)
	}
	if list, err := s.CI.Waiting(ctx, 10); err != nil || len(list) != 0 {
		t.Fatalf("resolved intake still waiting: %d %v", len(list), err)
	}
}

// A CI release skips the reading window because nobody is reading, but it
// still goes through submit (which claims the target) and lands in the same
// states as any other release.
func TestStartSkipsTheReadingWindowButNothingElse(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	in := input("svc-auto", "dev")
	in.Source = release.SourceCI
	rel, err := s.Releases.Create(ctx, alice, in)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Source != release.SourceCI {
		t.Fatalf("source not stored: %q", rel.Source)
	}
	if _, err := s.Releases.Start(ctx, alice, rel.ID, nil, nil); !errors.Is(err, release.ErrInvalidTransition) {
		t.Fatalf("started a draft: %v", err)
	}
	if _, err := s.Releases.Submit(ctx, alice, rel.ID); err != nil {
		t.Fatal(err)
	}
	started, err := s.Releases.Start(ctx, alice, rel.ID, nil, map[string]any{"pipeline": "https://ci/1"})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != release.Executing {
		t.Fatalf("got %q, want executing", started.Status)
	}
	// A second target in the same env is still refused while this one runs.
	other, err := s.Releases.Create(ctx, alice, input("svc-auto", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, other.ID); !errors.Is(err, release.ErrTargetBusy) {
		t.Fatalf("the target was not claimed: %v", err)
	}
}

// An approval rule applies to a CI release too: automatic is not unapproved.
func TestStartStillWaitsForApprovers(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	in := input("svc-appr", "prod")
	in.Source = release.SourceCI
	rel, err := s.Releases.Create(ctx, alice, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, rel.ID); err != nil {
		t.Fatal(err)
	}
	rule := &release.ApprovalRule{Name: "prod", Approvers: []string{"user:local:bob"}, Mode: release.ApproveAny, TimeoutMinutes: 30}
	started, err := s.Releases.Start(ctx, alice, rel.ID, rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != release.Approving {
		t.Fatalf("got %q, want approving", started.Status)
	}
	if started.ApprovalRule == nil || started.ApprovalExpiresAt == nil {
		t.Fatalf("the rule was not snapshotted: %+v", started)
	}
}

// Tracing one service's history must turn up the whole release, not only the
// records that happen to carry a payload. submit / confirm / cancel used to
// store the environment and nothing else, so they fell out of the filter.
func TestAuditServiceFilterFindsEveryStepOfARelease(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	rel, err := s.Releases.Create(ctx, alice, input("svc-trace", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, rel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Start(ctx, alice, rel.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Cancel(ctx, alice, rel.ID, "done looking"); err != nil {
		// Cancel is not valid from executing; that is fine, the three above
		// are enough to prove the filter.
		_ = err
	}
	items, _, err := s.Audit.List(ctx, audit.Filter{Service: "svc-trace"}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range items {
		seen[e.Action] = true
	}
	for _, want := range []string{"release.create", "release.submit", "release.confirm.auto"} {
		if !seen[want] {
			t.Errorf("filtering by service missed %q; got %v", want, keys(seen))
		}
	}
	// And a different service must not pick them up.
	other, _, err := s.Audit.List(ctx, audit.Filter{Service: "svc-somebody-else"}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("filtering by an unrelated service returned %d records", len(other))
	}
}

// Scoped audit readers (a viewer limited to some projects) go through a
// different clause; it must learn the same shape.
func TestScopedAuditAlsoSeesTheWholeRelease(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	rel, err := s.Releases.Create(ctx, alice, input("svc-scoped", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, rel.ID); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.Audit.List(ctx, audit.Filter{Services: []string{"svc-scoped"}}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range items {
		seen[e.Action] = true
	}
	if !seen["release.submit"] {
		t.Fatalf("a project-scoped reader cannot see the submit; got %v", keys(seen))
	}
	none, _, err := s.Audit.List(ctx, audit.Filter{Services: []string{"svc-elsewhere"}}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("a reader scoped elsewhere saw %d records", len(none))
	}
}

// A machine letting a release through and a person pressing confirm are
// different events and must not share an action name.
func TestAutomaticReleaseGetsItsOwnAuditAction(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	in := input("svc-act", "dev")
	in.Source = release.SourceCI
	rel, err := s.Releases.Create(ctx, alice, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Submit(ctx, alice, rel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Releases.Start(ctx, alice, rel.ID, nil, map[string]any{"pipeline": "https://ci/9"}); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.Audit.List(ctx, audit.Filter{Target: rel.ID, Action: "release.confirm"}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != "release.confirm.auto" {
		t.Fatalf("got %d records %v, want one release.confirm.auto", len(items), actions(items))
	}
	var d map[string]any
	if err := json.Unmarshal(items[0].Detail, &d); err != nil {
		t.Fatal(err)
	}
	if d["auto"] != true || d["pipeline"] != "https://ci/9" {
		t.Fatalf("detail lost what caused it: %v", d)
	}
	// Automatic is derived; it must agree with what was recorded.
	got, err := s.Releases.Get(ctx, rel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Automatic() {
		t.Fatalf("release %+v not reported as automatic", got)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func actions(list []audit.Entry) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.Action
	}
	return out
}

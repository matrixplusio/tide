package ci

import (
	"context"
	"strings"
	"testing"

	"tide/internal/audit"
	"tide/internal/catalog"
	"tide/internal/crypto"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

func service(t *testing.T) (*Service, *pg.CIToken) {
	t.Helper()
	s, _ := testdb.Setup(t)
	box, err := crypto.New("ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM=")
	if err != nil {
		t.Fatal(err)
	}
	set := &settings.Store{PG: s, Box: box}
	ctx := context.Background()
	// An environment that does not take releases from CI at all — which is
	// what every environment is until somebody turns it on.
	if err := set.Save(ctx, s, audit.System, "environments", &settings.Environments{Items: []settings.Environment{
		{Name: "dev", Tier: "development"},
	}}); err != nil {
		t.Fatal(err)
	}
	_, hash, prefix, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tok := pg.CIToken{ID: "11111111-1111-1111-1111-111111111111", Name: "t", Prefix: prefix,
		CreatedBy: "local:alice", CreatedByName: "Alice"}
	if err := s.CI.CreateToken(ctx, tok, hash); err != nil {
		t.Fatal(err)
	}
	return &Service{PG: s, Settings: set}, &tok
}

// An environment with CI releases switched off still has builds, and those
// builds still break. Refusing the failure there would mean the environments
// nobody deploys to automatically are exactly the ones whose broken builds
// stay silent — and "does not take releases from CI" is an answer about
// releases, which a failed build is not asking for.
func TestAFailedBuildIsAcceptedEvenWhereCIReleasesAreOff(t *testing.T) {
	x, tok := service(t)
	ctx := context.Background()

	in, accepted, err := x.Accept(ctx, tok, Request{
		Service: "cart", Env: "dev", Failed: true, Stage: "compile",
		Key: "pipeline-1-compile", Detail: "undefined: total",
	})
	if err != nil {
		t.Fatalf("a failed build must not be refused for an env with ci off: %v", err)
	}
	if !accepted || in.Status != pg.IntakeBuildFailed {
		t.Fatalf("accepted=%v status=%q", accepted, in.Status)
	}

	// The same environment must still refuse a success: that one does ask to
	// release, and nobody turned this environment on.
	_, _, err = x.Accept(ctx, tok, Request{
		Service: "cart", Env: "dev", Digest: "sha256:" + strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("a successful build must still be refused where CI releases are off")
	}
}

// A refused report leaves a record: the pipeline's notify step stays green on
// a non-2xx, so without one the build went nowhere and nobody could see why.
// The record never holds the digest's key, so the re-run after the cause is
// fixed is taken in as new rather than answered with the old refusal.
func TestARefusedReportIsRecordedAndDoesNotBlockTheRerun(t *testing.T) {
	x, tok := service(t)
	x.Hub = &catalog.Hub{Settings: x.Settings}
	ctx := context.Background()
	digest := "sha256:" + strings.Repeat("b", 64)
	req := Request{Service: "cart", Env: "dev", Digest: digest, Pipeline: "p/1", Commit: strings.Repeat("c", 40)}

	for _, r := range []Request{req, func() Request { r := req; r.Env = "moon"; return r }()} {
		in, accepted, err := x.Accept(ctx, tok, r)
		if err == nil || accepted {
			t.Fatalf("%s: want a refusal, got accepted=%v err=%v", r.Env, accepted, err)
		}
		if in == nil || in.Status != pg.IntakeRejected || in.Error == "" || in.Service != "cart" || in.Env != r.Env || in.Pipeline != "p/1" {
			t.Fatalf("%s: refusal not recorded as it should be: %+v", r.Env, in)
		}
	}
	list, total, err := x.PG.CI.ListIntakes(ctx, pg.IntakeFilter{Status: pg.IntakeRejected}, 1, 10)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("rejected intakes: %d %v", total, err)
	}

	// Fixed: CI turned on. The same digest is now taken in, not answered with
	// the refusal.
	if err := x.Settings.Save(ctx, x.PG, audit.System, "environments", &settings.Environments{Items: []settings.Environment{
		{Name: "dev", Tier: "development", CI: settings.CIAuto},
	}}); err != nil {
		t.Fatal(err)
	}
	in, accepted, err := x.Accept(ctx, tok, req)
	if err != nil || !accepted || in.Status != pg.IntakeWaiting {
		t.Fatalf("re-run after the fix: accepted=%v %+v %v", accepted, in, err)
	}
}

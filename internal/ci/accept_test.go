package ci

import (
	"context"
	"strings"
	"testing"

	"tide/internal/audit"
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

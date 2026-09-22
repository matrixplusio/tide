package pg_test

import (
	"context"
	"testing"

	"tide/internal/audit"
	"tide/internal/crypto"
	"tide/internal/settings"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

func box(t *testing.T) *crypto.Box {
	t.Helper()
	b, err := crypto.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The counter and the row it describes must commit together: a rolled back
// change must not invalidate anyone's cache, and a committed one always must.
func TestGenerationIsBumpedInsideTheTransaction(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()

	before, err := s.Cache.Generations(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A transaction that fails leaves the counter where it was.
	want := errFailed
	err = s.Tx(ctx, func(tx *pg.Store) error {
		if err := tx.Cache.Bump(ctx, pg.ScopeSettings); err != nil {
			return err
		}
		return want
	})
	if err == nil {
		t.Fatal("the transaction was supposed to fail")
	}
	after, err := s.Cache.Generations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.At(pg.ScopeSettings) != before.At(pg.ScopeSettings) {
		t.Fatalf("a rolled back change moved the counter: %d → %d",
			before.At(pg.ScopeSettings), after.At(pg.ScopeSettings))
	}

	// A transaction that commits moves it.
	if err := s.Tx(ctx, func(tx *pg.Store) error { return tx.Cache.Bump(ctx, pg.ScopeSettings) }); err != nil {
		t.Fatal(err)
	}
	after, err = s.Cache.Generations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.At(pg.ScopeSettings) <= before.At(pg.ScopeSettings) {
		t.Fatalf("a committed change did not move the counter: %d → %d",
			before.At(pg.ScopeSettings), after.At(pg.ScopeSettings))
	}
}

// This is the bug the counter exists for: a second replica used to keep its
// settings until the process restarted, so a change freeze set on one pod was
// not enforced on the other.
func TestAnotherReplicaSeesSettingsImmediately(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	// Two Stores over the same database stand in for two pods; each has its
	// own in-process cache, which is the whole point.
	podA := &settings.Store{PG: s, Box: box(t)}
	podB := &settings.Store{PG: s, Box: box(t)}

	save := func(st *settings.Store, envs settings.Environments) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *pg.Store) error {
			return st.Save(ctx, tx, audit.Actor{Sub: "local:admin", Name: "admin"}, settings.SectionEnvironments, &envs)
		}); err != nil {
			t.Fatal(err)
		}
	}

	save(podA, settings.Environments{Items: []settings.Environment{{Name: "qa", Tier: "testing", CI: settings.CIAuto}}})

	// B reads and caches.
	got, err := podB.Environments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].CI != settings.CIAuto {
		t.Fatalf("pod B first read: %+v", got.Items)
	}

	// A turns CI off. B must refuse the next CI release, not keep saying auto.
	save(podA, settings.Environments{Items: []settings.Environment{{Name: "qa", Tier: "testing", CI: settings.CIOff}}})

	got, err = podB.Environments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[0].CI != settings.CIOff {
		t.Fatalf("pod B still sees ci=%q after pod A turned it off", got.Items[0].CI)
	}
}

// A freeze is a safety boundary; it has to hold on whichever replica the
// request happens to land on.
func TestAnotherReplicaSeesAFreezeImmediately(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	podA := &settings.Store{PG: s, Box: box(t)}
	podB := &settings.Store{PG: s, Box: box(t)}

	if _, err := podB.ReleasePolicy(ctx); err != nil { // B caches the empty policy
		t.Fatal(err)
	}
	policy := settings.DefaultReleasePolicy()
	policy.Freezes = []settings.Freeze{{Name: "year end", Envs: []string{"prod"}}}
	if err := s.Tx(ctx, func(tx *pg.Store) error {
		return podA.Save(ctx, tx, audit.Actor{Sub: "local:admin", Name: "admin"}, settings.SectionRelease, &policy)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := podB.ReleasePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Freezes) != 1 || got.Freezes[0].Name != "year end" {
		t.Fatalf("pod B does not know about the freeze: %+v", got.Freezes)
	}
}

var errFailed = &rollback{}

type rollback struct{}

func (*rollback) Error() string { return "deliberate rollback" }

// Changing an upstream's address or token has to reach the other replica
// too. The catalog hub caches the built clients, and that cache used to have
// no expiry at all: a rotated token stayed rotated only on the replica that
// did it, and the other kept calling with the old one until it restarted.
func TestAnotherReplicaSeesAnUpstreamAddressChange(t *testing.T) {
	s, _ := testdb.Setup(t)
	ctx := context.Background()
	podA := &settings.Store{PG: s, Box: box(t)}
	podB := &settings.Store{PG: s, Box: box(t)}
	actor := audit.Actor{Sub: "local:admin", Name: "admin"}

	save := func(st *settings.Store, url string) {
		t.Helper()
		ups := settings.Upstreams{Items: []settings.Upstream{{
			Name: "onprem", KargoURL: "https://kargo.example", KargoToken: "k",
			ArgoCDURL: url, ArgoCDToken: "a", RegistryURL: "https://registry.example",
		}}}
		if err := s.Tx(ctx, func(tx *pg.Store) error {
			if err := st.Save(ctx, tx, actor, settings.SectionUpstreams, &ups); err != nil {
				return err
			}
			// What the settings handler does: the catalog is derived from
			// upstreams, so its generation moves with them.
			return tx.Cache.Bump(ctx, pg.ScopeCatalog)
		}); err != nil {
			t.Fatal(err)
		}
	}

	save(podA, "https://argocd.example")
	before, err := s.Cache.Generation(ctx, pg.ScopeCatalog)
	if err != nil {
		t.Fatal(err)
	}
	var got settings.Upstreams
	if err := podB.Load(ctx, settings.SectionUpstreams, &got); err != nil {
		t.Fatal(err)
	}
	if got.Items[0].ArgoCDURL != "https://argocd.example" {
		t.Fatalf("pod B first read: %q", got.Items[0].ArgoCDURL)
	}

	save(podA, "https://argocd-new.example")
	after, err := s.Cache.Generation(ctx, pg.ScopeCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("the catalog generation did not move: %d → %d", before, after)
	}
	if err := podB.Load(ctx, settings.SectionUpstreams, &got); err != nil {
		t.Fatal(err)
	}
	if got.Items[0].ArgoCDURL != "https://argocd-new.example" {
		t.Fatalf("pod B still points at %q", got.Items[0].ArgoCDURL)
	}
}

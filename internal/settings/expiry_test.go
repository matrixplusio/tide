package settings

import (
	"context"
	"testing"
	"time"

	"tide/internal/audit"
	"tide/internal/crypto"
	"tide/internal/store/pg"
	"tide/internal/testdb"
)

func TestExpiringReportsOnlyWhatIsNearAndKnown(t *testing.T) {
	now := time.Date(2026, 9, 23, 11, 30, 0, 0, time.UTC)
	d := CredentialDates{
		Kargo:    "2026-12-31", // far off
		ArgoCD:   "2026-10-02", // 9 days
		Registry: "",           // nobody wrote it down
	}

	got := d.Expiring("onprem", now, ExpiryWarnDays)
	if len(got) != 1 {
		t.Fatalf("expected only the near one: %+v", got)
	}
	if got[0].Kind != "argocd" || got[0].Days != 9 || got[0].Upstream != "onprem" {
		t.Fatalf("wrong credential or arithmetic: %+v", got[0])
	}
}

func TestExpiringSortsSoonestFirstAndKeepsGoingPastTheDate(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	d := CredentialDates{
		Kargo:    "2026-09-30", // 7 days
		ArgoCD:   "2026-09-20", // 3 days ago
		Registry: "2026-09-23", // today
	}

	got := d.Expiring("onprem", now, ExpiryWarnDays)
	if len(got) != 3 {
		t.Fatalf("expected all three: %+v", got)
	}
	// A credential that already expired is the most urgent thing on the list,
	// not something to drop: it is why the upstream is failing right now.
	if got[0].Kind != "argocd" || got[0].Days != -3 {
		t.Fatalf("expired one should sort first: %+v", got)
	}
	if got[1].Days != 0 || got[2].Days != 7 {
		t.Fatalf("out of order: %+v", got)
	}
}

// The hour of day must not move the answer: "expires on the 24th" is one day
// away all through the 23rd, whether Tide is asked at 00:30 or at 23:30.
func TestExpiringIgnoresTimeOfDay(t *testing.T) {
	d := CredentialDates{Kargo: "2026-09-24"}
	for _, hour := range []int{0, 9, 23} {
		now := time.Date(2026, 9, 23, hour, 45, 0, 0, time.UTC)
		got := d.Expiring("onprem", now, ExpiryWarnDays)
		if len(got) != 1 || got[0].Days != 1 {
			t.Fatalf("at %02d:45 got %+v", hour, got)
		}
	}
}

func TestExpiringSkipsUnparseableDates(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	d := CredentialDates{Kargo: "next tuesday", ArgoCD: "2026-09-25"}
	got := d.Expiring("onprem", now, ExpiryWarnDays)
	if len(got) != 1 || got[0].Kind != "argocd" {
		t.Fatalf("a bad date must be silent, not fatal: %+v", got)
	}
}

// A form sends back the mask for a secret it never saw. Anything that wants
// to use that secret before it is saved — checking the credentials work, for
// instance — has to resolve the mask first, or every other field on the form
// becomes unchangeable without re-typing the secret.
func TestUnmaskFillsInWhatTheCallerDidNotSee(t *testing.T) {
	s, _ := testdb.Setup(t)
	box, err := crypto.New("ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM=")
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{PG: s, Box: box}
	ctx := context.Background()

	saved := &PipelineRepo{Provider: ProviderGitea, BaseURL: "https://git.example.com",
		Project: "acme/pipelines", Token: "s3cret"}
	if err := s.Tx(ctx, func(tx *pg.Store) error {
		return store.Save(ctx, tx, audit.Actor{Sub: "test", Name: "test"}, SectionPipelineRepo, saved)
	}); err != nil {
		t.Fatal(err)
	}

	// What a form sends back after changing one unrelated field.
	edited := &PipelineRepo{Provider: ProviderGitea, BaseURL: "https://git.example.com",
		Project: "acme/pipelines", Branch: "review", Token: Masked}
	if err := store.Unmask(ctx, SectionPipelineRepo, edited); err != nil {
		t.Fatal(err)
	}
	if edited.Token != "s3cret" {
		t.Fatalf("token after unmasking: %q", edited.Token)
	}
	if edited.Branch != "review" {
		t.Fatalf("unmasking overwrote an edited field: %q", edited.Branch)
	}

	// A token the caller really did type must win over the stored one.
	typed := &PipelineRepo{Token: "brand-new"}
	if err := store.Unmask(ctx, SectionPipelineRepo, typed); err != nil {
		t.Fatal(err)
	}
	if typed.Token != "brand-new" {
		t.Fatalf("unmasking replaced a supplied secret: %q", typed.Token)
	}
}

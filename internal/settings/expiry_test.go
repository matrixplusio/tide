package settings

import (
	"testing"
	"time"
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

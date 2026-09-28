package ci

import (
	"strings"
	"testing"

	"tide/internal/catalog"
)

// A green pipeline whose intake expired is told why, and the why has to be
// the thing to fix, in order: no such service, no such environment, no
// Kargo Stage there, and only then "Kargo never found the image".
func TestStrandedReasonNamesTheMissingLink(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{
		{Name: "order-api", Envs: map[string]*catalog.Deployment{
			"dev": {Env: "dev", KargoStage: "order-api-dev"},
			"qa":  {Env: "qa"}, // deployed, but no pipeline reaches it
		}},
	}}
	const freight = "waited 30m, Kargo found nothing"
	cases := []struct {
		name, service, env, want string
	}{
		{"unknown service", "cart-api", "dev", "cart-api"},
		{"known service, no such env", "order-api", "prod", "prod"},
		{"deployed, no stage", "order-api", "qa", "Kargo Stage"},
		{"all wired: the freight message stands", "order-api", "dev", freight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strandedReason(snap, c.service, c.env, freight)
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q, want it to mention %q", got, c.want)
			}
		})
	}
	// The three catalog answers must not be the freight message: that one
	// says "check the warehouse", which is the wrong door for all of them.
	for _, env := range []string{"prod", "qa"} {
		if got := strandedReason(snap, "order-api", env, freight); got == freight {
			t.Errorf("%s: fell through to the freight message", env)
		}
	}
	// No snapshot at all: the freight message is the honest fallback.
	if got := strandedReason(nil, "order-api", "dev", freight); got != freight {
		t.Errorf("nil snapshot: got %q", got)
	}
}

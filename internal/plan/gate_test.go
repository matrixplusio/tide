package plan

import (
	"testing"
	"time"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/settings"
)

func TestNewGate(t *testing.T) {
	uat := &catalog.Deployment{Service: "svc", Env: "uat", Upstream: "gcp", KargoProject: "svc-gcp", KargoStage: "uat"}
	for name, tc := range map[string]struct {
		env     string
		src     *catalog.Deployment
		nilGate bool
		label   string
		problem bool
	}{
		"no source env":        {"", nil, true, "", false},
		"not deployed there":   {"qa", nil, false, "qa", true},
		"source not in Kargo":  {"qa", &catalog.Deployment{Env: "qa", Upstream: "onprem"}, false, "qa", true},
		"same Kargo project":   {"qa", &catalog.Deployment{Env: "qa", Upstream: "gcp", KargoProject: "svc-gcp", KargoStage: "qa"}, true, "", false},
		"other site":           {"qa", &catalog.Deployment{Env: "qa", Upstream: "onprem", KargoProject: "svc", KargoStage: "qa"}, false, "qa（onprem）", false},
		"same site other proj": {"qa", &catalog.Deployment{Env: "qa", Upstream: "gcp", KargoProject: "svc", KargoStage: "qa"}, false, "qa", false},
	} {
		g := NewGate(uat, tc.env, tc.src)
		switch {
		case tc.nilGate != (g == nil):
			t.Errorf("%s: gate = %+v", name, g)
		case g != nil && (g.Label != tc.label || (g.Problem != "") != tc.problem):
			t.Errorf("%s: label %q problem %q", name, g.Label, g.Problem)
		}
	}
}

func TestGateMark(t *testing.T) {
	at := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	g := &Gate{Label: "qa（onprem）"}
	cands := &Candidates{Items: []Candidate{
		{Freight: "a", Digest: "sha256:a", Available: true},
		{Freight: "b", Digest: "sha256:b", Available: true},
		{Freight: "c", Digest: "sha256:c", Available: false},
	}}
	g.mark(cands, map[string]*time.Time{"sha256:a": &at, "sha256:c": &at})
	got := []bool{cands.Items[0].Available, cands.Items[1].Available, cands.Items[2].Available}
	if got[0] != true || got[1] != false || got[2] != false {
		t.Fatalf("availability %v: only verified and Kargo-available freight passes", got)
	}
	if cands.AvailableCount != 1 || len(cands.Items[0].VerifiedIn) != 1 || cands.Items[0].VerifiedIn[0].Stage != "qa（onprem）" {
		t.Fatalf("marks: %+v count %d", cands.Items[0].VerifiedIn, cands.AvailableCount)
	}
	// Unreadable source: nothing passes.
	blocked := &Candidates{Items: []Candidate{{Digest: "sha256:a", Available: true}}}
	g.mark(blocked, nil)
	if blocked.Items[0].Available {
		t.Fatal("candidate passed without verification data")
	}
}

func TestSoakUsesSourceVerification(t *testing.T) {
	now := time.Date(2026, 9, 18, 2, 0, 0, 0, time.UTC)
	since := now.Add(-10 * time.Minute)
	cands := &Candidates{UpstreamStages: []string{"qa（onprem）"}}
	target := &Candidate{Tag: "20260918010000-a-1", VerifiedIn: []StageMark{{Stage: "qa（onprem）", Since: &since}}}
	current := &Candidate{Tag: "20260917010000-a-0"}
	sys := settings.DefaultReleasePolicy()
	sys.MinSoakMinutes = 30
	an := Anomalies(sys, cands, target, current, &release.Artifact{Tag: current.Tag}, false, now)
	found := false
	for _, a := range an {
		if a.Code == "short_soak" {
			found = true
		}
	}
	if !found {
		t.Fatalf("anomalies %+v: 10 minutes in qa（onprem） should be short soak", an)
	}
}

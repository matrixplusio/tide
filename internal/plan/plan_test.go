package plan

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/upstream/kargo"
)

func ts(s string) *time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return &t
}

func codes(a []release.Anomaly) []string {
	var out []string
	for _, x := range a {
		out = append(out, x.Code)
	}
	return out
}

func TestAnomalies(t *testing.T) {
	sys := settings.ReleasePolicy{MinSoakMinutes: 30, MultiVersionJump: 2}
	now, _ := time.Parse(time.RFC3339, "2026-09-17T12:00:00Z")
	c := func(name, created string) Candidate {
		return Candidate{Freight: name, Tag: name, CreatedAt: ts(created)}
	}
	items := []Candidate{
		c("v5", "2026-09-17T10:00:00Z"), c("v4", "2026-09-17T09:00:00Z"), c("v3", "2026-09-17T08:00:00Z"),
		c("v2", "2026-09-17T07:00:00Z"), c("v1", "2026-09-17T06:00:00Z"),
	}
	cands := &Candidates{Items: items, UpstreamStages: []string{"qa"}}
	from := &release.Artifact{Tag: "v2"}

	t.Run("first deploy", func(t *testing.T) {
		got := codes(Anomalies(sys, cands, &items[0], nil, nil, false, now))
		if !slices.Equal(got, []string{"first_deploy"}) {
			t.Fatal(got)
		}
	})
	t.Run("rollback", func(t *testing.T) {
		got := codes(Anomalies(sys, cands, &items[4], &items[3], from, true, now))
		if !slices.Contains(got, "rollback") {
			t.Fatal(got)
		}
	})
	t.Run("multi version jump", func(t *testing.T) {
		got := codes(Anomalies(sys, cands, &items[0], &items[3], from, true, now)) // v2 → v5: 3 newer builds
		if !slices.Contains(got, "multi_version_jump") {
			t.Fatal(got)
		}
		got = codes(Anomalies(sys, cands, &items[2], &items[3], from, true, now)) // v2 → v3: 1
		if slices.Contains(got, "multi_version_jump") {
			t.Fatal(got)
		}
	})
	t.Run("short soak", func(t *testing.T) {
		target := items[1]
		target.VerifiedIn = []StageMark{{Stage: "qa", Since: ts("2026-09-17T11:50:00Z")}}
		got := codes(Anomalies(sys, cands, &target, &items[3], from, true, now))
		if !slices.Contains(got, "short_soak") {
			t.Fatal(got)
		}
		target.VerifiedIn = []StageMark{{Stage: "qa", Since: ts("2026-09-17T10:00:00Z")}}
		if got := codes(Anomalies(sys, cands, &target, &items[3], from, true, now)); slices.Contains(got, "short_soak") {
			t.Fatal(got)
		}
	})
	t.Run("tag timestamp fallback", func(t *testing.T) {
		a := &Candidate{Tag: "20260916151427-46b7619f-0022"}
		b := &release.Artifact{Tag: "20260917151427-46b7619f-0023"}
		got := codes(Anomalies(settings.ReleasePolicy{}, &Candidates{}, a, nil, b, true, now))
		if !slices.Contains(got, "rollback") {
			t.Fatal(got)
		}
	})
}

func TestRequestedFreight(t *testing.T) {
	stage := func(raw string) *kargo.Stage {
		var s kargo.Stage
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		return &s
	}
	freight := func(name, warehouse string) kargo.Freight {
		f := kargo.Freight{Origin: kargo.FreightOrigin{Kind: "Warehouse", Name: warehouse}}
		f.Metadata.Name = name
		return f
	}
	all := []kargo.Freight{freight("d1", "dev-ci"), freight("q1", "qa-ci"), freight("q2", "qa-ci")}
	names := func(list []kargo.Freight) []string {
		var out []string
		for _, f := range list {
			out = append(out, f.Metadata.Name)
		}
		return out
	}

	qa := stage(`{"spec":{"requestedFreight":[{"origin":{"kind":"Warehouse","name":"qa-ci"},"sources":{"direct":true}}]}}`)
	if got := names(requested(qa, all)); !slices.Equal(got, []string{"q1", "q2"}) {
		t.Fatalf("qa must not see dev CI freight: %v", got)
	}
	if w, direct := qa.Warehouses(); !slices.Equal(w, []string{"qa-ci"}) || !direct {
		t.Fatalf("qa warehouses: %v %v", w, direct)
	}

	uat := stage(`{"spec":{"requestedFreight":[{"origin":{"kind":"Warehouse","name":"qa-ci"},"sources":{"stages":["qa"]}}]}}`)
	if w, direct := uat.Warehouses(); !slices.Equal(w, []string{"qa-ci"}) || direct || !slices.Equal(uat.UpstreamStages(), []string{"qa"}) {
		t.Fatalf("uat: %v %v %v", w, direct, uat.UpstreamStages())
	}

	dev := stage(`{"spec":{"requestedFreight":[{"origin":{"kind":"Warehouse","name":"dev-ci"},"sources":{"direct":true}}]}}`)
	if got := names(requested(dev, all)); !slices.Equal(got, []string{"d1"}) {
		t.Fatalf("dev: %v", got)
	}
}

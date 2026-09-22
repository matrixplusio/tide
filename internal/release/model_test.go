package release

import "testing"

func TestTransitions(t *testing.T) {
	allowed := map[[2]Status]bool{
		{Draft, Confirming}: true, {Draft, Cancelled}: true,
		{Confirming, Executing}: true, {Confirming, Cancelled}: true,
		{Executing, Succeeded}: true, {Executing, Failed}: true,
	}
	all := []Status{Draft, Confirming, Executing, Succeeded, Failed, Cancelled}
	for _, from := range all {
		for _, to := range all {
			if got := CanTransition(from, to); got != allowed[[2]Status{from, to}] {
				t.Errorf("%s → %s: got %v", from, to, got)
			}
		}
	}
	for _, s := range []Status{Succeeded, Failed, Cancelled} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	// A failed release is never retried in place.
	if CanTransition(Failed, Executing) || CanTransition(Failed, Confirming) || CanTransition(Failed, Draft) {
		t.Error("failed must be terminal")
	}
}

func TestValidJira(t *testing.T) {
	for s, want := range map[string]bool{"OPS-1234": true, "G32_OPS-1": true, "ops-1": false, "OPS-": false, "": false, "OPS 12": false} {
		if ValidJira(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}

func TestApprovalRule(t *testing.T) {
	anyLeader := ApprovalRule{Mode: ApproveAny, Approvers: []string{"group:leaders", "user:local:ops"}}
	if !anyLeader.Eligible("local:bob", []string{"leaders"}, "local:alice") || !anyLeader.Eligible("local:ops", nil, "local:alice") {
		t.Fatal("group member and listed user are eligible")
	}
	if anyLeader.Eligible("local:alice", []string{"leaders"}, "local:alice") {
		t.Fatal("the creator is never eligible")
	}
	if anyLeader.Eligible("local:eve", []string{"dev"}, "local:alice") {
		t.Fatal("others are not eligible")
	}
	if anyLeader.Satisfied(nil, "local:alice") || !anyLeader.Satisfied([]string{"local:bob"}, "local:alice") {
		t.Fatal("any: one approval")
	}
	two := ApprovalRule{Mode: ApproveCount, MinApprovals: 2, Approvers: []string{"group:leaders"}}
	if two.Satisfied([]string{"a"}, "c") || !two.Satisfied([]string{"a", "b"}, "c") {
		t.Fatal("count: two approvals")
	}
	all := ApprovalRule{Mode: ApproveAll, Approvers: []string{"user:a", "user:b", "user:c"}}
	if all.Satisfied([]string{"a"}, "c") || !all.Satisfied([]string{"a", "b"}, "c") {
		t.Fatal("all: every listed user except the creator")
	}
}

// The log and the page must tell "a machine let this through" apart from
// "a person pressed confirm".
func TestAutomatic(t *testing.T) {
	const token = "ci:86c3518c"
	tests := []struct {
		name string
		rel  Release
		want bool
	}{
		{"CI created it and CI released it", Release{Source: SourceCI, CreatedBy: token, ConfirmedBy: token}, true},
		{"CI created it, a person confirmed", Release{Source: SourceCI, CreatedBy: token, ConfirmedBy: "local:admin"}, false},
		{"CI created it, nobody has yet", Release{Source: SourceCI, CreatedBy: token}, false},
		{"a person all the way", Release{Source: SourceUI, CreatedBy: "local:admin", ConfirmedBy: "local:admin"}, false},
		{"no source recorded (before CI existed)", Release{CreatedBy: "local:admin", ConfirmedBy: "local:admin"}, false},
	}
	for _, tt := range tests {
		if got := tt.rel.Automatic(); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

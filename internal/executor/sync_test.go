package executor

import (
	"testing"
	"time"

	"tide/internal/release"
)

func TestSameChanges(t *testing.T) {
	cm := func(action, diff string) release.ResourceChange {
		return release.ResourceChange{Kind: "ConfigMap", Namespace: "ns", Name: "c", Action: action, Diff: diff}
	}
	dep := release.ResourceChange{Kind: "Deployment", Namespace: "ns", Name: "d", Action: release.ChangeUpdate, Diff: "x"}
	for name, tc := range map[string]struct {
		want, got []release.ResourceChange
		same      bool
	}{
		"identical":         {[]release.ResourceChange{cm("update", "a"), dep}, []release.ResourceChange{dep, cm("update", "a")}, true},
		"diff text changed": {[]release.ResourceChange{cm("update", "a")}, []release.ResourceChange{cm("update", "b")}, false},
		"truncated ignores": {[]release.ResourceChange{{Kind: "ConfigMap", Namespace: "ns", Name: "c", Action: "update", Diff: "a", Truncated: true}}, []release.ResourceChange{cm("update", "b")}, true},
		"already applied":   {[]release.ResourceChange{cm("update", "a")}, nil, false},
		"new change":        {[]release.ResourceChange{cm("update", "a")}, []release.ResourceChange{cm("update", "a"), dep}, false},
		"action changed":    {[]release.ResourceChange{cm("update", "a")}, []release.ResourceChange{cm("delete", "a")}, false},
	} {
		if got := sameChanges(tc.want, tc.got) == ""; got != tc.same {
			t.Errorf("%s: same=%v, want %v (%q)", name, got, tc.same, sameChanges(tc.want, tc.got))
		}
	}
}

func TestSyncStartedAndRestartedSince(t *testing.T) {
	at, ok := syncStarted("sync@2026-09-18T01:09:18Z@001d1f67")
	if !ok || !at.Equal(time.Date(2026, 9, 18, 1, 9, 18, 0, time.UTC)) {
		t.Fatalf("syncStarted = %v %v", at, ok)
	}
	if _, ok := syncStarted("restart@x"); ok {
		t.Error("non-sync reference parsed")
	}
	obj := func(ts string) map[string]any {
		return map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{restartedAtAnnotation: ts}}}}}
	}
	if !restartedSince(obj("2026-09-18T01:09:18Z"), at.Add(500*time.Millisecond)) {
		t.Error("restart in the same second should count")
	}
	if restartedSince(obj("2026-09-18T01:00:00Z"), at) {
		t.Error("an older restart counted")
	}
	if restartedSince(map[string]any{}, at) {
		t.Error("no annotation counted")
	}
}

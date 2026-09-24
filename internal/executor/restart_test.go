package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"tide/internal/release"
)

func obj(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRolledOut(t *testing.T) {
	const annotated = `"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"2026-09-17T07:00:00Z"}}}`
	tests := []struct {
		name, kind, raw string
		want            bool
	}{
		{"not restarted yet", "Deployment", `{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"replicas":1}}`, false},
		{"generation not observed", "Deployment", `{"metadata":{"generation":3},"spec":{"replicas":1,` + annotated + `},"status":{"observedGeneration":2,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"replicas":1}}`, false},
		{"old pod still terminating", "Deployment", `{"metadata":{"generation":3},"spec":{"replicas":1,` + annotated + `},"status":{"observedGeneration":3,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"replicas":2}}`, false},
		{"done", "Deployment", `{"metadata":{"generation":3},"spec":{"replicas":2,` + annotated + `},"status":{"observedGeneration":3,"updatedReplicas":2,"readyReplicas":2,"availableReplicas":2,"replicas":2}}`, true},
		{"default one replica", "Deployment", `{"metadata":{"generation":3},"spec":{` + annotated + `},"status":{"observedGeneration":3,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"replicas":1}}`, true},
		{"statefulset revision rolling", "StatefulSet", `{"metadata":{"generation":2},"spec":{"replicas":1,` + annotated + `},"status":{"observedGeneration":2,"updatedReplicas":1,"readyReplicas":1,"replicas":1,"currentRevision":"a","updateRevision":"b"}}`, false},
		{"daemonset done", "DaemonSet", `{"metadata":{"generation":2},"spec":{` + annotated + `},"status":{"observedGeneration":2,"desiredNumberScheduled":3,"updatedNumberScheduled":3,"numberAvailable":3}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, why := rolledOut(tt.kind, obj(t, tt.raw)); got != tt.want {
				t.Fatalf("got %v (%s), want %v", got, why, tt.want)
			}
		})
	}
}

func TestRunsArtifact(t *testing.T) {
	w := obj(t, `{"spec":{"template":{"spec":{"containers":[{"image":"registry/app:20260917-0007"}]}}}}`)
	if !runsArtifact(w, release.Artifact{Tag: "20260917-0007", Digest: "sha256:abc"}) {
		t.Fatal("tag match")
	}
	if runsArtifact(w, release.Artifact{Tag: "20260917-0006"}) {
		t.Fatal("different tag must not match")
	}
	pinned := obj(t, `{"spec":{"template":{"spec":{"containers":[{"image":"registry/app@sha256:abc"}]}}}}`)
	if !runsArtifact(pinned, release.Artifact{Tag: "x", Digest: "sha256:abc"}) {
		t.Fatal("digest match")
	}
	if !runsArtifact(w, release.Artifact{}) {
		t.Fatal("unknown artifact matches")
	}
}

func TestRolloutStatus(t *testing.T) {
	target := release.Artifact{Tag: "20260917-0002"}
	const done = `"status":{"observedGeneration":2,"updatedReplicas":2,"readyReplicas":2,"availableReplicas":2,"replicas":2}`
	deploy := func(tag, status string) workload {
		return workload{kind: "Deployment", name: "app", obj: obj(t, `{"metadata":{"generation":2},"spec":{"replicas":2,"template":{"spec":{"containers":[{"image":"registry/app:`+tag+`"}]}}},`+status+`}`)}
	}
	tests := []struct {
		name        string
		ws          []workload
		done, ok    bool
		errContains string
	}{
		{"all new pods ready", []workload{deploy("20260917-0002", done)}, true, true, ""},
		{"one of two new pods ready", []workload{deploy("20260917-0002", `"status":{"observedGeneration":2,"updatedReplicas":2,"readyReplicas":1,"availableReplicas":1,"replicas":2}`)}, false, false, ""},
		{"old pod still terminating", []workload{deploy("20260917-0002", `"status":{"observedGeneration":2,"updatedReplicas":2,"readyReplicas":2,"availableReplicas":2,"replicas":3}`)}, false, false, ""},
		{"sync not applied yet", []workload{deploy("20260917-0001", done)}, false, false, ""},
		{"no workloads", nil, false, false, ""},
		{"another workload without the image does not block", []workload{deploy("20260917-0002", done), deploy("other-image", done)}, true, true, ""},
		{"kubernetes gave up", []workload{deploy("20260917-0002", `"status":{"observedGeneration":2,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"replicas":3,"conditions":[{"type":"Progressing","status":"False","reason":"ProgressDeadlineExceeded","message":"ReplicaSet \"app-2\" has timed out progressing."}]}`)}, true, false, "ProgressDeadlineExceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := rolloutStatus(tt.ws, target)
			if st.Done != tt.done || st.Success != tt.ok || !strings.Contains(st.Error, tt.errContains) {
				t.Fatalf("got %+v", st)
			}
			if !st.Done && st.Waiting == "" {
				t.Fatal("a pending item must say what it waits for")
			}
		})
	}
}

// Kubernetes leaves the previous rollout's conditions in place until its
// controller has looked at the new spec. So a change applied a moment ago is
// accompanied by the last attempt's verdict, and reading that as this
// attempt's is how a release was marked failed twenty-six seconds in, quoting
// a deadline of ten minutes it could not possibly have reached.
//
// This matters more now that nothing else judges: the promotion task stops at
// git, so what this function says about a workload is what the release says.
func TestALastAttemptsVerdictIsNotThisAttemptsVerdict(t *testing.T) {
	deployment := func(generation, observed int64) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"generation": float64(generation)},
			"status": map[string]any{
				"observedGeneration": float64(observed),
				"conditions": []any{map[string]any{
					"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded",
					"message": `ReplicaSet "cart-67fb4b659" has timed out progressing.`,
				}},
			},
		}
	}

	// The spec has moved on and the controller has not caught up: the verdict
	// on show belongs to the attempt before this one.
	if failed, why := rolloutFailed("Deployment", deployment(7, 6)); failed {
		t.Errorf("failed a release on the previous attempt's condition: %s", why)
	}

	// Caught up: now it is this attempt being described, and it did fail.
	failed, why := rolloutFailed("Deployment", deployment(7, 7))
	if !failed {
		t.Fatal("a rollout Kubernetes has given up on must still fail the release")
	}
	if !strings.Contains(why, "ProgressDeadlineExceeded") {
		t.Errorf("the reason must survive to the release: %q", why)
	}

	// An object with no generation at all is taken at face value rather than
	// being treated as permanently in flight.
	bare := map[string]any{"status": map[string]any{"conditions": []any{map[string]any{
		"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded", "message": "gave up",
	}}}}
	if ok, _ := rolloutFailed("Deployment", bare); !ok {
		t.Error("without a generation to compare, status is all there is")
	}
}

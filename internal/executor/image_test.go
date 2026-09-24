package executor

import (
	"strings"
	"testing"

	"tide/internal/release"
)

// deploy builds a Deployment's object as Argo CD hands it over. want is
// spec.replicas; the rest is what its controller has reported back.
func deploy(image string, want, updated, ready, total, available int64) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"generation": float64(2)},
		"spec": map[string]any{
			"replicas": float64(want),
			"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"image": image}},
			}},
		},
		"status": map[string]any{
			"observedGeneration": float64(2),
			"updatedReplicas":    float64(updated),
			"readyReplicas":      float64(ready),
			"replicas":           float64(total),
			"availableReplicas":  float64(available),
		},
	}
}

const newImage = "registry.example.com/acme-dev/cart:20260924054551-32465c7e-0004"

var target = release.Artifact{Tag: "20260924054551-32465c7e-0004", Digest: "sha256:abc"}

// The judgement of whether a release worked is Tide's alone now: the
// promotion task stops at git. So this has to tell apart a container that is
// still starting from one that will never start, and neither of them from a
// rollout that finished.
//
// The dangerous shape is the middle one: a new image that exits seconds after
// it starts, while the previous pod stays up and healthy. Something that
// asked only "is a pod ready" would call that a success — the old pod is
// ready — and report a release that never reached anybody as done.
func TestARolloutIsNotDoneWhileTheOldPodIsStillCarryingIt(t *testing.T) {
	for name, tc := range map[string]struct {
		obj   map[string]any
		done  bool
		ok    bool
		about string
	}{
		"finished": {
			obj: deploy(newImage, 1, 1, 1, 1, 1), done: true, ok: true,
		},
		// One replica wanted, two exist: the new one is not ready and the old
		// one is still serving. Ready replicas is 1 either way.
		"new image crashlooping, old pod still up": {
			obj: deploy(newImage, 1, 1, 1, 2, 1), done: false, about: "updated",
		},
		"new pod still starting": {
			obj: deploy(newImage, 1, 1, 0, 1, 0), done: false, about: "ready",
		},
		// Scaled out: every replica must be on the new version, not just one.
		"half way through a larger rollout": {
			obj: deploy(newImage, 3, 2, 2, 3, 2), done: false, about: "updated",
		},
	} {
		got := rolloutStatus([]workload{{kind: "Deployment", name: "cart", obj: tc.obj}}, target)
		switch {
		case got.Done != tc.done:
			t.Errorf("%s: done=%v, want %v (%+v)", name, got.Done, tc.done, got)
		case tc.done && got.Success != tc.ok:
			t.Errorf("%s: success=%v, want %v", name, got.Success, tc.ok)
		case !tc.done && !strings.Contains(got.Waiting, tc.about):
			t.Errorf("%s: waiting on %q, expected it to mention %q", name, got.Waiting, tc.about)
		}
	}
}

// Kubernetes giving up is a different answer from Tide running out of
// patience, and it arrives sooner. It has to reach the release as a failure
// rather than as more waiting.
func TestKubernetesGivingUpEndsTheRelease(t *testing.T) {
	obj := deploy(newImage, 1, 1, 0, 1, 0)
	obj["status"].(map[string]any)["conditions"] = []any{map[string]any{
		"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded",
		"message": `ReplicaSet "cart-78757ff47" has timed out progressing.`,
	}}

	got := rolloutStatus([]workload{{kind: "Deployment", name: "cart", obj: obj}}, target)
	if !got.Done || got.Success {
		t.Fatalf("a rollout Kubernetes abandoned must fail the release: %+v", got)
	}
	if !strings.Contains(got.Error, "ProgressDeadlineExceeded") {
		t.Errorf("the reason must survive: %q", got.Error)
	}
}

// A sync that has not reached the cluster is not a finished rollout, however
// settled the workload looks: what is settled is the version before this one.
func TestAWorkloadStillOnTheOldVersionIsNotDone(t *testing.T) {
	old := deploy("registry.example.com/acme-dev/cart:20260901000000-aaaaaaaa-0001", 1, 1, 1, 1, 1)
	got := rolloutStatus([]workload{{kind: "Deployment", name: "cart", obj: old}}, target)
	if got.Done {
		t.Fatalf("nothing runs the target yet: %+v", got)
	}
	if !strings.Contains(got.Waiting, "no workload runs") {
		t.Errorf("it must say what is missing: %q", got.Waiting)
	}
}

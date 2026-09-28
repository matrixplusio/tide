package executor

import (
	"encoding/json"
	"testing"

	"tide/internal/release"
)

func hpa(t *testing.T, kind, name string) map[string]any {
	t.Helper()
	var m map[string]any
	raw := `{"kind":"HorizontalPodAutoscaler","spec":{"minReplicas":2,"scaleTargetRef":{"kind":"` + kind + `","name":"` + name + `"}}}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// An autoscaler in the same Application that points at something else is not
// this service's problem. Refusing on its account would block a change that is
// perfectly safe — and a check that cries wolf gets switched off.
func TestOnlyTheAutoscalerAimedAtThisWorkloadCounts(t *testing.T) {
	w := release.Workload{Kind: "Deployment", Name: "order-api"}
	if !scaleTargets(hpa(t, "Deployment", "order-api"), w) {
		t.Error("an autoscaler aimed at this workload was not recognised; the scale would have been undone within seconds")
	}
	if scaleTargets(hpa(t, "Deployment", "pay-center"), w) {
		t.Error("an autoscaler for another workload blocked the change")
	}
	if scaleTargets(hpa(t, "StatefulSet", "order-api"), w) {
		t.Error("an autoscaler for a different kind with the same name blocked the change")
	}
	var empty map[string]any
	if scaleTargets(empty, w) {
		t.Error("an object with no scaleTargetRef was treated as aimed at this workload")
	}
}

// Absent means one to Kubernetes. Reading it as zero would make Poll think a
// scale to zero had already arrived, and report success before anything moved.
func TestAnAbsentCountReadsAsOne(t *testing.T) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(`{"spec":{}}`), &obj); err != nil {
		t.Fatal(err)
	}
	if got := desiredReplicas(obj); got != 1 {
		t.Fatalf("absent replicas read as %d, want 1", got)
	}
	if err := json.Unmarshal([]byte(`{"spec":{"replicas":0}}`), &obj); err != nil {
		t.Fatal(err)
	}
	if got := desiredReplicas(obj); got != 0 {
		t.Fatalf("an explicit zero read as %d", got)
	}
}

// Scaling to zero is done when the pods are gone, not when the object says
// zero — it says so immediately, and the last pod is still terminating.
func TestScalingToZeroWaitsForThePodsToGo(t *testing.T) {
	gone := deploy("app:1", 0, 0, 0, 0, 0)
	if done, why := rolloutComplete("Deployment", gone); !done {
		t.Fatalf("zero replicas with no pods left is complete, got: %s", why)
	}
	stillThere := deploy("app:1", 0, 0, 0, 1, 0)
	if done, _ := rolloutComplete("Deployment", stillThere); done {
		t.Fatal("a pod that has not gone yet counted as done")
	}
}

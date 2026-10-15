package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

// Shapes copied from Argo Rollouts v1.10 objects read off a cluster: the
// generations are strings, a canary with traffic routing reports its weights,
// and a Rollout that adopts a Deployment carries no pod template of its own.

const (
	v1 = "registry.example.com/shop/order:20260101000000-aaaaaaaa-0001"
	v2 = "registry.example.com/shop/order:20260102000000-bbbbbbbb-0002"
)

var roTarget = release.Artifact{Tag: "20260102000000-bbbbbbbb-0002"}

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func deployment(t *testing.T, image string, generation int) map[string]any {
	return decode(t, `{"kind":"Deployment","metadata":{"name":"order","generation":`+itoa(generation)+`},
		"spec":{"replicas":0,"template":{"spec":{"containers":[{"name":"app","image":"`+image+`"}]}}}}`)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func canary(t *testing.T, status string) map[string]any {
	return decode(t, `{"kind":"Rollout","metadata":{"name":"order","generation":1},
		"spec":{"replicas":1,"workloadRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"order"},
			"strategy":{"canary":{"steps":[{"setWeight":20},{"pause":{}},{"setWeight":50},{"pause":{}}]}}},
		"status":`+status+`}`)
}

func blueGreen(t *testing.T, status string) map[string]any {
	return decode(t, `{"kind":"Rollout","metadata":{"name":"order","generation":1},
		"spec":{"replicas":1,"workloadRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"order"},
			"strategy":{"blueGreen":{"activeService":"order","previewService":"order-preview","autoPromotionEnabled":false}}},
		"status":`+status+`}`)
}

// The canary as Argo Rollouts reported it, stopped at its first pause with a
// fifth of the traffic on the new version.
const canaryPaused = `{"phase":"Paused","message":"CanaryPauseStep","observedGeneration":"1","workloadObservedGeneration":"4",
	"currentPodHash":"8b897866d","stableRS":"77f55d8c65","currentStepIndex":1,
	"pauseConditions":[{"reason":"CanaryPauseStep","startTime":"2026-10-15T05:42:27Z"}],"controllerPause":true,
	"canary":{"weights":{"canary":{"podTemplateHash":"8b897866d","weight":20},"stable":{"podTemplateHash":"77f55d8c65","weight":80}}},
	"replicas":2,"updatedReplicas":1,"readyReplicas":1}`

func TestACanaryStoppedAtAStepWaitsForADecision(t *testing.T) {
	st := judgeRollout(canary(t, canaryPaused), deployment(t, v2, 4), roTarget)
	if st.paused == nil || st.done || st.failed != "" {
		t.Fatalf("got %+v, want a pause", st)
	}
	p := st.paused
	if p.Strategy != "canary" || p.Step != 2 || p.Steps != 4 || p.Weight != 20 || p.Reason != "CanaryPauseStep" {
		t.Errorf("pause %+v, want canary step 2 of 4 at 20%%", p)
	}
	if want := time.Date(2026, 10, 15, 5, 42, 27, 0, time.UTC); !p.Since.Equal(want) {
		t.Errorf("since %s, want %s", p.Since, want)
	}
}

// Until the controller has seen the Deployment's new template, the pause it
// reports belongs to the version before: reading it as this release's wait
// would announce a decision about something that is not on screen.
func TestAPauseFromBeforeTheNewTemplateIsNotThisReleases(t *testing.T) {
	if st := judgeRollout(canary(t, canaryPaused), deployment(t, v2, 5), roTarget); st.paused != nil || st.onTarget {
		t.Errorf("a status observed for generation 4 was read for generation 5: %+v", st)
	}
	if st := judgeRollout(canary(t, canaryPaused), deployment(t, v1, 4), roTarget); st.paused != nil || st.onTarget {
		t.Errorf("a rollout still on the old image was taken to be this release's: %+v", st)
	}
}

func TestABlueGreenIsDoneOnlyOnceTrafficIsOnTheNewVersion(t *testing.T) {
	waiting := blueGreen(t, `{"phase":"Paused","observedGeneration":"1","workloadObservedGeneration":"4",
		"currentPodHash":"new","stableRS":"old","pauseConditions":[{"reason":"BlueGreenPause","startTime":"2026-10-15T06:00:00Z"}],
		"blueGreen":{"activeSelector":"old","previewSelector":"new"}}`)
	st := judgeRollout(waiting, deployment(t, v2, 4), roTarget)
	if st.paused == nil || st.paused.Strategy != "blueGreen" || st.paused.Weight != 0 {
		t.Fatalf("got %+v, want a blue-green pause with no traffic on the new version", st)
	}
	// Promoted but the service not yet pointed over: not done.
	switching := blueGreen(t, `{"phase":"Healthy","observedGeneration":"1","workloadObservedGeneration":"4",
		"currentPodHash":"new","stableRS":"new","blueGreen":{"activeSelector":"old","previewSelector":"new"}}`)
	if st := judgeRollout(switching, deployment(t, v2, 4), roTarget); st.done {
		t.Error("done while the active service still selects the old version")
	}
	switched := blueGreen(t, `{"phase":"Healthy","observedGeneration":"1","workloadObservedGeneration":"4",
		"currentPodHash":"new","stableRS":"new","blueGreen":{"activeSelector":"new","previewSelector":"new"}}`)
	if st := judgeRollout(switched, deployment(t, v2, 4), roTarget); !st.done {
		t.Errorf("not done after the switch: %+v", st)
	}
}

func TestAnAbortedOrDegradedRolloutFailsTheRelease(t *testing.T) {
	aborted := canary(t, `{"phase":"Degraded","abort":true,"message":"RolloutAborted: Rollout aborted update to revision 3",
		"observedGeneration":"1","workloadObservedGeneration":"4","currentPodHash":"new","stableRS":"old"}`)
	if st := judgeRollout(aborted, deployment(t, v2, 4), roTarget); st.failed == "" {
		t.Error("an aborted rollout did not fail the release")
	}
	degraded := canary(t, `{"phase":"Degraded","message":"ProgressDeadlineExceeded","observedGeneration":"1","workloadObservedGeneration":"4"}`)
	if st := judgeRollout(degraded, deployment(t, v2, 4), roTarget); st.failed == "" {
		t.Error("a degraded rollout did not fail the release")
	}
}

// Without traffic routing a canary splits by pods, and the weight shown is
// the last setWeight it reached.
func TestACanaryWithoutTrafficRoutingReportsItsLastSetWeight(t *testing.T) {
	ro := canary(t, `{"phase":"Paused","observedGeneration":"1","workloadObservedGeneration":"4","currentStepIndex":3,
		"pauseConditions":[{"reason":"CanaryPauseStep","startTime":"2026-10-15T06:00:00Z"}]}`)
	if p := pauseOf(ro); p == nil || p.Weight != 50 || p.Step != 4 {
		t.Errorf("got %+v, want step 4 at 50%%", p)
	}
}

func TestRolloutActionsAreAClosedSet(t *testing.T) {
	for action, want := range map[RolloutAction]string{ActionNext: "resume", ActionFull: "promote-full", ActionAbort: "abort"} {
		if got, ok := action.argoAction(); !ok || got != want {
			t.Errorf("%s maps to %q, want %q", action, got, want)
		}
	}
	if _, ok := RolloutAction("delete").argoAction(); ok {
		t.Error("an action outside the set was accepted")
	}
}

// A Rollout restarts pods without a new revision and records the restart it
// carried out; until then it is not done.
func TestARolloutRestartIsDoneWhenTheRolloutRecordsIt(t *testing.T) {
	asked := `"spec":{"replicas":1,"restartAt":"2026-10-15T07:00:00Z","strategy":{"canary":{}}}`
	running := decode(t, `{"kind":"Rollout","metadata":{"generation":2},`+asked+`,
		"status":{"phase":"Healthy","observedGeneration":"2","currentPodHash":"a","stableRS":"a","readyReplicas":1}}`)
	if done, _ := rolledOut(rolloutKind, running); done {
		t.Error("done before the rollout recorded the restart")
	}
	finished := decode(t, `{"kind":"Rollout","metadata":{"generation":2},`+asked+`,
		"status":{"phase":"Healthy","observedGeneration":"2","currentPodHash":"a","stableRS":"a","readyReplicas":1,"restartedAt":"2026-10-15T07:00:00Z"}}`)
	if done, why := rolledOut(rolloutKind, finished); !done {
		t.Errorf("not done after the restart was recorded: %s", why)
	}
}

// The Deployment a Rollout adopts is scaled to nothing and kept only for its
// template. Judged on its own, zero of zero replicas reads as rolled out, and
// a release would succeed before the canary had taken a step.
func TestAnAdoptedDeploymentIsNotJudgedOnItsOwn(t *testing.T) {
	objs := map[string]string{
		"Rollout/order":    `{"kind":"Rollout","metadata":{"name":"order","generation":1},"spec":{"workloadRef":{"apiVersion":"apps/v1","kind":"Deployment","name":"order"}}}`,
		"Deployment/order": `{"kind":"Deployment","metadata":{"name":"order","generation":4},"spec":{"replicas":0,"template":{"spec":{"containers":[{"image":"` + v2 + `"}]}}}}`,
		"Deployment/cron":  `{"kind":"Deployment","metadata":{"name":"cron","generation":1},"spec":{"replicas":1}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := objs[r.URL.Query().Get("kind")+"/"+r.URL.Query().Get("resourceName")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"manifest": raw})
	}))
	defer srv.Close()
	refs := []release.Workload{
		{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "shop", Name: "order"},
		{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "shop", Name: "cron"},
		{Group: rolloutGroup, Version: rolloutVersion, Kind: rolloutKind, Namespace: "shop", Name: "order"},
	}
	ws, err := observeWorkloads(context.Background(), argocd.New(srv.URL, "t", false), "shop-order", refs)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, w := range ws {
		kinds = append(kinds, w.kind+"/"+w.name)
		if w.kind == rolloutKind && !runsArtifact(w.template(), roTarget) {
			t.Error("the Rollout was not paired with the template of the Deployment it adopts")
		}
	}
	if len(ws) != 2 || kinds[0] != "Deployment/cron" || kinds[1] != "Rollout/order" {
		t.Errorf("workloads %v, want the unrelated Deployment and the Rollout only", kinds)
	}
}

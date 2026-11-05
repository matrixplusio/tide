package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

// Shapes as Argo CD v3.5 returns them for a blue-green Rollout two versions
// in: the active ReplicaSet, the previous one with the controller's deadline
// on it, and one from before that already scaled to nothing.

type fakeArgo struct {
	mu      sync.Mutex
	rs      map[string]string // name -> manifest
	actions []string          // "<kind>/<name>:<action>"
}

func (f *fakeArgo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/resource/actions/v2"):
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		f.actions = append(f.actions, m["kind"]+"/"+m["resourceName"]+":"+m["action"])
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	case strings.HasSuffix(r.URL.Path, "/resource-tree"):
		_, _ = w.Write([]byte(`{"nodes":[
			{"group":"argoproj.io","kind":"Rollout","namespace":"shop","name":"order"},
			{"group":"apps","kind":"ReplicaSet","namespace":"shop","name":"order-new","parentRefs":[{"kind":"Rollout","name":"order"}]},
			{"group":"apps","kind":"ReplicaSet","namespace":"shop","name":"order-old","parentRefs":[{"kind":"Rollout","name":"order"}]},
			{"group":"apps","kind":"ReplicaSet","namespace":"shop","name":"order-older","parentRefs":[{"kind":"Rollout","name":"order"}]},
			{"group":"apps","kind":"ReplicaSet","namespace":"shop","name":"cron-1","parentRefs":[{"kind":"Deployment","name":"cron"}]}
		]}`))
	case strings.HasSuffix(r.URL.Path, "/resource"):
		raw, ok := f.rs[r.URL.Query().Get("resourceName")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"manifest": raw})
	default:
		_, _ = w.Write([]byte(`{"metadata":{"name":"shop-order"},"status":{"resources":[
			{"group":"argoproj.io","version":"v1alpha1","kind":"Rollout","namespace":"shop","name":"order"}
		]}}`))
	}
}

func rsManifest(deadline string, replicas, ready int) string {
	ann := ""
	if deadline != "" {
		ann = fmt.Sprintf(`,"annotations":{"scale-down-deadline":%q}`, deadline)
	}
	return fmt.Sprintf(`{"metadata":{"name":"x"%s},"spec":{"replicas":%d,"template":{"spec":{"containers":[{"image":%q}]}}},"status":{"readyReplicas":%d}}`,
		ann, replicas, v1, ready)
}

func newFakeArgo(deadline string) (*fakeArgo, *catalog.Clients, func()) {
	f := &fakeArgo{rs: map[string]string{
		"order-new":   rsManifest("", 3, 3),
		"order-old":   rsManifest(deadline, 3, 3),
		"order-older": rsManifest("2026-01-01T00:00:00Z", 0, 0),
		"cron-1":      rsManifest(deadline, 1, 1), // not a Rollout's: never touched
	}}
	srv := httptest.NewServer(f)
	return f, &catalog.Clients{ArgoCD: argocd.New(srv.URL, "t", false)}, srv.Close
}

func TestOnlyTheMarkedPreviousVersionIsRetained(t *testing.T) {
	until := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	_, c, done := newFakeArgo(until.Format(time.RFC3339))
	defer done()
	kept, err := RetainedOf(context.Background(), c, "shop-order")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].ReplicaSet != "order-old" || kept[0].Rollout != "order" {
		t.Fatalf("want only order-old kept, got %+v", kept)
	}
	if !kept[0].Until.Equal(until) || len(kept[0].Images) != 1 || kept[0].Images[0] != v1 {
		t.Errorf("read wrong: %+v", kept[0])
	}
	if !kept[0].Usable(time.Now()) {
		t.Error("ready and before its deadline, so it can take traffic back")
	}
}

// Past its deadline, or with pods not all ready, the previous version is
// still there but cannot take the traffic back at once: a rollback then is
// not the switch the window promises.
func TestARetainedVersionIsUsableOnlyReadyAndInTime(t *testing.T) {
	now := time.Now()
	r := Retained{Until: now.Add(time.Minute), Replicas: 3, Ready: 3}
	if !r.Usable(now) {
		t.Error("in time and ready")
	}
	if r.Usable(now.Add(2 * time.Minute)) {
		t.Error("past the deadline")
	}
	r.Ready = 2
	if r.Usable(now) {
		t.Error("one pod not ready")
	}
}

func TestEndingTheWindowActsOnlyOnTheRetainedReplicaSet(t *testing.T) {
	f, c, done := newFakeArgo(time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339))
	defer done()
	n, err := EndObservation(context.Background(), c, "shop-order")
	if err != nil || n != 1 {
		t.Fatalf("ended %d, err %v", n, err)
	}
	if len(f.actions) != 1 || f.actions[0] != "ReplicaSet/order-old:scale-down-now" {
		t.Errorf("actions %v, want scale-down-now on order-old only", f.actions)
	}
}

func TestNoWindowToEndIsSaidSo(t *testing.T) {
	f, c, done := newFakeArgo("")
	defer done()
	if _, err := EndObservation(context.Background(), c, "shop-order"); !errors.Is(err, ErrNotObserving) {
		t.Errorf("want ErrNotObserving, got %v", err)
	}
	if len(f.actions) != 0 {
		t.Errorf("nothing to end, yet ran %v", f.actions)
	}
}

// The rollback's exemption from approval rests on the previous version still
// running; the executor checks that again just before promoting, and refuses
// rather than turning into a release that has to start new pods.
func TestARollbackIsRefusedOnceTheWindowHasClosed(t *testing.T) {
	now := time.Now()
	to := release.Artifact{Tag: "20260101000000-aaaaaaaa-0001"}
	ok := Retained{Until: now.Add(time.Minute), Replicas: 2, Ready: 2, Images: []string{v1}}
	if err := stillKept([]Retained{ok}, to, now); err != nil {
		t.Errorf("kept and ready: %v", err)
	}
	closed := ok
	closed.Until = now.Add(-time.Second)
	notReady := ok
	notReady.Ready = 1
	other := ok
	other.Images = []string{"registry.example.com/shop/order:other"}
	for name, kept := range map[string][]Retained{"nothing kept": nil, "past the deadline": {closed}, "a pod not ready": {notReady}, "another version": {other}} {
		if err := stillKept(kept, to, now); err == nil {
			t.Errorf("%s: want refused", name)
		}
	}
}

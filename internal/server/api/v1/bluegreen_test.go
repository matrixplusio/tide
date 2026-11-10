package v1

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tide/internal/executor"
	"tide/internal/i18n"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
)

// A rollback inside the window goes out without approval, so each condition
// that keeps it to "switch back to the version that was just replaced" is a
// test: losing any one of them would let an approval-free release put
// something else into an environment.

const (
	bgOld = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	bgNew = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func bgRelease(from *release.Artifact, to string, status release.ItemStatus) *release.Release {
	p, _ := json.Marshal(release.ImagePayload{Service: "order", Env: "uat", Project: "shop", Stage: "order-uat",
		Image: "registry.example.com/shop/order", From: from, To: release.Artifact{Digest: to, Tag: "v2"}})
	return &release.Release{ID: "R-1", Items: []release.Item{{ID: 1, Kind: release.KindImage, Status: status, Payload: p}}}
}

func bgKept(until time.Time, ready int64, images ...string) executor.Retained {
	return executor.Retained{Rollout: "order", ReplicaSet: "order-old", Until: until, Replicas: 2, Ready: ready, Images: images}
}

func wantKey(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Key != key {
		t.Errorf("want %s, got %v", key, err)
	}
}

func TestRollbackIsOfferedForTheVersionJustReplaced(t *testing.T) {
	now := time.Now()
	from := &release.Artifact{Digest: bgOld, Tag: "v1"}
	kept := []executor.Retained{bgKept(now.Add(5*time.Minute), 2, "registry.example.com/shop/order@"+bgOld)}
	offer, last, err := decideRollback(now, kept, bgRelease(from, bgNew, release.ItemSucceeded), "order", bgNew)
	if err != nil {
		t.Fatal(err)
	}
	if offer.To.Digest != bgOld || offer.ReleaseID != "R-1" || last == nil || !offer.Until.Equal(kept[0].Until) {
		t.Errorf("offer %+v", offer)
	}
}

func TestRollbackIsRefusedOutsideTheWindow(t *testing.T) {
	now := time.Now()
	from := &release.Artifact{Digest: bgOld, Tag: "v1"}
	rel := bgRelease(from, bgNew, release.ItemSucceeded)
	img := "registry.example.com/shop/order@" + bgOld

	cases := []struct {
		name    string
		kept    []executor.Retained
		rel     *release.Release
		running string
		key     i18n.Key
	}{
		{"nothing kept", nil, rel, bgNew, "r.noWindow"},
		{"past the deadline", []executor.Retained{bgKept(now.Add(-time.Second), 2, img)}, rel, bgNew, "r.windowNotUsable"},
		{"no pod ready", []executor.Retained{bgKept(now.Add(time.Minute), 0, img)}, rel, bgNew, "r.windowNotUsable"},
		{"two Rollouts", []executor.Retained{bgKept(now.Add(time.Minute), 2, img),
			{Rollout: "other", Until: now.Add(time.Minute), Replicas: 1, Ready: 1}}, rel, bgNew, "r.rollbackSeveral"},
		{"no release on record", []executor.Retained{bgKept(now.Add(time.Minute), 2, img)}, nil, bgNew, "r.rollbackNoRelease"},
		{"the release item did not succeed", []executor.Retained{bgKept(now.Add(time.Minute), 2, img)},
			bgRelease(from, bgNew, release.ItemFailed), bgNew, "r.rollbackNoRelease"},
		{"first deploy", []executor.Retained{bgKept(now.Add(time.Minute), 2, img)},
			bgRelease(nil, bgNew, release.ItemSucceeded), bgNew, "r.rollbackFirstDeploy"},
		{"something deployed since", []executor.Retained{bgKept(now.Add(time.Minute), 2, img)}, rel,
			"sha256:3333333333333333333333333333333333333333333333333333333333333333", "r.rollbackMovedOn"},
		{"kept pods run another version", []executor.Retained{bgKept(now.Add(time.Minute), 2,
			"registry.example.com/shop/order@sha256:4444444444444444444444444444444444444444444444444444444444444444")}, rel, bgNew, "r.rollbackNotRetained"},
		{"previous version known by tag only", []executor.Retained{bgKept(now.Add(time.Minute), 2, "registry.example.com/shop/order:v1")},
			bgRelease(&release.Artifact{Tag: "v1"}, bgNew, release.ItemSucceeded), bgNew, "r.rollbackNotRetained"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := decideRollback(now, c.kept, c.rel, "order", c.running)
			wantKey(t, err, c.key)
		})
	}
}

// Without a revision limit a Rollout keeps every version still in its window;
// the one to offer is the one running the version replaced, not the first.
func TestRollbackFindsTheRightOneAmongSeveralKept(t *testing.T) {
	now := time.Now()
	from := &release.Artifact{Digest: bgOld, Tag: "v1"}
	older := bgKept(now.Add(time.Minute), 2, "registry.example.com/shop/order@sha256:0000000000000000000000000000000000000000000000000000000000000000")
	older.ReplicaSet = "order-older"
	kept := []executor.Retained{older, bgKept(now.Add(4*time.Minute), 2, "registry.example.com/shop/order@"+bgOld)}
	offer, _, err := decideRollback(now, kept, bgRelease(from, bgNew, release.ItemSucceeded), "order", bgNew)
	if err != nil || offer.To.Digest != bgOld || !offer.Until.Equal(kept[1].Until) {
		t.Errorf("offer %+v, err %v", offer, err)
	}
}

// An autoscaler growing the Rollout during the window grows the kept version
// too; a pod of it still starting does not take the rollback away.
func TestRollbackIsOfferedWhileAKeptPodIsStarting(t *testing.T) {
	now := time.Now()
	from := &release.Artifact{Digest: bgOld, Tag: "v1"}
	kept := []executor.Retained{bgKept(now.Add(5*time.Minute), 1, "registry.example.com/shop/order@"+bgOld)}
	offer, _, err := decideRollback(now, kept, bgRelease(from, bgNew, release.ItemSucceeded), "order", bgNew)
	if err != nil || offer.To.Digest != bgOld {
		t.Errorf("offer %+v, err %v", offer, err)
	}
}

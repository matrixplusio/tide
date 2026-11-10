package executor

import (
	"strings"
	"testing"
	"time"

	"tide/internal/release"
)

const imageDiff = "@@ -20 +20 @@\n       containers:\n-        - image: registry.example.com/shop/order:20260101000000-aaaaaaaa-0001\n+        - image: registry.example.com/shop/order:20260102000000-bbbbbbbb-0002\n           name: order\n"

func change(action, kind, name, diff string) release.ResourceChange {
	return release.ResourceChange{Action: action, Kind: kind, Namespace: "shop", Name: name, Diff: diff}
}

// The operation Argo CD reports is only this release's sync if it started
// after the promotion finished. Reading an older one as the answer is how a
// release would be called done — or failed — on the strength of last week.
func TestOnlyAnOperationAfterThePromotionIsThisReleasesSync(t *testing.T) {
	finished := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	before, after := finished.Add(-time.Hour), finished.Add(5*time.Second)
	cases := []struct {
		name    string
		phase   string
		started *time.Time
		want    syncStep
	}{
		{"never synced", "", nil, syncStart},
		{"an old success says nothing", "Succeeded", &before, syncStart},
		{"an old failure says nothing", "Failed", &before, syncStart},
		{"an old operation still running is waited for", "Running", &before, syncWait},
		{"ours, running", "Running", &after, syncWait},
		{"ours, done", "Succeeded", &after, syncSettled},
		{"ours, failed", "Failed", &after, syncFailed},
		{"ours, errored", "Error", &after, syncFailed},
	}
	for _, c := range cases {
		if got, _ := nextSync(c.phase, "boom", c.started, finished); got != c.want {
			t.Errorf("%s: got step %d, want %d", c.name, got, c.want)
		}
	}
	if _, why := nextSync("Failed", "boom", &after, finished); !strings.Contains(why, "boom") {
		t.Errorf("a failed sync must say why, got %q", why)
	}
}

func TestAnImageOnlyChangeIsSyncedWithoutDeleting(t *testing.T) {
	prune, err := approveSync(nil, []release.ResourceChange{change(release.ChangeUpdate, "Deployment", "order", imageDiff)}, false)
	if err != "" || prune {
		t.Fatalf("got prune=%v err=%v, want a plain sync", prune, err)
	}
}

// The reason these environments are not synced automatically: a commit nobody
// reviewed must not ride along with a release.
func TestAChangeTheReleaseNeverShowedStopsTheSync(t *testing.T) {
	image := change(release.ChangeUpdate, "Deployment", "order", imageDiff)
	cases := map[string][]release.ResourceChange{
		"a new ConfigMap edit":        {image, change(release.ChangeUpdate, "ConfigMap", "order", "+a: b\n")},
		"a new deletion":              {image, change(release.ChangeDelete, "ConfigMap", "old", "")},
		"a workload edited elsewhere": {change(release.ChangeUpdate, "Deployment", "order", imageDiff+"+          value: debug\n")},
		"a workload nobody listed":    {image, change(release.ChangeCreate, "Deployment", "extra", "+kind: Deployment\n")},
	}
	for name, pending := range cases {
		if _, err := approveSync(nil, pending, true); err == "" {
			t.Errorf("%s: the sync was approved", name)
		}
	}
}

func TestAReviewedChangeThatMovedSinceStopsTheSync(t *testing.T) {
	recorded := []release.ResourceChange{change(release.ChangeUpdate, "ConfigMap", "order", "+a: b\n")}
	pending := []release.ResourceChange{change(release.ChangeUpdate, "ConfigMap", "order", "+a: c\n")}
	if _, err := approveSync(recorded, pending, true); err == "" {
		t.Fatal("a ConfigMap that changed after review was approved")
	}
	if _, err := approveSync(recorded, recorded, true); err != "" {
		t.Fatalf("the reviewed change itself was refused: %s", err)
	}
}

// Deleting takes two things: the deletion was on the release, and the creator
// chose to send the configuration along.
func TestDeletingNeedsToBeListedAndChosen(t *testing.T) {
	del := change(release.ChangeDelete, "ConfigMap", "old", "")
	image := change(release.ChangeUpdate, "Deployment", "order", imageDiff)
	recorded, pending := []release.ResourceChange{del}, []release.ResourceChange{image, del}

	if prune, err := approveSync(recorded, pending, true); err != "" || !prune {
		t.Errorf("listed and chosen: got prune=%v err=%v, want it deleted", prune, err)
	}
	if prune, err := approveSync(recorded, pending, false); err != "" || prune {
		t.Errorf("listed but not chosen: got prune=%v err=%v, want it left alone", prune, err)
	}
}

func TestANamespaceIsNeverDeleted(t *testing.T) {
	ns := release.ResourceChange{Action: release.ChangeDelete, Kind: "Namespace", Name: "shop"}
	if _, err := approveSync([]release.ResourceChange{ns}, []release.ResourceChange{ns}, true); err == "" {
		t.Fatal("deleting a namespace was approved")
	}
}

// Syncing before Argo CD has read the commit applies the commit before it.
func TestTheSyncWaitsUntilArgoHasReadTheCommit(t *testing.T) {
	target := release.Artifact{Tag: "20260102000000-bbbbbbbb-0002"}
	if seesTarget(nil, target) {
		t.Error("nothing pending read as the commit having arrived")
	}
	if seesTarget([]release.ResourceChange{change(release.ChangeUpdate, "ConfigMap", "order", target.Tag)}, target) {
		t.Error("a ConfigMap mentioning the tag read as the workload changing")
	}
	if !seesTarget([]release.ResourceChange{change(release.ChangeUpdate, "Deployment", "order", imageDiff)}, target) {
		t.Error("the workload changing to the target was not seen")
	}
}

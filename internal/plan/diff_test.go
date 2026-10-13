package plan

import (
	"strings"
	"testing"

	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

func TestManifestYAMLStripsClusterFields(t *testing.T) {
	got, err := manifestYAML(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"c","uid":"u","resourceVersion":"9","managedFields":[{}],
		"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{}"}},"data":{"a":"1"},"status":{"x":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, noise := range []string{"uid", "resourceVersion", "managedFields", "last-applied", "status", "annotations"} {
		if strings.Contains(got, noise) {
			t.Errorf("%q left in:\n%s", noise, got)
		}
	}
	if got, _ := manifestYAML("null"); got != "" {
		t.Errorf("null renders %q", got)
	}
}

func TestUnifiedDiff(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\n"
	after := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\n"
	got, truncated := unifiedDiff(before, after)
	want := "@@ -1 +1 @@\n a\n-b\n+B\n c\n d\n e\n@@ -11 +11 @@\n k\n l\n m\n+n\n"
	if got != want || truncated {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
	if d, _ := unifiedDiff("x\n", "x\n"); d != "" {
		t.Errorf("equal texts diff to %q", d)
	}
	if d, _ := unifiedDiff("", "k: v\n"); d != "@@ -1 +1 @@\n+k: v\n" {
		t.Errorf("create diff %q", d)
	}
	big := strings.Repeat("line that is long enough to add up quickly\n", 1400)
	if d, tr := unifiedDiff(big, big+"x\n"+big); !tr || len(d) > maxDiffBytes {
		t.Errorf("large diff not capped: %d bytes truncated=%v", len(d), tr)
	}
}

// Made with the real rendering and the real diff, not a hand-written one: a
// release that syncs for itself refuses a workload whose diff is anything
// more than its image, so a mistake about what such a diff looks like would
// refuse every release.
func TestOnlyImageChanged(t *testing.T) {
	deploy := func(image, first, replicas string) string {
		return `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"order","namespace":"shop"},
			"spec":{"replicas":` + replicas + `,"template":{"spec":{"containers":[
			{` + first + `"image":"` + image + `","name":"order","env":[{"name":"A","value":"1"}]}]}}}}`
	}
	diff := func(before, after string) string {
		r := argocd.ManagedResource{Kind: "Deployment", Name: "order", LiveState: before, NormalizedLiveState: before,
			PredictedLiveState: after, TargetState: after, Modified: true}
		ch, err := resourceChange(r, false)
		if err != nil || ch == nil {
			t.Fatalf("no change: %v", err)
		}
		return ch.Diff
	}
	const v1, v2 = "registry.example.com/shop/order:1", "registry.example.com/shop/order:2"

	if d := diff(deploy(v1, "", "2"), deploy(v2, "", "2")); !OnlyImageChanged(d) {
		t.Errorf("an image change was not recognised:\n%s", d)
	}
	// With a key sorting before it, image is no longer the list item's first line.
	if d := diff(deploy(v1, `"args":["x"],`, "2"), deploy(v2, `"args":["x"],`, "2")); !OnlyImageChanged(d) {
		t.Errorf("an image that is not its container's first key was not recognised:\n%s", d)
	}
	if d := diff(deploy(v1, "", "2"), deploy(v2, "", "9")); OnlyImageChanged(d) {
		t.Errorf("a replica change rode along as an image change:\n%s", d)
	}
	if d := diff(deploy(v1, "", "2"), deploy(v1, "", "9")); OnlyImageChanged(d) {
		t.Errorf("a change with no image in it counted as an image change:\n%s", d)
	}
	if OnlyImageChanged("") {
		t.Error("no diff counted as an image change")
	}
}

func TestResourceChange(t *testing.T) {
	cm := func(v string) string {
		return `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"c"},"data":{"k":"` + v + `"}}`
	}
	for name, tc := range map[string]struct {
		r      argocd.ManagedResource
		prune  bool
		action string
	}{
		"update":             {argocd.ManagedResource{Kind: "ConfigMap", Name: "c", LiveState: cm("1"), NormalizedLiveState: cm("1"), PredictedLiveState: cm("2"), TargetState: cm("2"), Modified: true}, false, release.ChangeUpdate},
		"unchanged":          {argocd.ManagedResource{Kind: "ConfigMap", Name: "c", LiveState: cm("1"), NormalizedLiveState: cm("1"), PredictedLiveState: cm("1"), TargetState: cm("1")}, false, ""},
		"create":             {argocd.ManagedResource{Kind: "ConfigMap", Name: "c", LiveState: "null", NormalizedLiveState: "null", PredictedLiveState: "null", TargetState: cm("2")}, false, release.ChangeCreate},
		"delete":             {argocd.ManagedResource{Kind: "ConfigMap", Name: "c", LiveState: cm("1"), NormalizedLiveState: cm("1"), TargetState: "null"}, true, release.ChangeDelete},
		"extra, not pruning": {argocd.ManagedResource{Kind: "ConfigMap", Name: "c", LiveState: cm("1"), NormalizedLiveState: cm("1"), TargetState: "null"}, false, ""},
	} {
		ch, err := resourceChange(tc.r, tc.prune)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case tc.action == "" && ch != nil:
			t.Errorf("%s: unexpected change %+v", name, ch)
		case tc.action != "" && (ch == nil || ch.Action != tc.action || ch.Diff == ""):
			t.Errorf("%s: got %+v, want %s with a diff", name, ch, tc.action)
		}
	}
}

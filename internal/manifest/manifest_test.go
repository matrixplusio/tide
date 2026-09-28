package manifest

import (
	"strings"
	"testing"
)

// The deployment manifests are maintained by people: they carry comments that
// say why a value is what it is. A change of one number has to read as a change
// of one number, or nobody reviews it — and an unreviewable diff to the file
// that decides what runs is worse than no automation.
const sample = `# 订单服务，dev
apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-api          # 和 Service 的 selector 对应
  labels:
    app: order-api
spec:
  replicas: 2              # 压测期间临时调到 6，记得调回来
  selector:
    matchLabels:
      app: order-api
  template:
    spec:
      containers:
        - name: app
          image: PLACEHOLDER
`

func TestChangingTheCountKeepsEverythingElse(t *testing.T) {
	d, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind() != "Deployment" || d.Name() != "order-api" {
		t.Fatalf("read kind=%q name=%q", d.Kind(), d.Name())
	}
	if n, explicit := d.Replicas(); n != 2 || !explicit {
		t.Fatalf("read replicas=%d explicit=%v", n, explicit)
	}
	was, err := d.SetReplicas(6)
	if err != nil {
		t.Fatal(err)
	}
	if was != 2 {
		t.Fatalf("reported the old count as %d", was)
	}
	out, err := d.String()
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{
		"# 订单服务，dev",
		"# 和 Service 的 selector 对应",
		"压测期间临时调到 6，记得调回来",
		"image: PLACEHOLDER",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q — the diff is no longer reviewable:\n%s", keep, out)
		}
	}
	if !strings.Contains(out, "replicas: 6") {
		t.Errorf("the count did not change:\n%s", out)
	}
	// A quoted count is a string to YAML and an error to Kubernetes.
	if strings.Contains(out, `replicas: "6"`) || strings.Contains(out, "replicas: '6'") {
		t.Errorf("the count was written as a string:\n%s", out)
	}
	// Key order is what the file had, not what a map would produce.
	if i, j := strings.Index(out, "metadata:"), strings.Index(out, "spec:"); i > j {
		t.Errorf("keys were reordered:\n%s", out)
	}
}

// Zero is a real count, and it is the one that matters most: it is how a
// service is taken out of service without deleting anything.
func TestZeroIsACountLikeAnyOther(t *testing.T) {
	d, _ := Parse(sample)
	if _, err := d.SetReplicas(0); err != nil {
		t.Fatal(err)
	}
	out, _ := d.String()
	if !strings.Contains(out, "replicas: 0") {
		t.Errorf("zero did not survive the round trip:\n%s", out)
	}
	d2, _ := Parse(out)
	if n, explicit := d2.Replicas(); n != 0 || !explicit {
		t.Errorf("zero read back as %d explicit=%v — an untouched manifest would look the same", n, explicit)
	}
}

// Absent means one to Kubernetes. Reading it as zero would make a manifest
// that never mentioned replicas look like a service somebody had stopped.
func TestAbsentMeansOneAndIsNotWritten(t *testing.T) {
	d, err := Parse("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: a\nspec:\n  selector: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if n, explicit := d.Replicas(); n != 1 || explicit {
		t.Fatalf("absent replicas read as %d explicit=%v", n, explicit)
	}
	if _, err := d.SetReplicas(3); err == nil {
		t.Error("a manifest with no replicas field was written to; its count comes from somewhere else and Tide would be fighting whatever that is")
	}
}

// Several documents in one file: setting the count on whichever came first is
// how the wrong workload gets scaled.
func TestSeveralDocumentsAreRefused(t *testing.T) {
	two := sample + "---\napiVersion: v1\nkind: Service\nmetadata:\n  name: order-api\n"
	if _, err := Parse(two); err == nil {
		t.Error("a multi-document file was accepted")
	}
}

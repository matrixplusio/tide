package v1

import (
	"strings"
	"testing"

	"tide/internal/catalog"
)

// An upstream that did not answer produces an empty list of Applications,
// which on its own is indistinguishable from an upstream that genuinely holds
// none. Argo CD decides what exists, so what it no longer has should go — but
// only when it said so. Pushing a catalog read through a broken connection
// deletes working pipelines, and with pruning on that reaches the cluster
// before anybody sees it.
//
// Only the upstream being pushed counts: each site's pipelines live apart,
// and one site's outage must not hold up the other's changes.
func TestNothingIsPushedWhileItsUpstreamIsUnreachable(t *testing.T) {
	ok := catalog.UpstreamStatus{Name: "onprem", ArgoCDOK: true}
	down := catalog.UpstreamStatus{Name: "gcp", ArgoCDOK: false,
		ArgoCDError: "dial tcp 10.0.0.1:443: connect: connection refused"}
	snap := &catalog.Snapshot{Upstreams: []catalog.UpstreamStatus{ok, down}}

	if got := unreachable(snap, "onprem"); got != "" {
		t.Errorf("the other site being down must not block this one: %q", got)
	}
	got := unreachable(snap, "gcp")
	if got == "" {
		t.Fatal("its own upstream down is enough: its services would look deleted")
	}
	// The message has to name which one and why, or whoever sees the refusal
	// has to go and find out before they can do anything about it.
	if !strings.Contains(got, "gcp") || !strings.Contains(got, "connection refused") {
		t.Errorf("the refusal must say which upstream and what happened: %q", got)
	}

	// No catalog at all is the same answer, not a push of nothing.
	if unreachable(nil, "onprem") == "" {
		t.Error("without a catalog there is nothing to push from")
	}
}

// Two upstreams pushing to one repository and branch must sit in directories
// neither of which contains the other, or a full push for one prunes the
// other's pipelines.
func TestPipelineEntriesMustNotOverlap(t *testing.T) {
	cases := map[[2]string]bool{
		{"", "gcp"}: true, {"idc", "gcp"}: false, {"idc", "idc"}: true,
		{"pipelines", "pipelines/gcp"}: true, {"pipelines/idc", "pipelines/gcp"}: false, {"gcp", "gcp-old"}: false,
	}
	for c, want := range cases {
		if got := pathsOverlap(c[0], c[1]); got != want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", c[0], c[1], got, want)
		}
	}
}

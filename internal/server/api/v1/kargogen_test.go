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
func TestNothingIsPushedWhileAnUpstreamIsUnreachable(t *testing.T) {
	ok := catalog.UpstreamStatus{Name: "onprem", ArgoCDOK: true}
	down := catalog.UpstreamStatus{Name: "gcp", ArgoCDOK: false,
		ArgoCDError: "dial tcp 10.0.0.1:443: connect: connection refused"}

	if got := unreachable(&catalog.Snapshot{Upstreams: []catalog.UpstreamStatus{ok}}); got != "" {
		t.Errorf("an upstream that answered must not block a push: %q", got)
	}

	got := unreachable(&catalog.Snapshot{Upstreams: []catalog.UpstreamStatus{ok, down}})
	if got == "" {
		t.Fatal("one upstream down is enough: its services would look deleted")
	}
	// The message has to name which one and why, or whoever sees the refusal
	// has to go and find out before they can do anything about it.
	if !strings.Contains(got, "gcp") || !strings.Contains(got, "connection refused") {
		t.Errorf("the refusal must say which upstream and what happened: %q", got)
	}

	// No catalog at all is the same answer, not a push of nothing.
	if unreachable(nil) == "" {
		t.Error("without a catalog there is nothing to push from")
	}
}

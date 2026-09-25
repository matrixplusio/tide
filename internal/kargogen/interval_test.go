package kargogen

import (
	"regexp"
	"testing"
	"time"

	"tide/internal/catalog"
	"tide/internal/ci"
)

// A warehouse's interval is the fallback for a notification that went
// missing, and a fallback that arrives after the waiting has stopped is not
// one. It was an hour against a wait of thirty minutes: four builds were
// recorded as "waited 30m and never found the image" while the image had
// been in the registry the whole time, because the only thing that could
// still have found it was not due for another half hour.
//
// Written against ci.DefaultWait rather than a number, so that changing
// either one has to be a decision about both.
func TestTheWarehouseLooksAgainBeforeTideGivesUp(t *testing.T) {
	snap := &catalog.Snapshot{Services: []catalog.Service{svc("order-api", "trade", dep("dev"))}}
	r := Generate(snap, envs("dev"), Options{})
	yaml := find(t, r, "trade/warehouses.yaml")

	m := regexp.MustCompile(`(?m)^\s*interval:\s*(\S+)`).FindStringSubmatch(yaml)
	if m == nil {
		t.Fatalf("no interval in:\n%s", yaml)
	}
	interval, err := time.ParseDuration(m[1])
	if err != nil {
		t.Fatalf("interval %q does not parse: %v", m[1], err)
	}
	if interval >= ci.DefaultWait {
		t.Fatalf("the warehouse looks again every %s but Tide gives up after %s — the fallback can never fire",
			interval, ci.DefaultWait)
	}
	// And comfortably inside it, so one missed look is not the end of it.
	if interval > ci.DefaultWait/2 {
		t.Errorf("interval %s leaves only one look inside a wait of %s", interval, ci.DefaultWait)
	}
}

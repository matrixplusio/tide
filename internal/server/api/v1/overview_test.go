package v1

import (
	"testing"

	"tide/internal/catalog"
)

func svc(domain string, envs map[string]*catalog.Deployment) catalog.Service {
	return catalog.Service{Domain: domain, Envs: envs}
}

func dep(sync, health string) *catalog.Deployment {
	return &catalog.Deployment{Sync: sync, Health: health}
}

// The installation this was written for prunes nothing, so almost every
// Application sits at OutOfSync forever while serving perfectly well. Counting
// that as "unexpected" made the headline number 178 of 179 — true, useless,
// and it hid the two that were actually down.
func TestFleetStateSeparatesNotServingFromDriftedFromGit(t *testing.T) {
	svcs := []catalog.Service{
		svc("base", map[string]*catalog.Deployment{"dev": dep("OutOfSync", "Healthy")}),
		svc("base", map[string]*catalog.Deployment{"dev": dep("OutOfSync", "Healthy")}),
		svc("admin", map[string]*catalog.Deployment{"dev": dep("OutOfSync", "Degraded")}),
		svc("admin", map[string]*catalog.Deployment{"dev": dep("Synced", "Healthy")}),
	}

	stats, unhealthy, drifted, domains := fleetState(svcs)
	if len(unhealthy) != 1 || unhealthy[0].Health != "Degraded" {
		t.Fatalf("only the one that is not serving belongs on the list: %+v", unhealthy)
	}
	if drifted != 3 {
		t.Fatalf("drift is counted, not listed: %d", drifted)
	}
	if domains != 2 {
		t.Fatalf("domains: %d", domains)
	}
	dev := stats["dev"]
	if dev.Services != 4 || dev.Unhealthy != 1 || dev.Drifted != 3 {
		t.Fatalf("per-environment line: %+v", dev)
	}
}

// An environment nothing has been onboarded to must not report a clean bill
// of health: there was nothing to check.
func TestFleetStateLeavesAnEmptyEnvironmentOutOfTheCounts(t *testing.T) {
	stats, unhealthy, drifted, _ := fleetState([]catalog.Service{
		svc("base", map[string]*catalog.Deployment{"dev": dep("Synced", "Healthy")}),
	})
	if stats["qa"] != nil {
		t.Fatalf("an environment with no services has no line: %+v", stats["qa"])
	}
	if len(unhealthy) != 0 || drifted != 0 {
		t.Fatalf("a healthy synced fleet reports nothing: %d %d", len(unhealthy), drifted)
	}
}

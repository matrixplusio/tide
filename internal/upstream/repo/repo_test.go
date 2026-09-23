package repo

import (
	"slices"
	"testing"
)

// Pruning is the dangerous half of a push: it deletes, in the same commit,
// with nothing in between for anybody to look at. The rule it follows has to
// be exact about what "under this directory" means.
func TestStaleDeletesOnlyWhatTheCallerOwns(t *testing.T) {
	existing := map[string]bool{
		"kargo/acme-base/project.yaml":     true,
		"kargo/acme-base/warehouses.yaml":  true,
		"kargo/acme-base/stages.yaml":      true, // no longer generated
		"kargo/acme-base-old/stages.yaml":  true, // a different directory
		"kargo/acme-admin/warehouses.yaml": true, // somebody else's domain
		"README.md":                        true,
	}
	files := []File{
		{Path: "kargo/acme-base/project.yaml"},
		{Path: "kargo/acme-base/warehouses.yaml"},
	}

	got := Stale(existing, files, []string{"kargo/acme-base"})
	if !slices.Equal(got, []string{"kargo/acme-base/stages.yaml"}) {
		t.Fatalf("regenerating one domain must touch only that domain: %v", got)
	}
}

// Naming no directory is what a caller does when it only knows part of the
// tree. It must delete nothing at all rather than guess.
func TestStaleWithoutAScopeDeletesNothing(t *testing.T) {
	existing := map[string]bool{"a/one.yaml": true, "b/two.yaml": true}
	if got := Stale(existing, nil, nil); got != nil {
		t.Fatalf("no scope must mean no deletions: %v", got)
	}
	if got := Stale(existing, nil, []string{}); got != nil {
		t.Fatalf("an empty scope must mean no deletions: %v", got)
	}
}

// Regenerating everything is how a directory that no longer has any services
// disappears; naming the repository root is how a caller says so.
func TestStaleAtTheRootRemovesAWholeDirectory(t *testing.T) {
	existing := map[string]bool{
		"acme-base/project.yaml": true,
		"acme-gone/project.yaml": true,
	}
	files := []File{{Path: "acme-base/project.yaml"}}
	got := Stale(existing, files, []string{""})
	if !slices.Equal(got, []string{"acme-gone/project.yaml"}) {
		t.Fatalf("a domain with nothing left must go: %v", got)
	}
}

// The order has to come from the paths, not from map iteration: two pushes of
// the same change should produce the same commit.
func TestStaleIsOrdered(t *testing.T) {
	existing := map[string]bool{"d/z.yaml": true, "d/a.yaml": true, "d/m.yaml": true}
	want := []string{"d/a.yaml", "d/m.yaml", "d/z.yaml"}
	for range 8 {
		if got := Stale(existing, nil, []string{"d"}); !slices.Equal(got, want) {
			t.Fatalf("got %v", got)
		}
	}
}

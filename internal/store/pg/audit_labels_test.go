package pg_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"tide/internal/release"
)

// Every audit action the server writes needs a name in the web client's
// actionLabel map, or the audit page shows users a raw key like
// "auth.login.failed". This test walks the Go sources for the action literals
// and checks the map covers them.
func TestAuditActionsHaveWebLabels(t *testing.T) {
	const labels = "../../../web/src/features/audit/labels.ts"
	src, err := os.ReadFile(labels)
	if err != nil {
		t.Fatalf("read %s: %v", labels, err)
	}
	labelled := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([a-z][a-z.]*)':`).FindAllStringSubmatch(string(src), -1) {
		labelled[m[1]] = true
	}
	if len(labelled) == 0 {
		t.Fatalf("%s: parsed no labels; has the file's shape changed?", labels)
	}

	for _, a := range writtenActions(t) {
		if !labelled[a] {
			t.Errorf("audit action %q has no label in %s", a, labels)
		}
	}
}

// writtenActions collects the action strings passed to audit writes, including
// the two that are built by appending a status. The actor argument may itself
// contain commas and parentheses (audit.Actor{...}), so the scan takes the
// first string literal of each call.
func writtenActions(t *testing.T) []string {
	t.Helper()
	// Write(...) on an audit store, the auditLocal helper in internal/auth, and
	// the sites that pick an action into a variable first.
	res := []*regexp.Regexp{
		regexp.MustCompile(`(?:Audit\.Write|audit\.Write|auditLocal)\((?:[^()]|\([^()]*\))*?"([a-z]+(?:\.[a-z]+)+)"`),
		regexp.MustCompile(`\baction\s*:?=\s*"([a-z]+(?:\.[a-z]+)+)"`),
	}
	seen := map[string]bool{}
	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, re := range res {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				seen[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// "release."+status and "item."+status, spelled out.
	for _, s := range []release.Status{release.Succeeded, release.Failed, release.Cancelled, release.Rejected} {
		seen["release."+string(s)] = true
	}
	for _, s := range []release.ItemStatus{release.ItemSucceeded, release.ItemFailed, release.ItemSkipped, release.ItemCancelled} {
		seen["item."+string(s)] = true
	}
	delete(seen, "release.") // the concatenations' literal prefixes
	delete(seen, "item.")
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	if len(out) < 30 {
		t.Fatalf("found only %d actions; the scan is probably broken", len(out))
	}
	return out
}

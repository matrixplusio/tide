package release_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"tide/internal/release"
)

// The web UI keeps its own map from anomaly code to a short label, and a code
// it does not know renders as the bare word "anomaly" — notable, but silent
// about what. That is how first_deploy_per_kargo reached people as "异常"
// while its twin said "首次部署" beside it.
//
// The map cannot import these constants, so the check runs the other way: a
// code added here has to appear there.
func TestEveryAnomalyCodeHasALabelInTheUI(t *testing.T) {
	codes := []string{
		release.AnomalyConfigDrift,
		release.AnomalyFirstDeploy,
		release.AnomalyFirstDeployPerKargo,
		// Not constants in this package, but the same contract holds.
		"rollback", "multi_version_jump", "short_soak",
	}
	const page = "../../web/src/features/releases/ReleaseDetailPage.tsx"
	src, err := os.ReadFile(page)
	if err != nil {
		t.Skipf("frontend not present: %v", err)
	}
	block := regexp.MustCompile(`(?s)ANOMALY_SHORT[^=]*=\s*\{(.*?)\n\}`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("could not find ANOMALY_SHORT in %s — if it was renamed, update this test", page)
	}
	m := string(block[1])
	for _, code := range codes {
		if !strings.Contains(m, code+":") {
			t.Errorf("anomaly %q has no label in ANOMALY_SHORT; it will show as the generic word", code)
		}
	}
}

// The UI decides which anomalies are notices from its own list, because a
// release stored before the distinction existed carries no flag to read. Two
// lists is how the label for first_deploy_per_kargo went missing in the
// first place, so they are held together here.
func TestTheUIAgreesOnWhichAnomaliesAreNotices(t *testing.T) {
	const helper = "../../web/src/lib/anomaly.ts"
	src, err := os.ReadFile(helper)
	if err != nil {
		t.Skipf("frontend not present: %v", err)
	}
	set := regexp.MustCompile(`NOTICE = new Set\(\[(.*?)\]\)`).FindSubmatch(src)
	if set == nil {
		t.Fatalf("could not find the NOTICE set in %s — if it was renamed, update this test", helper)
	}
	inUI := map[string]bool{}
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(set[1], -1) {
		inUI[string(m[1])] = true
	}
	for code := range release.NoticeAnomalies {
		if !inUI[code] {
			t.Errorf("%q is a notice here but a warning in the UI", code)
		}
		delete(inUI, code)
	}
	for code := range inUI {
		t.Errorf("%q is a notice in the UI but a warning here", code)
	}
}

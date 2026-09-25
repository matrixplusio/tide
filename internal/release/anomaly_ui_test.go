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

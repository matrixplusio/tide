package version

import "testing"

func TestGetFallsBackToDev(t *testing.T) {
	// Nothing was linked in for `go test`, so the build must say so rather
	// than claim a version it does not have.
	if got := Get(); got.Version != "dev" {
		t.Errorf("version = %q, want dev for an unlinked build", got.Version)
	}
}

func TestShortAndString(t *testing.T) {
	for name, tc := range map[string]struct {
		in         Info
		wantShort  string
		wantInLong []string
	}{
		"release": {
			in:         Info{Version: "v0.0.1-beta.1", Commit: "a1b2c3d4e5f6", Date: "2026-09-22T04:00:00Z", Go: "go1.26.0", Platform: "linux/amd64"},
			wantShort:  "v0.0.1-beta.1 (a1b2c3d)",
			wantInLong: []string{"tide v0.0.1-beta.1", "a1b2c3d4e5f6", "2026-09-22T04:00:00Z", "linux/amd64"},
		},
		"dirty tree": {
			in:         Info{Version: "dev", Commit: "a1b2c3d4e5f6", Modified: true, Go: "go1.26.0", Platform: "darwin/arm64"},
			wantShort:  "dev (a1b2c3d-dirty)",
			wantInLong: []string{"(modified)"},
		},
		"no vcs": {
			in:         Info{Version: "dev", Go: "go1.26.0", Platform: "linux/arm64"},
			wantShort:  "dev",
			wantInLong: []string{"tide dev", "go1.26.0"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.in.Short(); got != tc.wantShort {
				t.Errorf("Short() = %q, want %q", got, tc.wantShort)
			}
			long := tc.in.String()
			for _, want := range tc.wantInLong {
				if !contains(long, want) {
					t.Errorf("String() = %q, missing %q", long, want)
				}
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

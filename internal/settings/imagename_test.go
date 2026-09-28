package settings

import "testing"

// The entry name is not something a person configures: empty means the
// literal the deployment repository leaves in every manifest, and a
// promotion generated with anything else appends a second entry instead of
// changing the first.
func TestImageEntryDefaultsToTheRepositoryPlaceholder(t *testing.T) {
	if got := (PipelineRepo{}).ImageEntry(); got != DefaultImageName {
		t.Fatalf("empty must mean %q, got %q", DefaultImageName, got)
	}
	if got := (PipelineRepo{ImageName: "app"}).ImageEntry(); got != "app" {
		t.Fatalf("a set name must win, got %q", got)
	}
}

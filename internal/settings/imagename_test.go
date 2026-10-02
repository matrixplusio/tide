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

// One Kargo reads one repository, so each upstream's pipeline repository is
// its own. A configuration written before there was a second upstream has no
// Name and keeps serving the first; an upstream nobody configured gets
// nothing, never another site's repository.
func TestPipelineRepoForUpstream(t *testing.T) {
	p := PipelineRepo{BaseURL: "https://git.example.com", Project: "ops/pipelines", Token: "t", PathPrefix: "idc",
		Others: []PipelineRepo{{Name: "gcp", BaseURL: "https://git.example.com", Project: "ops/pipelines", Token: "t2", PathPrefix: "gcp"}}}
	if got := p.For("idc", "idc"); got.PathPrefix != "idc" || got.Others != nil || !got.Configured() {
		t.Fatalf("legacy entry for the first upstream: %+v", got)
	}
	if got := p.For("gcp", "idc"); got.PathPrefix != "gcp" || got.Token != "t2" {
		t.Fatalf("second upstream: %+v", got)
	}
	if got := p.For("other", "idc"); got.Configured() {
		t.Fatalf("an unconfigured upstream must get nothing: %+v", got)
	}
	named := p
	named.Name = "gcp-first"
	if got := named.For("idc", "idc"); got.Configured() {
		t.Fatalf("a named entry serves only its own upstream: %+v", got)
	}
}

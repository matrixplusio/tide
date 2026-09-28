package v1

import (
	"reflect"
	"testing"

	"tide/internal/kargogen"
)

// The scope a push is allowed to delete inside. Only a generation with no
// filter at all owns the whole tree; a filter by line without a domain used to
// look like "no domain" and would have handed the whole tree to a push that
// generated one project — with pruning on, every other project would have
// been gone from the cluster seconds later.
func TestPruneScopeOwnsOnlyWhatWasGenerated(t *testing.T) {
	res := kargogen.Result{Domains: []kargogen.Domain{{Name: "kargo-acme-shop"}}}
	cases := map[string]struct {
		domain, project string
		want            []string
	}{
		"nothing filtered: the whole tree":   {"", "", []string{"pipelines"}},
		"a domain: its projects":             {"shop", "", []string{"pipelines/kargo-acme-shop"}},
		"a line: its projects, not the tree": {"", "acme", []string{"pipelines/kargo-acme-shop"}},
		"both: the one project":              {"shop", "acme", []string{"pipelines/kargo-acme-shop"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pruneScope("pipelines", c.domain, c.project, res); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
	if got := pruneScope("", "", "acme", res); !reflect.DeepEqual(got, []string{"kargo-acme-shop"}) {
		t.Errorf("no prefix: got %v", got)
	}
}

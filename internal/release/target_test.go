package release

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every kind a release can carry must resolve to a target: confirming a
// release walks its items through Target, and a kind the switch does not
// know fails there with "unsupported kind" — after the release was created,
// after the person read the sheet, as a 500 on the confirm button. That is
// how the replica-count change shipped: every other place that dispatches on
// kind had its case, this one did not, and nothing pinned the set.
func TestEveryKindHasATarget(t *testing.T) {
	payloads := map[string]any{
		KindImage:   ImagePayload{Upstream: "u", Service: "s", Env: "dev", To: Artifact{Digest: "sha256:to"}},
		KindRestart: RestartPayload{Upstream: "u", Service: "s", Env: "dev", Current: Artifact{Digest: "sha256:cur"}},
		KindSync:    SyncPayload{Upstream: "u", Service: "s", Env: "dev", Current: Artifact{Digest: "sha256:cur"}},
		KindScale:   ScalePayload{Upstream: "u", Service: "s", Env: "dev", From: 2, To: 3, Current: Artifact{Digest: "sha256:cur"}},
	}
	for _, kind := range Kinds {
		p, ok := payloads[kind]
		if !ok {
			t.Fatalf("kind %q has no fixture here; add one, this test is the pin", kind)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		r := &Release{}
		got, err := r.Target(Item{ID: 1, Kind: kind, Payload: raw})
		if err != nil {
			if strings.Contains(err.Error(), "unsupported kind") {
				t.Errorf("kind %q: Target does not know it", kind)
				continue
			}
			t.Fatalf("kind %q: %v", kind, err)
		}
		if got.Upstream != "u" || got.Service != "s" || got.Env != "dev" || got.Digest == "" {
			t.Errorf("kind %q: target %+v", kind, got)
		}
	}
}

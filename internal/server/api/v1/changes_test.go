package v1

import (
	"encoding/json"
	"testing"

	"tide/internal/release"
	"tide/internal/store/pg"
)

// Two builds each carry their last commits, newest first. The running
// image's commit found in the target's history means everything above it is
// new; found the other way round means a rollback; found in neither means
// the two are further apart than the history sent.
func TestChangesForReadsTheTwoHistories(t *testing.T) {
	hist := func(shas ...string) []pg.CICommit {
		out := make([]pg.CICommit, 0, len(shas))
		for _, s := range shas {
			out = append(out, pg.CICommit{ID: s, Title: "t-" + s})
		}
		return out
	}
	rel := func(fromDigest, toDigest string) *release.Release {
		p, _ := json.Marshal(release.ImagePayload{Service: "s", Env: "dev", From: &release.Artifact{Digest: fromDigest}, To: release.Artifact{Digest: toDigest}})
		return &release.Release{Items: []release.Item{{ID: 7, Kind: release.KindImage, Payload: p}}}
	}
	builds := map[string]pg.CIBuild{
		"old":  {Commit: "c2", Commits: hist("c2", "c1", "c0"), Repo: "https://git.example.com/acme/order-api"},
		"new":  {Commit: "c5", Commits: hist("c5", "c4", "c3", "c2", "c1"), Repo: "https://git.example.com/acme/order-api"},
		"far":  {Commit: "c9", Commits: hist("c9", "c8"), Repo: "https://git.example.com/acme/order-api"},
		"bare": {Commit: "c7"},
	}

	fwd := changesFor(rel("old", "new"), builds)[7]
	if fwd.Direction != "forward" || len(fwd.Commits) != 3 || fwd.Commits[0].ID != "c5" || fwd.Commits[2].ID != "c3" {
		t.Errorf("forward: %+v", fwd)
	}
	if fwd.Commits[0].URL != "https://git.example.com/acme/order-api/-/commit/c5" || fwd.Commits[0].ShortID != "c5" {
		t.Errorf("link/short id: %+v", fwd.Commits[0])
	}

	back := changesFor(rel("new", "old"), builds)[7]
	if back.Direction != "rollback" || len(back.Commits) != 3 || back.Commits[0].ID != "c5" {
		t.Errorf("rollback: %+v", back)
	}

	same := changesFor(rel("new", "new"), builds)[7]
	if same.Direction != "same" || len(same.Commits) != 0 || same.Note != "" {
		t.Errorf("same: %+v", same)
	}

	if got := changesFor(rel("old", "far"), builds)[7]; got.Note != "tooFar" {
		t.Errorf("beyond the window: %+v", got)
	}
	if got := changesFor(rel("old", "bare"), builds)[7]; got.Note != "noHistory" {
		t.Errorf("no history sent: %+v", got)
	}
	if got := changesFor(rel("old", "unknown"), builds)[7]; got.Note != "noBuild" {
		t.Errorf("unknown image: %+v", got)
	}
}

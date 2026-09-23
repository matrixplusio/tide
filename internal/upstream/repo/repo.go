// Package repo is the little that Tide needs from a git host: put these
// files in that branch, in one commit.
//
// The hosts disagree about almost everything underneath — GitLab wants plain
// text and no upsert, Gitea wants base64 and the blob SHA of anything it is
// replacing, and they create branches differently — so the difference is kept
// inside each client rather than leaking into the caller as a provider
// switch in the middle of a handler.
package repo

import (
	"context"
	"sort"
	"strings"
)

// File is one file to write, with its full path inside the repository.
type File struct {
	Path    string
	Content string
}

// Stale returns the paths under one of the prune directories that files does
// not account for: what a push has to delete to leave the repository saying
// only what was generated this time.
//
// Shared by the clients because getting it wrong is the dangerous half of
// pruning, not the API calls: a prefix that matches more than the caller
// meant deletes somebody else's directory, in the same commit, with no
// intermediate state anyone could have reviewed.
func Stale(existing map[string]bool, files []File, prune []string) []string {
	if len(prune) == 0 {
		return nil
	}
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f.Path] = true
	}
	var out []string
	for path := range existing {
		if keep[path] || !within(path, prune) {
			continue
		}
		out = append(out, path)
	}
	sort.Strings(out) // a commit's contents should not depend on map order
	return out
}

// within says whether path is inside one of the directories. Compared a
// segment at a time: "kargo/acme-base" must not be taken to contain
// "kargo/acme-base-old/warehouses.yaml".
func within(path string, dirs []string) bool {
	for _, d := range dirs {
		d = strings.Trim(d, "/")
		if d == "" {
			return true // the whole repository
		}
		if strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}

// Commit is what a push produced, as much of it as is worth showing.
type Commit struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id,omitempty"`
	URL     string `json:"web_url,omitempty"`
}

// Identity is who a token belongs to. Commits land under this name, so it is
// worth showing before anything is pushed: a token is an opaque string, and
// "which account am I about to commit as" is not answerable by looking at it.
type Identity struct {
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
	// Project is the repository the token was checked against, as the host
	// spells it back.
	Project string `json:"project,omitempty"`
	// CanWrite is what actually matters: a token that can read the project
	// and not write it fails at the commit, long after it was accepted.
	CanWrite bool `json:"canWrite"`
}

// Pusher writes files to a branch in a single commit.
//
// One commit, not one per file: a push that fails halfway would leave the
// repository describing a pipeline that half exists, and the half that exists
// is the half Argo CD applies.
type Pusher interface {
	// Push commits files to branch, creating the branch from the default one
	// when it is not there yet. An empty branch means the default branch.
	//
	// prune lists directories the caller is authoritative over: anything
	// under them that is not in files is deleted in the same commit. Without
	// it a generator that stops emitting a file leaves the old one behind
	// forever, and Argo CD goes on applying it — a warehouse subscribed to
	// the wrong repository keeps creating freight nobody asked for. Deleting
	// in the same commit is what keeps the repository from ever describing
	// both the old shape and the new one.
	//
	// An empty prune deletes nothing. A caller that regenerates one directory
	// must name that directory and not its parent, or it will delete every
	// sibling it did not generate this time.
	Push(ctx context.Context, branch, message string, files []File, prune []string) (*Commit, error)

	// Whoami reports who the token belongs to and whether it may write the
	// configured project, so that answer arrives when the settings are saved
	// rather than when somebody presses push.
	Whoami(ctx context.Context) (*Identity, error)
}

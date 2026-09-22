// Package repo is the little that Tide needs from a git host: put these
// files in that branch, in one commit.
//
// The hosts disagree about almost everything underneath — GitLab wants plain
// text and no upsert, Gitea wants base64 and the blob SHA of anything it is
// replacing, and they create branches differently — so the difference is kept
// inside each client rather than leaking into the caller as a provider
// switch in the middle of a handler.
package repo

import "context"

// File is one file to write, with its full path inside the repository.
type File struct {
	Path    string
	Content string
}

// Commit is what a push produced, as much of it as is worth showing.
type Commit struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id,omitempty"`
	URL     string `json:"web_url,omitempty"`
}

// Pusher writes files to a branch in a single commit.
//
// One commit, not one per file: a push that fails halfway would leave the
// repository describing a pipeline that half exists, and the half that exists
// is the half Argo CD applies.
type Pusher interface {
	// Push commits files to branch, creating the branch from the default one
	// when it is not there yet. An empty branch means the default branch.
	Push(ctx context.Context, branch, message string, files []File) (*Commit, error)
}

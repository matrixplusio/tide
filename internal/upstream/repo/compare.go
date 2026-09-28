package repo

import (
	"context"
	"errors"
	"time"
)

// Change is one commit between two images of a service, as the page shows
// it: enough to read, and a link for the rest.
type Change struct {
	ID      string    `json:"id"`
	ShortID string    `json:"shortId"`
	Title   string    `json:"title"`
	Author  string    `json:"author,omitempty"`
	At      time.Time `json:"at"`
	URL     string    `json:"url,omitempty"`
}

// Reader is the read-only side of a host: whether the token works, and the
// commits between two revisions of a project. Deliberately not Pusher —
// this credential must never be handed anything that writes.
type Reader interface {
	Ping(ctx context.Context) error
	Compare(ctx context.Context, project, from, to string) ([]Change, error)
}

// ErrCompareUnsupported is a host this was not written for yet. The page
// shows the builds and no list, which is the pre-Compare behaviour.
var ErrCompareUnsupported = errors.New("comparing commits is not supported on this host")

// MaxChanges caps a comparison: past this many commits the list is a
// scroll, not a read, and the number is what matters.
const MaxChanges = 50

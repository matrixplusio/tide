package repo

import "time"

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

// MaxChanges caps a comparison: past this many commits the list is a
// scroll, not a read, and the number is what matters. It is also how much
// history a pipeline sends with each build.
const MaxChanges = 50

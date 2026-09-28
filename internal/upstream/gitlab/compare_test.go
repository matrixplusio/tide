package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tide/internal/upstream/repo"
)

// The compare endpoint answers oldest first; the page wants that order, the
// fields a person reads, and no more than repo.MaxChanges of them.
func TestCompareReadsCommitsOldestFirstAndCaps(t *testing.T) {
	var gotPath, gotQuery string
	n := 3
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-ro" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		commits := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			commits = append(commits, map[string]any{
				"id": fmt.Sprintf("%040d", i), "short_id": fmt.Sprintf("%08d", i), "title": fmt.Sprintf("change %d", i),
				"author_name": "dev", "created_at": "2026-09-29T10:00:00Z", "web_url": fmt.Sprintf("https://git.example.com/acme/order-api/-/commit/%d", i),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"commits": commits, "compare_same_ref": false})
	}))
	defer srv.Close()

	c := New(srv.URL, "", "glpat-ro", false)
	got, err := c.Compare(context.Background(), "acme/order-api", "aaa", "bbb")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/projects/acme%2Forder-api/repository/compare") {
		t.Errorf("path: %s", gotPath)
	}
	if !strings.Contains(gotQuery, "from=aaa") || !strings.Contains(gotQuery, "to=bbb") {
		t.Errorf("query: %s", gotQuery)
	}
	if len(got) != 3 || got[0].Title != "change 0" || got[2].ShortID != "00000002" || got[1].Author != "dev" || got[0].URL == "" {
		t.Errorf("commits: %+v", got)
	}

	// Sixty commits: the newest fifty are kept, because a list that long is
	// read from its end.
	n = 60
	got, err = c.Compare(context.Background(), "acme/order-api", "aaa", "ccc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != repo.MaxChanges || got[0].Title != "change 10" || got[len(got)-1].Title != "change 59" {
		t.Errorf("cap: %d commits, first %q, last %q", len(got), got[0].Title, got[len(got)-1].Title)
	}
}

func TestPingIsJustTheTokenCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"username":"ro"}`))
	}))
	defer srv.Close()
	if err := New(srv.URL, "", "x", false).Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

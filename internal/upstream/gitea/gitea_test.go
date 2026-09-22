package gitea

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tide/internal/upstream/repo"
)

// A Gitea server that answers the four calls a push makes. tree is what the
// branch already contains, path → blob id.
func fake(t *testing.T, branchExists bool, tree map[string]string, body *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/contents"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, body)
			_, _ = w.Write([]byte(`{"commit":{"sha":"abcdef1234567890","html_url":"https://gitea/c/abcdef"}}`))
		case strings.Contains(r.URL.Path, "/branches/"):
			if !branchExists {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"branch does not exist"}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"kargo"}`))
		case strings.Contains(r.URL.Path, "/git/trees/"):
			var entries []string
			for path, sha := range tree {
				entries = append(entries, `{"path":"`+path+`","type":"blob","sha":"`+sha+`"}`)
			}
			entries = append(entries, `{"path":"trade","type":"tree","sha":"dir"}`)
			_, _ = w.Write([]byte(`{"tree":[` + strings.Join(entries, ",") + `]}`))
		default:
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		}
	}))
}

// Gitea takes file contents base64-encoded and refuses an update that does
// not quote the blob id of what it is replacing. Both are easy to get wrong
// and neither fails loudly in a way that points at the cause — the commit is
// simply rejected.
func TestPushEncodesContentAndQuotesTheBlobItReplaces(t *testing.T) {
	var body map[string]any
	srv := fake(t, true, map[string]string{"trade/project.yaml": "blob-1"}, &body)
	defer srv.Close()

	commit, err := New(srv.URL, "tide/k8s-apps", "tok", false).Push(context.Background(), "kargo", "m", []repo.File{
		{Path: "trade/project.yaml", Content: "kind: Project"},
		{Path: "trade/stages.yaml", Content: "kind: Stage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.ID != "abcdef1234567890" || commit.ShortID != "abcdef12" || commit.URL == "" {
		t.Fatalf("commit: %+v", commit)
	}

	byPath := map[string]map[string]any{}
	for _, f := range body["files"].([]any) {
		m := f.(map[string]any)
		byPath[m["path"].(string)] = m
	}

	up := byPath["trade/project.yaml"]
	if up["operation"] != "update" || up["sha"] != "blob-1" {
		t.Errorf("replacing an existing file: operation=%v sha=%v", up["operation"], up["sha"])
	}
	// Base64, not the text itself.
	if got, _ := base64.StdEncoding.DecodeString(up["content"].(string)); string(got) != "kind: Project" {
		t.Errorf("content did not survive encoding: %q", up["content"])
	}

	nw := byPath["trade/stages.yaml"]
	if nw["operation"] != "create" {
		t.Errorf("a new file was sent as %v", nw["operation"])
	}
	if _, ok := nw["sha"]; ok {
		t.Error("a create quoted a blob id; there is nothing to replace")
	}

	// Writing into a branch that is already there names only the base.
	if body["branch"] != "kargo" {
		t.Errorf("base branch: %v", body["branch"])
	}
	if _, ok := body["new_branch"]; ok {
		t.Error("new_branch was sent for a branch that exists")
	}
}

// Creating the review branch on the first push: Gitea names the base and the
// new branch together, unlike GitLab's start_branch.
func TestPushCreatesTheBranchFromTheDefault(t *testing.T) {
	var body map[string]any
	srv := fake(t, false, nil, &body)
	defer srv.Close()

	if _, err := New(srv.URL, "tide/k8s-apps", "tok", false).Push(context.Background(), "kargo", "m",
		[]repo.File{{Path: "a.yaml", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if body["branch"] != "main" || body["new_branch"] != "kargo" {
		t.Fatalf("branch=%v new_branch=%v", body["branch"], body["new_branch"])
	}
	if f := body["files"].([]any)[0].(map[string]any); f["operation"] != "create" {
		t.Errorf("operation %v on a branch that did not exist", f["operation"])
	}
}

// "owner/repo" is two path segments here, not one escaped one: getting this
// wrong produces 404s that look like a missing repository.
func TestProjectSplitsIntoOwnerAndName(t *testing.T) {
	c := New("https://gitea", "tide/k8s-apps", "t", false)
	if c.Owner != "tide" || c.Name != "k8s-apps" {
		t.Fatalf("owner=%q name=%q", c.Owner, c.Name)
	}
	if _, err := New("https://gitea", "k8s-apps", "t", false).Push(context.Background(), "b", "m",
		[]repo.File{{Path: "a", Content: "b"}}); err == nil {
		t.Fatal("a project without an owner was accepted")
	}
}

func TestPushRefusesAnEmptyCommit(t *testing.T) {
	if _, err := New("https://gitea", "o/r", "t", false).Push(context.Background(), "b", "m", nil); err == nil {
		t.Fatal("an empty commit was accepted")
	}
}

package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tide/internal/upstream/repo"
)

// The whole point of using the Commits API is that a push either lands
// completely or not at all. If the files went one request at a time, a
// failure halfway would leave a repository describing a pipeline that half
// exists — and the half that exists would be applied by Argo CD.
func TestPushSendsOneCommitForEveryFile(t *testing.T) {
	var gotPath, gotToken string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// EscapedPath, not Path: the latter is already decoded, so the escaping
		// that lets a project's namespace live in one segment is invisible there.
		gotPath, gotToken = r.URL.EscapedPath(), r.Header.Get("PRIVATE-TOKEN")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc123","short_id":"abc123","web_url":"https://gitlab/c/abc123"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "devops/k8s-pipelines", "glpat-x", false)
	commit, err := c.commit(context.Background(), c.Project, "kargo", "", "update", []Action{
		{Action: "create", FilePath: "trade/project.yaml", Content: "a"},
		{Action: "update", FilePath: "trade/stages.yaml", Content: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.ID != "abc123" || commit.WebURL == "" {
		t.Fatalf("commit: %+v", commit)
	}
	// The project path is one escaped segment, not two.
	if want := "/api/v4/projects/devops%2Fk8s-pipelines/repository/commits"; gotPath != want {
		t.Fatalf("path %q, want %q", gotPath, want)
	}
	if gotToken != "glpat-x" {
		t.Fatalf("token header %q", gotToken)
	}
	if body["branch"] != "kargo" || body["commit_message"] != "update" {
		t.Fatalf("body: %v", body)
	}
	if _, ok := body["start_branch"]; ok {
		t.Error("start_branch was sent for an existing branch; GitLab rejects that")
	}
	if n := len(body["actions"].([]any)); n != 2 {
		t.Fatalf("%d actions in the commit, want 2", n)
	}
}

// Creating the branch on the first push is the difference between "configure
// a review branch" being free and being a manual step in GitLab first.
func TestPushCreatesTheBranchWhenAsked(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"def456"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "g/r", "t", false)
	_, err := c.commit(context.Background(), c.Project, "kargo", "main", "m",
		[]Action{{Action: "create", FilePath: "a.yaml", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if body["start_branch"] != "main" {
		t.Fatalf("start_branch: %v", body["start_branch"])
	}
}

func TestPushRefusesAnEmptyCommit(t *testing.T) {
	if _, err := New("https://gitlab", "g/r", "t", false).Push(context.Background(), "b", "m", nil); err == nil {
		t.Fatal("an empty commit was accepted")
	}
}

// GitLab has no upsert: "create" on a file that exists fails the whole
// commit. Existing is what lets the caller decide per file, and a branch that
// does not exist yet must read as "nothing is there" rather than as an error
// — otherwise the very first push can never happen.
func TestExistingListsBlobsAndToleratesAMissingBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") == "no-such-branch" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Tree Not Found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"path":"kargo/trade","type":"tree"},
			{"path":"kargo/trade/project.yaml","type":"blob"},
			{"path":"kargo/trade/stages.yaml","type":"blob"}]`))
	}))
	defer srv.Close()
	c := New(srv.URL, "g/r", "t", false)

	files, err := c.Existing(context.Background(), "g/r", "kargo", "kargo")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !files["kargo/trade/project.yaml"] {
		t.Fatalf("blobs: %v", files)
	}
	if files["kargo/trade"] {
		t.Error("a directory was reported as a file; committing over it would fail")
	}

	files, err = c.Existing(context.Background(), "g/r", "no-such-branch", "")
	if err != nil {
		t.Fatalf("a branch that does not exist yet must not be an error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected nothing: %v", files)
	}
}

func TestBranchExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/api/v4/projects/g%2Fr/repository/branches/kargo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"kargo"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Branch Not Found"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "g/r", "t", false)

	for _, tc := range []struct {
		branch string
		want   bool
	}{{"kargo", true}, {"missing", false}} {
		got, err := c.BranchExists(context.Background(), "g/r", tc.branch)
		if err != nil {
			t.Fatalf("%s: %v", tc.branch, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.branch, got, tc.want)
		}
	}
}

// The whole of Push, not one call of it: which files already exist decides
// create-versus-update per file, and GitLab fails the entire commit if one of
// them is wrong. A second push over the same domain is the ordinary case, so
// getting this wrong means the feature works exactly once.
func TestPushChoosesCreateOrUpdatePerFile(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/repository/commits"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			_, _ = w.Write([]byte(`{"id":"c0ffee","short_id":"c0ffee"}`))
		case strings.Contains(r.URL.Path, "/repository/branches/"):
			_, _ = w.Write([]byte(`{"name":"kargo"}`)) // the branch is there
		case strings.Contains(r.URL.Path, "/repository/tree"):
			// trade/project.yaml exists; trade/stages.yaml does not.
			_, _ = w.Write([]byte(`[{"path":"trade/project.yaml","type":"blob"},{"path":"trade","type":"tree"}]`))
		default:
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		}
	}))
	defer srv.Close()

	commit, err := New(srv.URL, "g/r", "t", false).Push(context.Background(), "kargo", "m", []repo.File{
		{Path: "trade/project.yaml", Content: "a"},
		{Path: "trade/stages.yaml", Content: "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.ID != "c0ffee" {
		t.Fatalf("commit: %+v", commit)
	}
	got := map[string]string{}
	for _, a := range body["actions"].([]any) {
		m := a.(map[string]any)
		got[m["file_path"].(string)] = m["action"].(string)
	}
	if got["trade/project.yaml"] != "update" {
		t.Errorf("an existing file was pushed as %q; the commit would fail", got["trade/project.yaml"])
	}
	if got["trade/stages.yaml"] != "create" {
		t.Errorf("a new file was pushed as %q; the commit would fail", got["trade/stages.yaml"])
	}
	if _, ok := body["start_branch"]; ok {
		t.Error("start_branch was sent for a branch that exists")
	}
}

// First push into a branch nobody has made yet: everything is a create, and
// the commit has to say where to branch from.
func TestPushIntoAMissingBranch(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/repository/commits"):
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			_, _ = w.Write([]byte(`{"id":"beef"}`))
		case strings.Contains(r.URL.Path, "/repository/branches/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Branch Not Found"}`))
		default:
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		}
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "g/r", "t", false).Push(context.Background(), "kargo", "m",
		[]repo.File{{Path: "a.yaml", Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if body["start_branch"] != "main" {
		t.Fatalf("start_branch: %v", body["start_branch"])
	}
	if a := body["actions"].([]any)[0].(map[string]any); a["action"] != "create" {
		t.Errorf("action %v on a branch that did not exist", a["action"])
	}
}

// Sending start_branch for a branch that already exists is a 400, and the
// message GitLab returns talks about branch conflicts rather than about the
// parameter — so a regression here would be debugged in the wrong place.
// Verified against GitLab 19.3.2.
func TestPushNeverSendsStartBranchForAnExistingBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/repository/commits"):
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			if _, ok := body["start_branch"]; ok {
				// What the real GitLab answers, verbatim.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"A branch called 'kargo' already exists. Switch to that branch in order to make changes"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		case strings.Contains(r.URL.Path, "/repository/branches/"):
			_, _ = w.Write([]byte(`{"name":"kargo"}`))
		case strings.Contains(r.URL.Path, "/repository/tree"):
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		}
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "g/r", "t", false).Push(context.Background(), "kargo", "m",
		[]repo.File{{Path: "a.yaml", Content: "x"}}); err != nil {
		t.Fatalf("push to an existing branch was refused: %v", err)
	}
}

// Package gitlab writes files into a repository through GitLab's API.
//
// Deliberately not git. Tide already speaks HTTP to every other upstream, and
// a git binary in the image would need a working tree, a credential helper
// and an ssh agent to do what one POST does here. The Commits API also gives
// something git does not without effort: every generated file lands in a
// single commit, so a repository is never left half-updated.
package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"tide/internal/upstream"
	"tide/internal/upstream/repo"
)

type Client struct {
	BaseURL string
	// Project is the path with namespace, e.g. "devops/k8s-pipelines".
	Project string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, project, token string, insecure bool) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Project: project, Token: token,
		HTTP: upstream.NewHTTPClient(insecure)}
}

// ErrNotConfigured means nobody has said where to push yet.
var ErrNotConfigured = errors.New("no pipeline repository configured")

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	h := http.Header{}
	if c.Token != "" {
		h.Set("PRIVATE-TOKEN", c.Token)
	}
	return upstream.DoJSON(ctx, c.HTTP, method, c.BaseURL+"/api/v4"+path, h, in, out)
}

// p escapes a project path with namespace ("group/repo") into the single
// path segment GitLab expects.
func p(project string) string { return url.PathEscape(project) }

// Action is one file change within a commit.
type Action struct {
	// Action is "create" or "update"; GitLab has no upsert, which is why
	// Push looks at what is already there first.
	Action   string `json:"action"`
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type Commit struct {
	ID      string `json:"id"`
	ShortID string `json:"short_id"`
	Title   string `json:"title"`
	WebURL  string `json:"web_url"`
}

// Existing lists the files already present under a path, so a second push
// updates them rather than failing on "already exists".
//
// Paginated: a repository holding more than one page of files would otherwise
// look like it held only the first hundred, and every file past that would be
// pushed as a create and fail the whole commit.
func (c *Client) Existing(ctx context.Context, project, ref, path string) (map[string]bool, error) {
	files := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		q := url.Values{"ref": {ref}, "recursive": {"true"},
			"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		if path != "" {
			q.Set("path", path)
		}
		var out []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		}
		if err := c.do(ctx, http.MethodGet, "/projects/"+p(project)+"/repository/tree?"+q.Encode(), nil, &out); err != nil {
			// A branch that does not exist yet, or an empty repository, is not
			// an error here: it means nothing is there to update.
			var he *upstream.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				return map[string]bool{}, nil
			}
			return nil, err
		}
		for _, e := range out {
			if e.Type == "blob" {
				files[e.Path] = true
			}
		}
		if len(out) < 100 {
			break
		}
	}
	return files, nil
}

// maxPages bounds the tree walk. A repository this deep is not one Tide is
// generating into, and an unbounded loop against a paginating API is how a
// request hangs forever.
const maxPages = 50

// Push implements repo.Pusher: work out whether the branch and each file
// already exist, then write them all in one commit.
func (c *Client) Push(ctx context.Context, branch, message string, files []repo.File, prune []string) (*repo.Commit, error) {
	if len(files) == 0 {
		return nil, errors.New("nothing to commit")
	}
	def, err := c.DefaultBranch(ctx, c.Project)
	if err != nil {
		return nil, err
	}
	if branch == "" {
		branch = def
	}
	exists, err := c.BranchExists(ctx, c.Project, branch)
	if err != nil {
		return nil, err
	}
	// A branch that does not exist has to say where to start from, and one
	// that does must not. GitLab rejects start_branch on an existing branch
	// rather than ignoring it, and the refusal points somewhere else:
	//
	//   400  A branch called 'x' already exists.
	//        Switch to that branch in order to make changes
	//
	// which reads as a branch conflict and sends whoever hits it looking at
	// branches, when the actual fault is one parameter too many. Verified
	// against GitLab 19.3.2.
	start := ""
	existing := map[string]bool{}
	if exists {
		if existing, err = c.Existing(ctx, c.Project, branch, ""); err != nil {
			return nil, err
		}
	} else {
		start = def
	}

	actions := make([]Action, 0, len(files))
	for _, f := range files {
		action := "create"
		if existing[f.Path] {
			action = "update"
		}
		actions = append(actions, Action{Action: action, FilePath: f.Path, Content: f.Content})
	}
	// In the same commit as the writes: the repository never describes both
	// the old shape and the new one, so Argo CD can never apply a mixture.
	for _, path := range repo.Stale(existing, files, prune) {
		actions = append(actions, Action{Action: "delete", FilePath: path})
	}
	commit, err := c.commit(ctx, c.Project, branch, start, message, actions)
	if err != nil {
		return nil, err
	}
	return &repo.Commit{ID: commit.ID, ShortID: commit.ShortID, URL: commit.WebURL}, nil
}

// commit sends the whole set as one commit. startBranch creates the branch
// when it does not exist; pass the default branch's name.
func (c *Client) commit(ctx context.Context, project, branch, startBranch, message string, actions []Action) (*Commit, error) {
	if len(actions) == 0 {
		return nil, errors.New("nothing to commit")
	}
	body := map[string]any{
		"branch":         branch,
		"commit_message": message,
		"actions":        actions,
	}
	if startBranch != "" {
		body["start_branch"] = startBranch
	}
	var out Commit
	if err := c.do(ctx, http.MethodPost, "/projects/"+p(project)+"/repository/commits", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DefaultBranch is needed to create the target branch the first time.
func (c *Client) DefaultBranch(ctx context.Context, project string) (string, error) {
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, http.MethodGet, "/projects/"+p(project), nil, &out); err != nil {
		return "", err
	}
	if out.DefaultBranch == "" {
		return "", fmt.Errorf("gitlab: project %q reports no default branch", project)
	}
	return out.DefaultBranch, nil
}

// BranchExists reports whether the target branch is already there, which
// decides whether the commit needs to say where to branch from.
func (c *Client) BranchExists(ctx context.Context, project, branch string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/projects/"+p(project)+"/repository/branches/"+url.PathEscape(branch), nil, nil)
	if err == nil {
		return true, nil
	}
	var he *upstream.HTTPError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

// Whoami implements repo.Pusher.
func (c *Client) Whoami(ctx context.Context) (*repo.Identity, error) {
	var me struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/user", nil, &me); err != nil {
		return nil, err
	}
	// permissions tells us whether this token may push, which "the project
	// exists" does not: read access is enough to see it and not enough to
	// commit, and that difference only shows up at the commit otherwise.
	var proj struct {
		PathWithNamespace string `json:"path_with_namespace"`
		Permissions       struct {
			ProjectAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"project_access"`
			GroupAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"group_access"`
		} `json:"permissions"`
	}
	if err := c.do(ctx, http.MethodGet, "/projects/"+p(c.Project), nil, &proj); err != nil {
		return nil, err
	}
	level := 0
	if a := proj.Permissions.ProjectAccess; a != nil {
		level = a.AccessLevel
	}
	if a := proj.Permissions.GroupAccess; a != nil && a.AccessLevel > level {
		level = a.AccessLevel
	}
	// 30 is Developer, the lowest level that may push to a branch.
	return &repo.Identity{
		Username: me.Username, Name: me.Name,
		Project: proj.PathWithNamespace, CanWrite: level >= 30,
	}, nil
}

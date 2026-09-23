// Package gitea writes files into a repository through Gitea's API.
//
// The same job as the gitlab package and the same shape of answer, but the
// two hosts agree on very little underneath: Gitea takes file contents
// base64-encoded, needs the blob SHA of anything it replaces, and creates a
// branch by naming the new one alongside the base rather than by saying where
// to start. Those differences stop here.
package gitea

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"tide/internal/upstream"
	"tide/internal/upstream/repo"
)

type Client struct {
	BaseURL string
	// Owner and Name come from a "owner/repo" project path.
	Owner string
	Name  string
	Token string
	HTTP  *http.Client
}

// New splits "owner/repo" because Gitea addresses the two separately, unlike
// GitLab's single escaped path.
func New(baseURL, project, token string, insecure bool) *Client {
	owner, name, _ := strings.Cut(strings.Trim(project, "/"), "/")
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Owner: owner, Name: name,
		Token: token, HTTP: upstream.NewHTTPClient(insecure),
	}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	h := http.Header{}
	if c.Token != "" {
		h.Set("Authorization", "token "+c.Token)
	}
	return upstream.DoJSON(ctx, c.HTTP, method, c.BaseURL+"/api/v1"+path, h, in, out)
}

func (c *Client) repoPath() string {
	return "/repos/" + url.PathEscape(c.Owner) + "/" + url.PathEscape(c.Name)
}

// changeFile is one entry of a multi-file commit.
type changeFile struct {
	Operation string `json:"operation"` // create | update | delete
	Path      string `json:"path"`
	Content   string `json:"content"` // base64
	// SHA is the blob id of the file being replaced; Gitea requires it for
	// an update and refuses the commit without it.
	SHA string `json:"sha,omitempty"`
}

// Push implements repo.Pusher.
func (c *Client) Push(ctx context.Context, branch, message string, files []repo.File) (*repo.Commit, error) {
	if len(files) == 0 {
		return nil, errors.New("nothing to commit")
	}
	if c.Owner == "" || c.Name == "" {
		return nil, errors.New("gitea: the project must be written as owner/repo")
	}
	def, err := c.defaultBranch(ctx)
	if err != nil {
		return nil, err
	}
	if branch == "" {
		branch = def
	}
	exists, err := c.branchExists(ctx, branch)
	if err != nil {
		return nil, err
	}

	// Gitea names the base branch and, separately, the branch to create from
	// it. Writing to a branch that is already there means naming only the
	// base; creating one means naming both.
	base, newBranch := branch, ""
	shas := map[string]string{}
	if exists {
		if shas, err = c.blobs(ctx, branch); err != nil {
			return nil, err
		}
	} else {
		base, newBranch = def, branch
	}

	changes := make([]changeFile, 0, len(files))
	for _, f := range files {
		ch := changeFile{
			Operation: "create",
			Path:      f.Path,
			Content:   base64.StdEncoding.EncodeToString([]byte(f.Content)),
		}
		if sha := shas[f.Path]; sha != "" {
			ch.Operation, ch.SHA = "update", sha
		}
		changes = append(changes, ch)
	}

	body := map[string]any{"branch": base, "message": message, "files": changes}
	if newBranch != "" {
		body["new_branch"] = newBranch
	}
	var out struct {
		Commit struct {
			SHA  string `json:"sha"`
			HTML string `json:"html_url"`
		} `json:"commit"`
	}
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/contents", body, &out); err != nil {
		return nil, err
	}
	short := out.Commit.SHA
	if len(short) > 8 {
		short = short[:8]
	}
	return &repo.Commit{ID: out.Commit.SHA, ShortID: short, URL: out.Commit.HTML}, nil
}

// blobs maps every file in the branch to its blob id, which is what an update
// has to quote. The whole tree in one request rather than one request per
// file: a hundred generated files would otherwise be a hundred round trips
// before the commit even starts.
func (c *Client) blobs(ctx context.Context, ref string) (map[string]string, error) {
	out := map[string]string{}
	for page := 1; page <= maxPages; page++ {
		var res struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"tree"`
			Truncated bool `json:"truncated"`
			Page      int  `json:"page"`
			TotalPage int  `json:"total_count"`
		}
		q := url.Values{"recursive": {"1"}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		err := c.do(ctx, http.MethodGet, c.repoPath()+"/git/trees/"+url.PathEscape(ref)+"?"+q.Encode(), nil, &res)
		if err != nil {
			// An empty repository or a branch with no tree yet is not a
			// failure: it means there is nothing to replace.
			var he *upstream.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				return map[string]string{}, nil
			}
			return nil, err
		}
		for _, e := range res.Tree {
			if e.Type == "blob" {
				out[e.Path] = e.SHA
			}
		}
		if len(res.Tree) < 100 {
			break
		}
	}
	return out, nil
}

// maxPages bounds the tree walk; see the same constant in the gitlab package.
const maxPages = 50

func (c *Client) defaultBranch(ctx context.Context) (string, error) {
	var out struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, http.MethodGet, c.repoPath(), nil, &out); err != nil {
		return "", err
	}
	if out.DefaultBranch == "" {
		return "", fmt.Errorf("gitea: %s/%s reports no default branch", c.Owner, c.Name)
	}
	return out.DefaultBranch, nil
}

func (c *Client) branchExists(ctx context.Context, branch string) (bool, error) {
	err := c.do(ctx, http.MethodGet, c.repoPath()+"/branches/"+url.PathEscape(branch), nil, nil)
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
		Login    string `json:"login"`
		FullName string `json:"full_name"`
	}
	if err := c.do(ctx, http.MethodGet, "/user", nil, &me); err != nil {
		return nil, err
	}
	var proj struct {
		FullName    string `json:"full_name"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := c.do(ctx, http.MethodGet, c.repoPath(), nil, &proj); err != nil {
		return nil, err
	}
	return &repo.Identity{
		Username: me.Login, Name: me.FullName,
		Project: proj.FullName, CanWrite: proj.Permissions.Push,
	}, nil
}

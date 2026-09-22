// Package registry reads image metadata (digest, OCI labels, build time) via
// the Docker Registry HTTP API v2, handling basic and bearer-token auth.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tide/internal/upstream"
)

const VersionLabel = "org.opencontainers.image.version"

type Client struct {
	// BaseURL like https://registry.example.com. Plain http is allowed for dev.
	BaseURL  string
	User     string
	Password string
	HTTP     *http.Client

	mu     sync.Mutex
	tokens map[string]string // scope → bearer token
}

func New(baseURL, user, password string, insecure bool) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), User: user, Password: password,
		HTTP: upstream.NewHTTPClient(insecure), tokens: map[string]string{}}
}

type Image struct {
	Digest  string            `json:"digest"`
	Labels  map[string]string `json:"labels,omitempty"`
	Created *time.Time        `json:"created,omitempty"`
	Size    int64             `json:"size"`
}

func (i Image) Version() string { return i.Labels[VersionLabel] }

var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// Inspect resolves ref (tag or digest) in repo (path without host).
func (c *Client) Inspect(ctx context.Context, repo, ref string) (*Image, error) {
	body, digest, err := c.fetch(ctx, repo, "/manifests/"+ref, manifestAccept)
	if err != nil {
		return nil, err
	}
	var m struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Size int64 `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	img := &Image{Digest: digest}
	if len(m.Manifests) > 0 {
		// Multi-arch index: the index digest is what gets deployed, labels come
		// from the linux/amd64 (or first) image.
		child := m.Manifests[0].Digest
		for _, mm := range m.Manifests {
			if mm.Platform.OS == "linux" && mm.Platform.Architecture == "amd64" {
				child = mm.Digest
			}
		}
		sub, err := c.Inspect(ctx, repo, child)
		if err != nil {
			return nil, err
		}
		sub.Digest = digest
		return sub, nil
	}
	for _, l := range m.Layers {
		img.Size += l.Size
	}
	if m.Config.Digest == "" {
		return img, nil
	}
	cfgBody, _, err := c.fetch(ctx, repo, "/blobs/"+m.Config.Digest, "*/*")
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Created *time.Time `json:"created"`
		Config  struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := json.Unmarshal(cfgBody, &cfg); err == nil {
		img.Labels, img.Created = cfg.Config.Labels, cfg.Created
	}
	return img, nil
}

// Ping validates connectivity and credentials.
func (c *Client) Ping(ctx context.Context) error {
	_, _, err := c.fetch(ctx, "", "", "*/*")
	return err
}

func (c *Client) fetch(ctx context.Context, repo, path, accept string) ([]byte, string, error) {
	u := c.BaseURL + "/v2/"
	if repo != "" {
		u = c.BaseURL + "/v2/" + repo + path
	}
	scope := ""
	if repo != "" {
		scope = "repository:" + repo + ":pull"
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Accept", accept)
		c.mu.Lock()
		tok := c.tokens[scope]
		c.mu.Unlock()
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		} else if c.User != "" {
			req.SetBasicAuth(c.User, c.Password)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, "", err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if ch := resp.Header.Get("WWW-Authenticate"); strings.HasPrefix(strings.ToLower(ch), "bearer ") {
				t, err := c.token(ctx, ch, scope)
				if err != nil {
					return nil, "", err
				}
				c.mu.Lock()
				c.tokens[scope] = t
				c.mu.Unlock()
				continue
			}
		}
		if resp.StatusCode >= 300 {
			return nil, "", &upstream.HTTPError{Method: "GET", URL: u, Status: resp.StatusCode, Body: string(body)}
		}
		return body, resp.Header.Get("Docker-Content-Digest"), nil
	}
	return nil, "", errors.New("registry: authentication failed")
}

func (c *Client) token(ctx context.Context, challenge, scope string) (string, error) {
	params := map[string]string{}
	for _, part := range splitChallenge(challenge[len("bearer "):]) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			params[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("registry: bearer challenge without realm: %s", challenge)
	}
	q := url.Values{}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", &upstream.HTTPError{Method: "GET", URL: realm, Status: resp.StatusCode, Body: string(b)}
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token != "" {
		return out.Token, nil
	}
	return out.AccessToken, nil
}

// splitChallenge splits on commas outside quotes.
func splitChallenge(s string) []string {
	var parts []string
	var b strings.Builder
	inQ := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			b.WriteRune(r)
		case r == ',' && !inQ:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// SplitImage turns "registry.example.com/devops/app" into host and repo path.
func SplitImage(image string) (host, repo string) {
	image = strings.TrimPrefix(strings.TrimPrefix(image, "https://"), "http://")
	host, repo, ok := strings.Cut(image, "/")
	if !ok || (!strings.ContainsAny(host, ".:") && host != "localhost") {
		return "docker.io", image
	}
	return host, repo
}

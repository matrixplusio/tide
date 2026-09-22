// Package argocd is a minimal Argo CD REST client. The official Go module is
// avoided on purpose: its dependency tree is enormous and we need five calls.
package argocd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tide/internal/upstream"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string, insecure bool) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: upstream.NewHTTPClient(insecure)}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return upstream.DoJSON(ctx, c.HTTP, http.MethodGet, u, http.Header{"Authorization": {"Bearer " + c.Token}}, nil, out)
}

type ObjectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// Source is one place an Application reads manifests from.
type Source struct {
	RepoURL        string `json:"repoURL"`
	Path           string `json:"path,omitempty"`
	TargetRevision string `json:"targetRevision,omitempty"`
	Chart          string `json:"chart,omitempty"`
	// Ref names this source so another one can reference it for values; a
	// source with a Ref and no Path contributes no manifests of its own.
	Ref string `json:"ref,omitempty"`
}

type Application struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		Project     string `json:"project"`
		Destination struct {
			Server    string `json:"server"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"destination"`
		// Where the manifests come from. Source is the single-source form,
		// which is what an ordinary service uses; Sources is the multi-source
		// form, where several repositories are combined and no single one is
		// "the" place a tag would be written.
		Source  *Source  `json:"source,omitempty"`
		Sources []Source `json:"sources,omitempty"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"health"`
		Summary struct {
			Images []string `json:"images"`
		} `json:"summary"`
		OperationState *struct {
			Phase      string     `json:"phase"`
			Message    string     `json:"message"`
			StartedAt  *time.Time `json:"startedAt"`
			FinishedAt *time.Time `json:"finishedAt"`
			SyncResult *struct {
				Revision string `json:"revision"`
			} `json:"syncResult"`
		} `json:"operationState"`
		ReconciledAt *time.Time `json:"reconciledAt"`
		Resources    []struct {
			Group     string `json:"group"`
			Version   string `json:"version"`
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Health    *struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"health"`
			RequiresPruning bool `json:"requiresPruning"`
		} `json:"resources"`
	} `json:"status"`
}

func (c *Client) ListApplications(ctx context.Context) ([]Application, error) {
	var out struct {
		Items []Application `json:"items"`
	}
	err := c.get(ctx, "/api/v1/applications", nil, &out)
	return out.Items, err
}

func (c *Client) GetApplication(ctx context.Context, name string) (*Application, error) {
	var out Application
	err := c.get(ctx, "/api/v1/applications/"+url.PathEscape(name), nil, &out)
	return &out, err
}

type ResourceNode struct {
	Group      string `json:"group"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
	ParentRefs []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"parentRefs"`
	Info []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"info"`
	Health *struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"health"`
	Images    []string   `json:"images"`
	CreatedAt *time.Time `json:"createdAt"`
}

func (n ResourceNode) InfoValue(name string) string {
	for _, i := range n.Info {
		if i.Name == name {
			return i.Value
		}
	}
	return ""
}

type ResourceTree struct {
	Nodes []ResourceNode `json:"nodes"`
}

func (c *Client) ResourceTree(ctx context.Context, app string) (*ResourceTree, error) {
	var out ResourceTree
	err := c.get(ctx, "/api/v1/applications/"+url.PathEscape(app)+"/resource-tree", nil, &out)
	return &out, err
}

// Resource returns the live manifest of one managed resource.
func (c *Client) Resource(ctx context.Context, app, group, version, kind, namespace, name string) (map[string]any, error) {
	q := url.Values{"namespace": {namespace}, "resourceName": {name}, "version": {version}, "kind": {kind}, "group": {group}}
	var out struct {
		Manifest string `json:"manifest"`
	}
	if err := c.get(ctx, "/api/v1/applications/"+url.PathEscape(app)+"/resource", q, &out); err != nil {
		return nil, err
	}
	var m map[string]any
	err := json.Unmarshal([]byte(out.Manifest), &m)
	return m, err
}

// RunResourceAction runs a resource action (e.g. the built-in "restart" on
// Deployment / StatefulSet / DaemonSet). The token needs Argo CD RBAC
// "applications, action/<group>/<kind>/<action>".
func (c *Client) RunResourceAction(ctx context.Context, app, group, version, kind, namespace, name, action string) error {
	body := map[string]string{
		"name": app, "namespace": namespace, "resourceName": name,
		"version": version, "kind": kind, "group": group, "action": action,
	}
	return upstream.DoJSON(ctx, c.HTTP, http.MethodPost, c.BaseURL+"/api/v1/applications/"+url.PathEscape(app)+"/resource/actions/v2",
		http.Header{"Authorization": {"Bearer " + c.Token}}, body, nil)
}

// ManagedResource is one resource Argo CD compares for an Application. The
// states are JSON documents ("null" when absent): targetState is what git
// renders, normalizedLiveState the cluster object after Argo CD's
// normalization, predictedLiveState what a sync would leave in the cluster.
type ManagedResource struct {
	Group               string `json:"group"`
	Kind                string `json:"kind"`
	Namespace           string `json:"namespace"`
	Name                string `json:"name"`
	TargetState         string `json:"targetState"`
	LiveState           string `json:"liveState"`
	NormalizedLiveState string `json:"normalizedLiveState"`
	PredictedLiveState  string `json:"predictedLiveState"`
	Modified            bool   `json:"modified"`
	Hook                bool   `json:"hook"`
}

// Refresh asks Argo CD to re-read git for the Application (asynchronous).
func (c *Client) Refresh(ctx context.Context, app string) error {
	var out map[string]any
	return c.get(ctx, "/api/v1/applications/"+url.PathEscape(app), url.Values{"refresh": {"normal"}}, &out)
}

func (c *Client) ManagedResources(ctx context.Context, app string) ([]ManagedResource, error) {
	var out struct {
		Items []ManagedResource `json:"items"`
	}
	err := c.get(ctx, "/api/v1/applications/"+url.PathEscape(app)+"/managed-resources", nil, &out)
	return out.Items, err
}

// Sync starts a sync of the Application to revision. Prune deletes
// resources git no longer renders. The token needs Argo CD RBAC
// "applications, sync".
func (c *Client) Sync(ctx context.Context, app, revision string, prune bool) error {
	body := map[string]any{"name": app, "revision": revision, "prune": prune}
	return upstream.DoJSON(ctx, c.HTTP, http.MethodPost, c.BaseURL+"/api/v1/applications/"+url.PathEscape(app)+"/sync",
		http.Header{"Authorization": {"Bearer " + c.Token}}, body, nil)
}

type Event struct {
	Type           string     `json:"type"`
	Reason         string     `json:"reason"`
	Message        string     `json:"message"`
	Count          int        `json:"count"`
	FirstTimestamp *time.Time `json:"firstTimestamp"`
	LastTimestamp  *time.Time `json:"lastTimestamp"`
	EventTime      *time.Time `json:"eventTime"`
}

func (c *Client) Events(ctx context.Context, app, namespace, name, uid string) ([]Event, error) {
	q := url.Values{"resourceNamespace": {namespace}, "resourceName": {name}, "resourceUID": {uid}}
	var out struct {
		Items []Event `json:"items"`
	}
	err := c.get(ctx, "/api/v1/applications/"+url.PathEscape(app)+"/events", q, &out)
	return out.Items, err
}

type LogLine struct {
	Content   string    `json:"content"`
	TimeStamp time.Time `json:"timeStamp"`
	PodName   string    `json:"podName"`
}

// Logs returns the last tail lines of a pod container (no follow).
func (c *Client) Logs(ctx context.Context, app, namespace, pod, container string, tail int) ([]LogLine, error) {
	q := url.Values{"namespace": {namespace}, "podName": {pod}, "tailLines": {fmt.Sprint(tail)}, "follow": {"false"}}
	if container != "" {
		q.Set("container", container)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.BaseURL+"/api/v1/applications/"+url.PathEscape(app)+"/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var b strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			b.WriteString(sc.Text())
		}
		return nil, &upstream.HTTPError{Method: "GET", URL: req.URL.Path, Status: resp.StatusCode, Body: b.String()}
	}
	// The endpoint streams one JSON object per line.
	var lines []LogLine
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		var msg struct {
			Result *LogLine `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Error != nil {
			return lines, fmt.Errorf("argocd logs: %s", msg.Error.Message)
		}
		if msg.Result != nil && (msg.Result.Content != "" || !msg.Result.TimeStamp.IsZero()) {
			lines = append(lines, *msg.Result)
		}
	}
	return lines, sc.Err()
}

type Version struct {
	Version string `json:"Version"`
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var v Version
	err := c.get(ctx, "/api/version", nil, &v)
	return v.Version, err
}

// UserInfo validates the token.
func (c *Client) UserInfo(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/api/v1/session/userinfo", nil, &out)
	return out, err
}

// Manifests reports the single source this Application's manifests come from.
//
// A tag can only be written into one place, so the multi-source form has no
// answer here: several repositories are combined and nothing says which one
// holds the image. Rather than guess — and quietly write the tag into the
// wrong repository — this says it does not know, and the caller reports the
// Application as one it cannot handle.
func (a *Application) Manifests() (Source, bool) {
	if a.Spec.Source != nil && a.Spec.Source.RepoURL != "" {
		return *a.Spec.Source, true
	}
	// One source with a path and others that only carry a ref is still
	// unambiguous: the others contribute values, not manifests.
	var found Source
	n := 0
	for _, s := range a.Spec.Sources {
		if s.Path == "" && s.Chart == "" {
			continue // a values-only source
		}
		found, n = s, n+1
	}
	if n == 1 && found.RepoURL != "" {
		return found, true
	}
	return Source{}, false
}

// workloadKinds are the objects that run containers, and therefore the ones
// whose image a pipeline would ever want to change.
//
// Deliberately about the desired state, not the running one: an Application
// scaled to zero replicas has a Deployment and no pods, so anything derived
// from what is actually running — status.summary.images, for instance —
// reports it as having no images at all. Fifteen real services are parked at
// zero replicas, and judging by images would drop every one of them silently.
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"CronJob":     true,
	"Job":         true,
	"Rollout":     true, // Argo Rollouts
}

// RunsWorkloads reports whether this Application deploys something that runs
// containers, as opposed to one that only carries namespace scaffolding —
// Namespace, ResourceQuota, LimitRange, sealed secrets and the like.
//
// An Application with nothing to run has no image to subscribe to and nothing
// to promote, so generating a pipeline for it produces objects that can only
// sit there.
func (a *Application) RunsWorkloads() bool {
	for _, r := range a.Status.Resources {
		if workloadKinds[r.Kind] {
			return true
		}
	}
	return false
}

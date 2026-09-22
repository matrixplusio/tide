// Package kargo is a Kargo API client. Resources are read and written through
// the REST API (/v1beta1): its Connect JSON encoding returns every Kubernetes
// timestamp as {} and drops freight collection details. Only GetVersionInfo,
// which has no such fields, uses Connect. Types cover only the fields Tide reads and follow
// Kargo v1.11.4 (api/v1alpha1, api/service/v1alpha1). Field names were checked
// against that tag; bump them together with the cluster version.
package kargo

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"tide/internal/upstream"
)

const servicePath = "/akuity.io.kargo.service.v1alpha1.KargoService/"

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string, insecure bool) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: upstream.NewHTTPClient(insecure)}
}

func (c *Client) call(ctx context.Context, method string, in, out any) error {
	h := http.Header{"Connect-Protocol-Version": {"1"}}
	if c.Token != "" {
		h.Set("Authorization", "Bearer "+c.Token)
	}
	if in == nil {
		in = struct{}{}
	}
	return upstream.DoJSON(ctx, c.HTTP, http.MethodPost, c.BaseURL+servicePath+method, h, in, out)
}

type ObjectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp *time.Time        `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
}

type FreightOrigin struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Image struct {
	RepoURL string `json:"repoURL"`
	Tag     string `json:"tag"`
	Digest  string `json:"digest"`
}

type Freight struct {
	Metadata ObjectMeta    `json:"metadata"`
	Alias    string        `json:"alias"`
	Origin   FreightOrigin `json:"origin"`
	Images   []Image       `json:"images"`
	Status   struct {
		VerifiedIn map[string]struct {
			VerifiedAt  *time.Time `json:"verifiedAt"`
			LongestSoak string     `json:"longestSoak"`
		} `json:"verifiedIn"`
		ApprovedFor map[string]struct {
			ApprovedAt *time.Time `json:"approvedAt"`
		} `json:"approvedFor"`
		CurrentlyIn map[string]struct {
			Since *time.Time `json:"since"`
		} `json:"currentlyIn"`
	} `json:"status"`
}

type FreightReference struct {
	Name   string        `json:"name"`
	Origin FreightOrigin `json:"origin"`
	Images []Image       `json:"images"`
}

type StepMetadata struct {
	Alias      string     `json:"alias"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	ErrorCount int        `json:"errorCount"`
	Status     string     `json:"status"`
	Message    string     `json:"message"`
}

type PromotionStatus struct {
	Phase                 string            `json:"phase"`
	Message               string            `json:"message"`
	Freight               *FreightReference `json:"freight"`
	StartedAt             *time.Time        `json:"startedAt"`
	FinishedAt            *time.Time        `json:"finishedAt"`
	CurrentStep           int               `json:"currentStep"`
	StepExecutionMetadata []StepMetadata    `json:"stepExecutionMetadata"`
}

type PromotionStep struct {
	Uses string `json:"uses"`
	As   string `json:"as"`
	Task *struct {
		Name string `json:"name"`
	} `json:"task"`
}

type Promotion struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		Stage   string          `json:"stage"`
		Freight string          `json:"freight"`
		Steps   []PromotionStep `json:"steps"`
	} `json:"spec"`
	Status PromotionStatus `json:"status"`
}

// Terminal phases of a Promotion.
func (p *Promotion) Terminal() bool {
	switch p.Status.Phase {
	case "Succeeded", "Failed", "Errored", "Aborted":
		return true
	}
	return false
}

type PromotionReference struct {
	Name       string            `json:"name"`
	Freight    *FreightReference `json:"freight"`
	Status     *PromotionStatus  `json:"status"`
	FinishedAt *time.Time        `json:"finishedAt"`
}

type AutoPromotionHold struct {
	FreightName   string     `json:"freightName"`
	PromotionName string     `json:"promotionName"`
	Actor         string     `json:"actor"`
	CreatedAt     *time.Time `json:"createdAt"`
}

type Stage struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		RequestedFreight []struct {
			Origin  FreightOrigin `json:"origin"`
			Sources struct {
				Direct           bool     `json:"direct"`
				Stages           []string `json:"stages"`
				RequiredSoakTime string   `json:"requiredSoakTime"`
			} `json:"sources"`
		} `json:"requestedFreight"`
	} `json:"spec"`
	Status struct {
		FreightHistory []struct {
			ID    string                      `json:"id"`
			Items map[string]FreightReference `json:"items"`
		} `json:"freightHistory"`
		CurrentPromotion *PromotionReference `json:"currentPromotion"`
		LastPromotion    *PromotionReference `json:"lastPromotion"`
		Health           *struct {
			Status string   `json:"status"`
			Issues []string `json:"issues"`
		} `json:"health"`
		AutoPromotionEnabled        bool                         `json:"autoPromotionEnabled"`
		EffectiveAutoPromotionHolds map[string]AutoPromotionHold `json:"effectiveAutoPromotionHolds"`
		Conditions                  []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}

// Current returns the freight currently in the stage (first item of the
// newest history entry), or nil.
func (s *Stage) Current() *FreightReference {
	if len(s.Status.FreightHistory) == 0 {
		return nil
	}
	for _, f := range s.Status.FreightHistory[0].Items {
		return &f
	}
	return nil
}

// UpstreamStages returns the stages freight must pass before this one.
func (s *Stage) UpstreamStages() []string {
	var out []string
	for _, rf := range s.Spec.RequestedFreight {
		out = append(out, rf.Sources.Stages...)
	}
	return out
}

// Warehouses returns the warehouses this stage takes freight from, and
// whether any of them feeds the stage directly (no upstream stage).
func (s *Stage) Warehouses() (names []string, direct bool) {
	for _, rf := range s.Spec.RequestedFreight {
		if rf.Origin.Kind == "Warehouse" && !slices.Contains(names, rf.Origin.Name) {
			names = append(names, rf.Origin.Name)
		}
		direct = direct || rf.Sources.Direct
	}
	return names, direct
}

// Requests reports whether the stage takes freight of this origin. A stage
// with no requested freight (not expected in practice) takes anything.
func (s *Stage) Requests(o FreightOrigin) bool {
	if len(s.Spec.RequestedFreight) == 0 {
		return true
	}
	for _, rf := range s.Spec.RequestedFreight {
		if rf.Origin.Kind == o.Kind && rf.Origin.Name == o.Name {
			return true
		}
	}
	return false
}

func (c *Client) GetVersionInfo(ctx context.Context) (string, error) {
	var out struct {
		VersionInfo struct {
			Version string `json:"version"`
		} `json:"versionInfo"`
	}
	err := c.call(ctx, "GetVersionInfo", nil, &out)
	return out.VersionInfo.Version, err
}

func (c *Client) rest(ctx context.Context, method, path string, in, out any) error {
	h := http.Header{}
	if c.Token != "" {
		h.Set("Authorization", "Bearer "+c.Token)
	}
	return upstream.DoJSON(ctx, c.HTTP, method, c.BaseURL+"/v1beta1"+path, h, in, out)
}

func p(s string) string { return url.PathEscape(s) }

func (c *Client) ListProjects(ctx context.Context) ([]string, error) {
	var out struct {
		Items []struct {
			Metadata ObjectMeta `json:"metadata"`
		} `json:"items"`
	}
	if err := c.rest(ctx, http.MethodGet, "/projects", nil, &out); err != nil {
		return nil, err
	}
	names := make([]string, len(out.Items))
	for i, it := range out.Items {
		names[i] = it.Metadata.Name
	}
	return names, nil
}

func (c *Client) GetStage(ctx context.Context, project, stage string) (*Stage, error) {
	var out Stage
	if err := c.rest(ctx, http.MethodGet, "/projects/"+p(project)+"/stages/"+p(stage), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListStages(ctx context.Context, project string) ([]Stage, error) {
	var out struct {
		Items []Stage `json:"items"`
	}
	err := c.rest(ctx, http.MethodGet, "/projects/"+p(project)+"/stages", nil, &out)
	return out.Items, err
}

// QueryFreight with a stage returns only freight available to that stage
// (verified upstream / approved). Without a stage it returns all freight.
// Newest first.
func (c *Client) QueryFreight(ctx context.Context, project, stage string) ([]Freight, error) {
	path := "/projects/" + p(project) + "/freight"
	if stage != "" {
		path += "?stage=" + url.QueryEscape(stage)
	}
	var out struct {
		Groups map[string]struct {
			Items []Freight `json:"items"`
		} `json:"groups"`
	}
	if err := c.rest(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	var all []Freight
	for _, g := range out.Groups {
		all = append(all, g.Items...)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i].Metadata.CreationTimestamp, all[j].Metadata.CreationTimestamp
		if a == nil || b == nil {
			return all[i].Metadata.Name > all[j].Metadata.Name
		}
		return a.After(*b)
	})
	return all, nil
}

func (c *Client) GetFreight(ctx context.Context, project, name string) (*Freight, error) {
	var out Freight
	if err := c.rest(ctx, http.MethodGet, "/projects/"+p(project)+"/freight/"+p(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PromoteToStage creates a Promotion and returns it as persisted. Kargo's
// webhook rewrites the name to <stage>.<ulid>.<freight prefix>, so callers must
// use the returned name rather than anything they construct.
func (c *Client) PromoteToStage(ctx context.Context, project, stage, freight string) (*Promotion, error) {
	var out Promotion
	if err := c.rest(ctx, http.MethodPost, "/projects/"+p(project)+"/stages/"+p(stage)+"/promotions",
		map[string]string{"freight": freight}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefreshWarehouse asks Kargo to run this warehouse's artifact discovery now
// rather than at its next scheduled turn, which is what turns a pipeline's
// "I just pushed an image" into a Freight in seconds instead of at the end of
// a polling interval.
//
// It is an acceleration, never a correctness requirement: Kargo's own
// interval still finds the image if this call never happens, so callers log a
// failure and carry on rather than failing what they were doing.
//
// This goes over the REST route rather than the KargoService RPC of the same
// purpose. RefreshResource answers with a google.protobuf.Any, which v1.11.4
// cannot marshal over Connect's JSON codec: the refresh is applied and then
// the reply fails with `proto: google.protobuf.Any: unable to resolve "/,
// Kind=": not found`, so every call looks like a failure while quietly
// working. The REST route returns an empty body and has no such problem.
func (c *Client) RefreshWarehouse(ctx context.Context, project, name string) error {
	return c.rest(ctx, http.MethodPost, "/projects/"+p(project)+"/warehouses/"+p(name)+"/refresh", struct{}{}, nil)
}

type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type Warehouse struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   struct {
		Conditions []Condition `json:"conditions"`
	} `json:"status"`
}

func (c *Client) GetWarehouse(ctx context.Context, project, name string) (*Warehouse, error) {
	var out Warehouse
	if err := c.rest(ctx, http.MethodGet, "/projects/"+p(project)+"/warehouses/"+p(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DiscoveryProblem says what is stopping this warehouse from finding
// artifacts, in Kargo's own words, or "" when nothing is.
//
// Refreshing a warehouse answers 200 as soon as the annotation is written,
// which says nothing about whether the discovery that follows succeeded. A
// warehouse whose images have been deleted from the registry answers every
// refresh cheerfully and produces freight for none of them — and the only
// symptom, without this, is a release that never appears.
// The three states a warehouse is observed in, and what separates them:
//
//	discovering  Ready=False/DiscoveryInProgress  Healthy=Unknown/Pending
//	healthy      Ready=True /ArtifactsDiscovered  Healthy=True/ReconciliationSucceeded
//	broken       Ready=False/DiscoveryFailure     Healthy=False/DiscoveryFailed
//
// Healthy is the one to read, and only an explicit False counts. Ready is
// False for the first second after every refresh, so judging by Ready means
// calling every healthy warehouse broken; Unknown means Kargo has not decided
// yet, which is not the same as bad news.
func (w *Warehouse) DiscoveryProblem() string {
	broken := false
	for _, c := range w.Status.Conditions {
		if c.Type == "Healthy" {
			broken = c.Status == "False"
			break
		}
	}
	if !broken {
		return ""
	}
	// Ready carries the detail worth repeating — the registry's own words,
	// such as "MANIFEST_UNKNOWN: manifest unknown". Healthy only names the
	// category, so it is the fallback rather than the answer.
	for _, c := range w.Status.Conditions {
		if c.Type == "Ready" && c.Status != "True" && c.Reason != "" {
			if c.Message != "" {
				return c.Reason + ": " + c.Message
			}
			return c.Reason
		}
	}
	for _, c := range w.Status.Conditions {
		if c.Type == "Healthy" {
			if c.Message != "" {
				return c.Reason + ": " + c.Message
			}
			return c.Reason
		}
	}
	return ""
}

func (c *Client) GetPromotion(ctx context.Context, project, name string) (*Promotion, error) {
	var out Promotion
	if err := c.rest(ctx, http.MethodGet, "/projects/"+p(project)+"/promotions/"+p(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPromotions returns promotions newest first.
func (c *Client) ListPromotions(ctx context.Context, project, stage string) ([]Promotion, error) {
	path := "/projects/" + p(project) + "/promotions"
	if stage != "" {
		path += "?stage=" + url.QueryEscape(stage)
	}
	var out struct {
		Items []Promotion `json:"items"`
	}
	if err := c.rest(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i].Metadata.CreationTimestamp, out.Items[j].Metadata.CreationTimestamp
		return a != nil && b != nil && a.After(*b)
	})
	return out.Items, nil
}

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"tide/internal/catalog"
	"tide/internal/plan"
	"tide/internal/release"
	"tide/internal/upstream/kargo"
)

// Image promotes Kargo Freight to a Stage.
type Image struct {
	Hub *catalog.Hub
}

func (Image) Kind() string { return release.KindImage }

func payload(it *release.Item) (*release.ImagePayload, error) {
	var p release.ImagePayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (x Image) Validate(ctx context.Context, it *release.Item) error {
	p, err := payload(it)
	if err != nil {
		return err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return err
	}
	stage, err := c.Kargo.GetStage(ctx, p.Project, p.Stage)
	if err != nil {
		return err
	}
	if stage.Status.CurrentPromotion != nil {
		return fmt.Errorf("stage %s already has promotion %s running", p.Stage, stage.Status.CurrentPromotion.Name)
	}
	cur := stage.Current()
	curDigest := ""
	if cur != nil {
		for _, img := range cur.Images {
			if img.RepoURL == p.Image || len(cur.Images) == 1 {
				curDigest = img.Digest
			}
		}
	}
	switch {
	case p.From == nil && cur != nil:
		return fmt.Errorf("release was built for a first deploy, but %s is now running %s (%s); create a new release", p.Env, cur.Name, curDigest)
	case p.From != nil && curDigest != p.From.Digest:
		return fmt.Errorf("current version changed since the release was created: expected %s, now %s; create a new release", p.From.Digest, orNone(curDigest))
	}
	avail, err := c.Kargo.QueryFreight(ctx, p.Project, p.Stage)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(avail, func(f kargo.Freight) bool { return f.Metadata.Name == p.Freight }) {
		return fmt.Errorf("freight %s is no longer available to stage %s", p.Freight, p.Stage)
	}
	if v := p.Verified; v != nil {
		ok, err := plan.StillVerified(ctx, x.Hub, v)
		if err != nil {
			return fmt.Errorf("check verification in %s (%s/%s): %w", v.Env, v.Upstream, v.Stage, err)
		}
		if !ok {
			return fmt.Errorf("digest %s is no longer verified in %s (%s/%s)", v.Digest, v.Env, v.Upstream, v.Stage)
		}
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(nothing)"
	}
	return s
}

func (x Image) Execute(ctx context.Context, it *release.Item) (string, error) {
	p, err := payload(it)
	if err != nil {
		return "", err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return "", err
	}
	promo, err := c.Kargo.PromoteToStage(ctx, p.Project, p.Stage, p.Freight)
	if err != nil {
		return "", err
	}
	if promo == nil || promo.Metadata.Name == "" {
		return "", fmt.Errorf("kargo returned no promotion name")
	}
	return promo.Metadata.Name, nil
}

func (x Image) Poll(ctx context.Context, it *release.Item) (ItemStatus, error) {
	p, err := payload(it)
	if err != nil {
		return ItemStatus{}, err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return ItemStatus{}, err
	}
	promo, err := c.Kargo.GetPromotion(ctx, p.Project, it.ExternalRef)
	if err != nil {
		return ItemStatus{}, err
	}
	if !promo.Terminal() {
		return ItemStatus{}, nil
	}
	if promo.Status.Phase == "Succeeded" {
		// Kargo's argocd-update step ends when the sync finishes, not when the
		// new pods are ready: wait for the rollout before calling it done.
		if p.App == "" {
			return ItemStatus{Done: true, Success: true}, nil
		}
		return x.rollout(ctx, c, p)
	}
	msg := promo.Status.Phase
	if promo.Status.Message != "" {
		msg += ": " + promo.Status.Message
	}
	var steps []string
	for _, s := range promo.Status.StepExecutionMetadata {
		if s.Message != "" && s.Status != "Succeeded" {
			steps = append(steps, fmt.Sprintf("step %s (%s): %s", s.Alias, s.Status, s.Message))
		}
	}
	if len(steps) > 0 {
		msg += "\n" + strings.Join(steps, "\n")
	}
	return ItemStatus{Done: true, Error: msg}, nil
}

// rollout turns the Application's workloads into the item status: done when
// a workload runs the target artifact and every workload has rolled out all
// its replicas; failed when Kubernetes gave up on the rollout. At least one
// workload must run the target, so a sync that has not reached the cluster
// yet is not mistaken for done.
func (x Image) rollout(ctx context.Context, c *catalog.Clients, p *release.ImagePayload) (ItemStatus, error) {
	app, err := c.ArgoCD.GetApplication(ctx, p.App)
	if err != nil {
		return ItemStatus{}, err
	}
	var ws []workload
	for _, r := range app.Status.Resources {
		if r.Group != "apps" || (r.Kind != "Deployment" && r.Kind != "StatefulSet" && r.Kind != "DaemonSet") {
			continue
		}
		obj, err := c.ArgoCD.Resource(ctx, p.App, r.Group, r.Version, r.Kind, r.Namespace, r.Name)
		if err != nil {
			return ItemStatus{}, fmt.Errorf("%s/%s: %w", r.Kind, r.Name, err)
		}
		ws = append(ws, workload{kind: r.Kind, name: r.Name, obj: obj})
	}
	return rolloutStatus(ws, p.To), nil
}

type workload struct {
	kind, name string
	obj        map[string]any
}

func rolloutStatus(ws []workload, target release.Artifact) ItemStatus {
	runs := false
	var waiting []string
	for _, w := range ws {
		if failed, why := rolloutFailed(w.kind, w.obj); failed {
			return ItemStatus{Done: true, Error: fmt.Sprintf("%s/%s: %s", w.kind, w.name, why)}
		}
		if runsArtifact(w.obj, target) {
			runs = true
		}
		if ok, why := rolloutComplete(w.kind, w.obj); !ok {
			waiting = append(waiting, fmt.Sprintf("%s/%s: %s", w.kind, w.name, why))
		}
	}
	if !runs {
		waiting = append([]string{fmt.Sprintf("no workload runs %s yet", labelOf(target))}, waiting...)
	}
	if len(waiting) > 0 {
		return ItemStatus{Waiting: strings.Join(waiting, "; ")}
	}
	return ItemStatus{Done: true, Success: true}
}

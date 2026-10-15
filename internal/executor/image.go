package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/plan"
	"tide/internal/release"
	"tide/internal/upstream/argocd"
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
	if it.RevertRef != "" && p.From != nil {
		return x.pollRevert(ctx, c, p, it)
	}
	promo, err := c.Kargo.GetPromotion(ctx, p.Project, it.ExternalRef)
	if err != nil {
		return ItemStatus{}, err
	}
	if !promo.Terminal() {
		return ItemStatus{}, nil
	}
	if promo.Status.Phase == "Succeeded" {
		since := promo.Status.FinishedAt
		if since == nil {
			since = it.StartedAt
		}
		st, err := x.landed(ctx, c, p, since)
		if err == nil && st.aborted {
			return x.revert(ctx, c, p, st.Error)
		}
		return st, err
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

// landed follows a promotion that succeeded at p.To into the cluster. Kargo's
// part ends with the commit, not when the new pods are ready: wait for the
// rollout before calling it done. since is when the promotion finished.
func (x Image) landed(ctx context.Context, c *catalog.Clients, p *release.ImagePayload, since *time.Time) (ItemStatus, error) {
	if p.App == "" {
		return ItemStatus{Done: true, Success: true}, nil
	}
	app, err := c.ArgoCD.GetApplication(ctx, p.App)
	if err != nil {
		return ItemStatus{}, err
	}
	if !app.AutoSync() {
		return x.syncThenRollout(ctx, c, p, app, since)
	}
	return x.rollout(ctx, c, p, app)
}

// rollout turns the Application's workloads into the item status: done when
// a workload runs the target artifact and every workload has rolled out all
// its replicas; failed when Kubernetes gave up on the rollout. At least one
// workload must run the target, so a sync that has not reached the cluster
// yet is not mistaken for done.
func (x Image) rollout(ctx context.Context, c *catalog.Clients, p *release.ImagePayload, app *argocd.Application) (ItemStatus, error) {
	ws, err := x.workloads(ctx, c, p, app)
	if err != nil {
		return ItemStatus{}, err
	}
	return rolloutStatus(ws, p.To), nil
}

func (x Image) workloads(ctx context.Context, c *catalog.Clients, p *release.ImagePayload, app *argocd.Application) ([]workload, error) {
	return observeWorkloads(ctx, c.ArgoCD, p.App, appWorkloads(app))
}

type workload struct {
	kind, name, namespace string
	obj                   map[string]any
	// tmpl holds the pod template of a Rollout: the Rollout itself, or the
	// Deployment it adopts. Empty for every other kind.
	tmpl map[string]any
}

// template is the object whose pod template says which version this
// workload is meant to run.
func (w workload) template() map[string]any {
	if w.tmpl != nil {
		return w.tmpl
	}
	return w.obj
}

func rolloutStatus(ws []workload, target release.Artifact) ItemStatus {
	runs := false
	var waiting []string
	var paused *Pause
	for _, w := range ws {
		if w.kind == rolloutKind {
			st := judgeRollout(w.obj, w.template(), target)
			switch {
			case st.failed != "":
				return ItemStatus{Done: true, Error: fmt.Sprintf("%s/%s: %s", w.kind, w.name, st.failed), aborted: st.aborted}
			case st.paused != nil:
				runs, paused = true, st.paused
				waiting = append(waiting, fmt.Sprintf("%s/%s: waiting for a decision", w.kind, w.name))
			case !st.done:
				runs = runs || st.onTarget
				waiting = append(waiting, fmt.Sprintf("%s/%s: %s", w.kind, w.name, st.waiting))
			default:
				runs = true
			}
			continue
		}
		// A verdict only counts once the workload's spec is this release's.
		// Until the sync lands, whatever Kubernetes last concluded is about
		// the version before — and a Deployment that was already broken (a
		// new service still on its placeholder image, say) carries a
		// deadline it exceeded long ago, which read here failed a release a
		// second after its promotion, before the new pods even existed.
		onTarget := runsArtifact(w.obj, target)
		if onTarget {
			runs = true
			if failed, why := rolloutFailed(w.kind, w.obj); failed {
				return ItemStatus{Done: true, Error: fmt.Sprintf("%s/%s: %s", w.kind, w.name, why)}
			}
		}
		if ok, why := rolloutComplete(w.kind, w.obj); !ok {
			waiting = append(waiting, fmt.Sprintf("%s/%s: %s", w.kind, w.name, why))
		}
	}
	if !runs {
		waiting = append([]string{fmt.Sprintf("no workload runs %s yet", labelOf(target))}, waiting...)
	}
	if len(waiting) > 0 {
		return ItemStatus{Waiting: strings.Join(waiting, "; "), Paused: paused}
	}
	return ItemStatus{Done: true, Success: true}
}

// Rollout lets a waiting blue-green or canary rollout go on, or aborts it.
func (x Image) Rollout(ctx context.Context, it *release.Item, action RolloutAction) error {
	p, err := payload(it)
	if err != nil {
		return err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return err
	}
	return actOnRollouts(ctx, c, p.App, action)
}

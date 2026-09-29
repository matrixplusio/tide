package executor

import (
	"context"
	"errors"
	"fmt"

	"tide/internal/catalog"
	"tide/internal/manifest"
	"tide/internal/plan"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/upstream/gitea"
	"tide/internal/upstream/gitlab"
	"tide/internal/upstream/repo"
)

// Scale changes a service's replica count by editing the number in git.
//
// Not by scaling the cluster: Argo CD puts that back on the next sync, and in
// the meantime the Application sits OutOfSync — which stalls the sync wave it
// belongs to and shows up as unrelated things failing to start.
type Scale struct {
	Hub      *catalog.Hub
	Settings *settings.Store
}

func (Scale) Kind() string { return release.KindScale }

func (x Scale) parts(ctx context.Context, it *release.Item) (*release.ScalePayload, *catalog.Clients, error) {
	p, err := it.ScalePayload()
	if err != nil {
		return nil, nil, err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return nil, nil, err
	}
	return p, c, nil
}

// Validate refuses the cases that would report success and change nothing, and
// the two overlaps that would fight another writer.
func (x Scale) Validate(ctx context.Context, it *release.Item) error {
	p, c, err := x.parts(ctx, it)
	if err != nil {
		return err
	}
	if p.Project != "" && p.Stage != "" {
		stage, err := c.Kargo.GetStage(ctx, p.Project, p.Stage)
		if err != nil {
			return err
		}
		if stage.Status.CurrentPromotion != nil {
			return fmt.Errorf("stage %s has promotion %s running, and it writes the same repository; scale after it finishes",
				p.Stage, stage.Status.CurrentPromotion.Name)
		}
	}
	if err := x.noAutoscaler(ctx, c, p); err != nil {
		return err
	}
	// Checked again here, not only when the release was built: a budget can
	// land in the repository between the two, and the whole harm is in the
	// commit this executor is about to make.
	if pdb, err := plan.BlockingPDB(ctx, c, p.App, p.To); err != nil {
		return err
	} else if pdb != "" {
		return fmt.Errorf("PodDisruptionBudget/%s would forbid evicting any of %d replicas, so no node could be drained; change the budget first",
			pdb, p.To)
	}
	// The count in git is what the next sync applies, so that is what has to
	// still be what this release was built against.
	cfg, doc, err := x.manifest(ctx, p)
	if err != nil {
		return err
	}
	_ = cfg
	from, explicit := doc.Replicas()
	if !explicit {
		return fmt.Errorf("%s no longer sets a replica count", p.Path)
	}
	if from != p.From {
		return fmt.Errorf("%s was %d replicas when this release was confirmed and is %d now; somebody else changed it",
			p.Service, p.From, from)
	}
	return nil
}

// noAutoscaler refuses to scale a workload whose count something else decides.
// A HorizontalPodAutoscaler overrides whatever is in the manifest within
// seconds, and the release would already have reported success: a scale that
// quietly undoes itself is worse than one that refuses.
//
// This sees the autoscalers in the service's own Application, which is all Tide
// can see — every Argo CD resource query is scoped to an Application and there
// is no cluster-wide view. Business namespaces are meant to hold only what the
// registry renders, and on 2026-09-28 every object in them was Argo-tracked;
// but nothing enforces that, and on 2026-09-24 three manually applied routes
// were outside it. Complete for anything that came through the registry, blind
// to anything that did not.
func (x Scale) noAutoscaler(ctx context.Context, c *catalog.Clients, p *release.ScalePayload) error {
	app, err := c.ArgoCD.GetApplication(ctx, p.App)
	if err != nil {
		return err
	}
	for _, res := range app.Status.Resources {
		if res.Kind != "HorizontalPodAutoscaler" {
			continue
		}
		obj, err := c.ArgoCD.Resource(ctx, p.App, res.Group, res.Version, res.Kind, res.Namespace, res.Name)
		if err != nil {
			// Refusing on a failed lookup rather than carrying on: the whole
			// point is the case where something else sets the count.
			return fmt.Errorf("could not read %s/%s to check whether it scales this service: %w", res.Kind, res.Name, err)
		}
		if !scaleTargets(obj, p.Workload) {
			continue
		}
		return fmt.Errorf("%s/%s decides how many replicas %s runs; change the autoscaler instead",
			res.Kind, res.Name, p.Workload.Name)
	}
	return nil
}

// scaleTargets reports whether an autoscaler points at this workload. Kind and
// name both: an autoscaler for a different object in the same namespace is not
// this service's problem, and refusing on its account would block a change that
// is perfectly safe.
func scaleTargets(hpa map[string]any, w release.Workload) bool {
	ref, ok := lookup(hpa, "spec", "scaleTargetRef")
	if !ok {
		return false
	}
	m, _ := ref.(map[string]any)
	kind, _ := m["kind"].(string)
	name, _ := m["name"].(string)
	return kind == w.Kind && name == w.Name
}

func (x Scale) manifest(ctx context.Context, p *release.ScalePayload) (settings.AppsRepo, *manifest.Doc, error) {
	var cfg settings.AppsRepo
	if err := x.Settings.Load(ctx, settings.SectionAppsRepo, &cfg); err != nil {
		return cfg, nil, err
	}
	if !cfg.Configured() {
		return cfg, nil, errors.New("no manifest repository is configured, so the replica count cannot be changed")
	}
	text, err := x.client(cfg).Read(ctx, cfg.Branch, p.Path)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return cfg, nil, fmt.Errorf("%s is not in %s at %s", p.Path, cfg.Project, cfg.Branch)
		}
		return cfg, nil, fmt.Errorf("read %s: %w", p.Path, err)
	}
	doc, err := manifest.Parse(text)
	if err != nil {
		return cfg, nil, fmt.Errorf("%s: %w", p.Path, err)
	}
	return cfg, doc, nil
}

// client speaks to the manifest repository. Reading and writing are the same
// client: a read from one host and a write to another would be a configuration
// mistake nobody could see in the result.
type client interface {
	Read(ctx context.Context, ref, path string) (string, error)
	Push(ctx context.Context, branch, message string, files []repo.File, prune []string) (*repo.Commit, error)
}

func (x Scale) client(cfg settings.AppsRepo) client {
	if cfg.Host() == settings.ProviderGitea {
		return gitea.New(cfg.BaseURL, cfg.Project, cfg.Token, false)
	}
	return gitlab.New(cfg.BaseURL, cfg.Project, cfg.Token, false)
}

// Execute writes the new count. One commit, one number.
func (x Scale) Execute(ctx context.Context, it *release.Item) (string, error) {
	p, _, err := x.parts(ctx, it)
	if err != nil {
		return "", err
	}
	cfg, doc, err := x.manifest(ctx, p)
	if err != nil {
		return "", err
	}
	was, err := doc.SetReplicas(p.To)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.Path, err)
	}
	// Read and write are two requests, and the push replaces the whole file.
	// Checking here as well as in Validate closes the gap between them: a
	// commit that lands in the meantime would otherwise be discarded silently.
	if was != p.From {
		return "", fmt.Errorf("%s was %d replicas when this release was confirmed and is %d now; somebody else changed it",
			p.Service, p.From, was)
	}
	text, err := doc.String()
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("调整副本数：%s 的 %s 环境 %d → %d", p.Service, p.Env, p.From, p.To)
	commit, err := x.client(cfg).Push(ctx, cfg.Branch, msg, []repo.File{{Path: p.Path, Content: text}}, nil)
	if err != nil {
		return "", err
	}
	return commit.ShortID, nil
}

// Poll waits for the cluster to be running the number that was asked for.
//
// Scaling down to zero is done when the pods are gone, not when the object
// says zero — the object says so immediately, and the release would report
// success while the last pod is still terminating.
func (x Scale) Poll(ctx context.Context, it *release.Item) (ItemStatus, error) {
	p, c, err := x.parts(ctx, it)
	if err != nil {
		return ItemStatus{}, err
	}
	// Ask Argo CD to look at git now; otherwise the wait is however long the
	// reconciliation interval happens to be, which reads as a stuck release.
	if err := c.ArgoCD.Refresh(ctx, p.App); err != nil {
		return ItemStatus{}, err
	}
	w := p.Workload
	obj, err := c.ArgoCD.Resource(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name)
	if err != nil {
		return ItemStatus{}, fmt.Errorf("%s/%s: %w", w.Kind, w.Name, err)
	}
	if want := desiredReplicas(obj); want != int64(p.To) {
		return ItemStatus{Waiting: fmt.Sprintf("Argo CD 还没把副本数同步过来（现在是 %d，要 %d）", want, p.To)}, nil
	}
	if failed, why := rolloutFailed(w.Kind, obj); failed {
		return ItemStatus{Done: true, Error: fmt.Sprintf("%s/%s: %s", w.Kind, w.Name, why)}, nil
	}
	if done, why := rolloutComplete(w.Kind, obj); !done {
		return ItemStatus{Waiting: fmt.Sprintf("%s/%s: %s", w.Kind, w.Name, why)}, nil
	}
	return ItemStatus{Done: true, Success: true}, nil
}

// desiredReplicas is what the object asks for, which is what tells us whether
// the sync has arrived yet. Absent means one, as it does to Kubernetes.
func desiredReplicas(obj map[string]any) int64 {
	if v, ok := lookup(obj, "spec", "replicas"); ok {
		return toInt(v)
	}
	return 1
}

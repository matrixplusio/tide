package plan

import (
	"context"
	"slices"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

// ConfigDiff is what syncing an Application would change right now.
type ConfigDiff struct {
	App      string                   `json:"app"`
	Revision string                   `json:"revision"`
	Sync     string                   `json:"sync"`
	Changes  []release.ResourceChange `json:"changes"`
	// NeedsRestart: only ConfigMaps / Secrets change, so the pods would keep
	// the old values until restarted.
	NeedsRestart bool `json:"needsRestart"`
}

// Deletes counts the changes that remove a resource.
func (d *ConfigDiff) Deletes() int {
	n := 0
	for _, c := range d.Changes {
		if c.Action == release.ChangeDelete {
			n++
		}
	}
	return n
}

// DiffConfig compares git with the cluster for d's Application. refresh asks
// Argo CD to re-read git first (a commit pushed seconds ago) and waits a few
// seconds for it.
func DiffConfig(ctx context.Context, hub *catalog.Hub, d *catalog.Deployment, refresh bool) (*ConfigDiff, error) {
	c, err := hub.Named(ctx, d.Upstream)
	if err != nil {
		return nil, err
	}
	app, err := c.ArgoCD.GetApplication(ctx, d.App)
	if err != nil {
		return nil, err
	}
	if refresh {
		if app, err = refreshApp(ctx, c.ArgoCD, app); err != nil {
			return nil, err
		}
	}
	items, err := c.ArgoCD.ManagedResources(ctx, d.App)
	if err != nil {
		return nil, err
	}
	out := &ConfigDiff{App: d.App, Revision: app.Status.Sync.Revision, Sync: app.Status.Sync.Status, Changes: []release.ResourceChange{}}
	pruning := map[string]bool{}
	for _, r := range app.Status.Resources {
		if r.RequiresPruning {
			pruning[r.Kind+"/"+r.Namespace+"/"+r.Name] = true
		}
	}
	workloadChanged, configChanged := false, false
	for _, r := range items {
		if r.Hook {
			continue
		}
		ch, err := resourceChange(r, pruning[r.Kind+"/"+r.Namespace+"/"+r.Name])
		if err != nil {
			return nil, err
		}
		if ch == nil {
			continue
		}
		out.Changes = append(out.Changes, *ch)
		switch {
		case slices.Contains(RestartableKinds, r.Kind):
			workloadChanged = true
		case (r.Kind == "ConfigMap" || r.Kind == "Secret") && ch.Action == release.ChangeUpdate:
			configChanged = true
		}
	}
	slices.SortFunc(out.Changes, func(a, b release.ResourceChange) int {
		return cmpStr(a.Kind+"/"+a.Namespace+"/"+a.Name, b.Kind+"/"+b.Namespace+"/"+b.Name)
	})
	out.NeedsRestart = configChanged && !workloadChanged
	return out, nil
}

func resourceChange(r argocd.ManagedResource, prune bool) (*release.ResourceChange, error) {
	live, err := manifestYAML(r.NormalizedLiveState)
	if err != nil {
		return nil, err
	}
	target, err := manifestYAML(r.PredictedLiveState)
	if err != nil {
		return nil, err
	}
	ch := &release.ResourceChange{Group: r.Group, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name}
	switch {
	case isNull(r.LiveState) && !isNull(r.TargetState):
		ch.Action = release.ChangeCreate
		if target == "" {
			target, _ = manifestYAML(r.TargetState)
		}
	case isNull(r.TargetState) && !isNull(r.LiveState):
		if !prune {
			return nil, nil
		}
		ch.Action, target = release.ChangeDelete, ""
	case r.Modified:
		ch.Action = release.ChangeUpdate
	default:
		return nil, nil
	}
	ch.Diff, ch.Truncated = unifiedDiff(live, target)
	return ch, nil
}

func isNull(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || s == "null"
}

// refreshApp asks Argo CD to re-read git and waits up to 8s for the next
// reconciliation, returning the latest Application either way.
func refreshApp(ctx context.Context, a *argocd.Client, app *argocd.Application) (*argocd.Application, error) {
	before := app.Status.ReconciledAt
	if err := a.Refresh(ctx, app.Metadata.Name); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		cur, err := a.GetApplication(ctx, app.Metadata.Name)
		if err != nil {
			return nil, err
		}
		if (cur.Status.ReconciledAt != nil && (before == nil || cur.Status.ReconciledAt.After(*before))) || time.Now().After(deadline) {
			return cur, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// BuildSync pins a config sync of d to the revision and changes the person
// reviews. prune must be set when the sync deletes resources; restart forces
// a rolling restart after the sync (it is implied when only a ConfigMap or
// Secret changes).
func BuildSync(ctx context.Context, hub *catalog.Hub, d *catalog.Deployment, prune, restart bool) (*release.SyncPayload, error) {
	if d.Promoting != "" {
		return nil, errf("pl.promotingSync", d.Promoting)
	}
	diff, err := DiffConfig(ctx, hub, d, true)
	if err != nil {
		return nil, err
	}
	if len(diff.Changes) == 0 {
		return nil, errf("pl.nothingToSync", d.App)
	}
	if n := diff.Deletes(); n > 0 && !prune {
		return nil, &PruneError{Count: n}
	}
	c, err := hub.Named(ctx, d.Upstream)
	if err != nil {
		return nil, err
	}
	tree, err := c.ArgoCD.ResourceTree(ctx, d.App)
	if err != nil {
		return nil, err
	}
	p := &release.SyncPayload{
		Upstream: d.Upstream, Service: d.Service, Env: d.Env, App: d.App, Project: d.KargoProject, Stage: d.KargoStage,
		Revision: diff.Revision,
		Current:  release.Artifact{Digest: d.Digest, Tag: d.Tag, Version: d.Version, BuiltAt: d.BuiltAt},
		Changes:  diff.Changes,
		Prune:    prune && diff.Deletes() > 0,
		Restart:  restart || diff.NeedsRestart,
	}
	for _, n := range tree.Nodes {
		if len(n.ParentRefs) == 0 && n.Group == "apps" && slices.Contains(RestartableKinds, n.Kind) {
			p.Workloads = append(p.Workloads, release.Workload{Group: n.Group, Version: n.Version, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name})
		}
	}
	slices.SortFunc(p.Workloads, func(a, b release.Workload) int { return cmpStr(a.Kind+"/"+a.Name, b.Kind+"/"+b.Name) })
	return p, nil
}

// PruneError: the sync deletes resources and the person has not agreed.
type PruneError struct{ Count int }

func (e *PruneError) Error() string {
	return i18n.T(i18n.Default, "pl.pruneWarning", e.Count)
}

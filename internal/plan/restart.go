package plan

import (
	"context"
	"slices"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

// RestartableKinds are the workloads Argo CD's built-in "restart" action supports.
var RestartableKinds = []string{"Deployment", "StatefulSet", "DaemonSet"}

// topWorkloads lists the workloads of an Application a restart or a sync
// acts on: the top-level apps workloads and any Argo Rollout. A Deployment a
// Rollout adopts through workloadRef is left out — it is scaled to nothing
// and kept only for its pod template, so restarting it does nothing and
// waiting on it proves nothing.
func topWorkloads(ctx context.Context, c *catalog.Clients, app string, nodes []argocd.ResourceNode) ([]release.Workload, error) {
	var out []release.Workload
	adopted := map[string]bool{}
	for _, n := range nodes {
		if len(n.ParentRefs) != 0 {
			continue
		}
		switch {
		case n.Group == "apps" && slices.Contains(RestartableKinds, n.Kind):
		case n.Group == "argoproj.io" && n.Kind == "Rollout":
			obj, err := c.ArgoCD.Resource(ctx, app, n.Group, n.Version, n.Kind, n.Namespace, n.Name)
			if err != nil {
				return nil, err
			}
			if ref, _ := lookupStr(obj, "spec", "workloadRef", "name"); ref != "" {
				adopted[n.Namespace+"/"+ref] = true
			}
		default:
			continue
		}
		out = append(out, release.Workload{Group: n.Group, Version: n.Version, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name})
	}
	out = slices.DeleteFunc(out, func(w release.Workload) bool {
		return w.Kind == "Deployment" && adopted[w.Namespace+"/"+w.Name]
	})
	slices.SortFunc(out, func(a, b release.Workload) int { return cmpStr(a.Kind+"/"+a.Name, b.Kind+"/"+b.Name) })
	return out, nil
}

func lookupStr(obj map[string]any, path ...string) (string, bool) {
	var cur any = obj
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = m[k]
	}
	s, ok := cur.(string)
	return s, ok
}

// BuildRestart resolves the workloads a restart of d touches. Only top-level
// workloads managed by the Application count; ReplicaSets and Pods follow.
func BuildRestart(ctx context.Context, hub *catalog.Hub, d *catalog.Deployment) (*release.RestartPayload, error) {
	if d.Promoting != "" {
		return nil, errf("pl.promotingRestart", d.Promoting)
	}
	c, err := hub.Named(ctx, d.Upstream)
	if err != nil {
		return nil, err
	}
	tree, err := c.ArgoCD.ResourceTree(ctx, d.App)
	if err != nil {
		return nil, err
	}
	p := &release.RestartPayload{
		Upstream: d.Upstream, Service: d.Service, Env: d.Env, App: d.App, Project: d.KargoProject, Stage: d.KargoStage,
		Current:   release.Artifact{Digest: d.Digest, Tag: d.Tag, Version: d.Version, BuiltAt: d.BuiltAt},
		Workloads: []release.Workload{},
	}
	ws, err := topWorkloads(ctx, c, d.App, tree.Nodes)
	if err != nil {
		return nil, err
	}
	p.Workloads = append(p.Workloads, ws...)
	if len(p.Workloads) == 0 {
		return nil, errf("pl.nothingToRestart", d.App)
	}
	return p, nil
}

package plan

import (
	"context"
	"slices"

	"tide/internal/catalog"
	"tide/internal/release"
)

// RestartableKinds are the workloads Argo CD's built-in "restart" action supports.
var RestartableKinds = []string{"Deployment", "StatefulSet", "DaemonSet"}

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
	for _, n := range tree.Nodes {
		if len(n.ParentRefs) == 0 && n.Group == "apps" && slices.Contains(RestartableKinds, n.Kind) {
			p.Workloads = append(p.Workloads, release.Workload{Group: n.Group, Version: n.Version, Kind: n.Kind, Namespace: n.Namespace, Name: n.Name})
		}
	}
	if len(p.Workloads) == 0 {
		return nil, errf("pl.nothingToRestart", d.App)
	}
	slices.SortFunc(p.Workloads, func(a, b release.Workload) int { return cmpStr(a.Kind+"/"+a.Name, b.Kind+"/"+b.Name) })
	return p, nil
}

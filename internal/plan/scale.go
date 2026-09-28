package plan

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"

	"tide/internal/catalog"
	"tide/internal/manifest"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/upstream/gitea"
	"tide/internal/upstream/gitlab"
	"tide/internal/upstream/repo"
)

// ManifestFile is the file inside a service's directory that holds the replica
// count. Fixed rather than searched for: reading every file in the directory to
// find the workload costs a request each, and a repository that names it
// something else is better off saying so than having Tide guess.
const ManifestFile = "deployment.yaml"

// BuildScale reads the count the service is set to now and describes the move
// to another one.
//
// The count is read from git, not from the cluster. They disagree exactly when
// something has changed the cluster behind the Application's back, and in that
// case git is what the next sync will apply — so it is what a person deciding
// the new number needs to see.
func BuildScale(ctx context.Context, hub *catalog.Hub, set *settings.Store, d *catalog.Deployment, to int) (*release.ScalePayload, error) {
	if d.Promoting != "" {
		return nil, errf("pl.promotingRestart", d.Promoting)
	}
	if to < 0 || to > release.MaxReplicas {
		return nil, errf("pl.replicasRange", to, release.MaxReplicas)
	}
	// Before reading the repository: a budget that pins the last Pod is a
	// reason to stop that costs nothing to find, and it is the one a person
	// can act on (change the budget) rather than a symptom.
	c, err := hub.Named(ctx, d.Upstream)
	if err != nil {
		return nil, err
	}
	if pdb, err := BlockingPDB(ctx, c, d.App, to); err != nil {
		return nil, err
	} else if pdb != "" {
		return nil, errf("pl.pdbBlocks", pdb, to)
	}
	var cfg settings.AppsRepo
	if err := set.Load(ctx, settings.SectionAppsRepo, &cfg); err != nil && !errors.Is(err, settings.ErrNotConfigured) {
		return nil, err
	}
	if !cfg.Configured() {
		return nil, errf("pl.noAppsRepo")
	}
	if d.RepoPath == "" {
		return nil, errf("pl.noManifestPath", d.Service, d.Env)
	}
	file := path.Join(d.RepoPath, ManifestFile)
	text, err := readFile(ctx, cfg, cfg.Branch, file)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, errf("pl.manifestMissing", file)
		}
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	doc, err := manifest.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if !slices.Contains(ScalableKinds, doc.Kind()) {
		return nil, errf("pl.notScalable", file, doc.Kind())
	}
	from, explicit := doc.Replicas()
	if !explicit {
		// Nothing to change, and adding the field would start a fight with
		// whatever does decide the count — an autoscaler, or a patch layered
		// on somewhere Tide cannot see.
		return nil, errf("pl.noReplicasField", file)
	}
	if from == to {
		return nil, errf("pl.alreadyAtReplicas", d.Service, d.Env, to)
	}
	w, err := scaleTarget(ctx, c, d, doc.Name())
	if err != nil {
		return nil, err
	}
	return &release.ScalePayload{
		Upstream: d.Upstream, Service: d.Service, Env: d.Env, App: d.App,
		Project: d.KargoProject, Stage: d.KargoStage,
		Path: file, From: from, To: to, Workload: *w,
		Current: release.Artifact{Digest: d.Digest, Tag: d.Tag, Version: d.Version, BuiltAt: d.BuiltAt},
	}, nil
}

// ScalableKinds are the workload kinds whose replica count means what this
// release thinks it means. A DaemonSet has no replicas at all, and a CronJob's
// concurrency is a different idea wearing a similar name.
var ScalableKinds = []string{"Deployment", "StatefulSet", "Rollout"}

// scaleTarget finds the Application resource the manifest describes, so that
// Poll watches the object that is actually going to move. Matching by name and
// kind rather than trusting the file: the two disagree when a manifest has been
// renamed and the Application not yet synced, and waiting on an object that
// does not exist looks exactly like waiting on one that never becomes ready.
func scaleTarget(ctx context.Context, c *catalog.Clients, d *catalog.Deployment, name string) (*release.Workload, error) {
	app, err := c.ArgoCD.GetApplication(ctx, d.App)
	if err != nil {
		return nil, err
	}
	for _, r := range app.Status.Resources {
		if r.Name == name && slices.Contains(ScalableKinds, r.Kind) {
			return &release.Workload{Group: r.Group, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name}, nil
		}
	}
	return nil, errf("pl.workloadNotInApp", name, d.App)
}

func readFile(ctx context.Context, cfg settings.AppsRepo, ref, file string) (string, error) {
	if cfg.Host() == settings.ProviderGitea {
		return gitea.New(cfg.BaseURL, cfg.Project, cfg.Token, false).Read(ctx, ref, file)
	}
	return gitlab.New(cfg.BaseURL, cfg.Project, cfg.Token, false).Read(ctx, ref, file)
}

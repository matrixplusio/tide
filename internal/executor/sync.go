package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/plan"
	"tide/internal/release"
)

// Sync applies reviewed git configuration by syncing the Argo CD Application,
// optionally restarts the workloads, and waits for them to roll out.
type Sync struct {
	Hub *catalog.Hub
}

func (Sync) Kind() string { return release.KindSync }

func (x Sync) clients(ctx context.Context, it *release.Item) (*release.SyncPayload, *catalog.Clients, error) {
	var p release.SyncPayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return nil, nil, err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return nil, nil, err
	}
	return &p, c, nil
}

// Validate: no promotion is running, the workloads still run the recorded
// version, and a sync now would change exactly what the person reviewed. The
// deploy repo is shared, so the revision itself may move; the rendered
// changes may not.
func (x Sync) Validate(ctx context.Context, it *release.Item) error {
	p, c, err := x.clients(ctx, it)
	if err != nil {
		return err
	}
	if p.Project != "" && p.Stage != "" {
		stage, err := c.Kargo.GetStage(ctx, p.Project, p.Stage)
		if err != nil {
			return err
		}
		if stage.Status.CurrentPromotion != nil {
			return fmt.Errorf("stage %s has promotion %s running; sync after it finishes", p.Stage, stage.Status.CurrentPromotion.Name)
		}
	}
	for _, w := range p.Workloads {
		obj, err := c.ArgoCD.Resource(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", w.Kind, w.Name, err)
		}
		if !runsArtifact(obj, p.Current) {
			return fmt.Errorf("%s/%s no longer runs %s; create a new release", w.Kind, w.Name, labelOf(p.Current))
		}
	}
	_, err = x.current(ctx, p, true)
	return err
}

// current re-diffs the Application and returns it when it still matches the
// reviewed changes.
func (x Sync) current(ctx context.Context, p *release.SyncPayload, refresh bool) (*plan.ConfigDiff, error) {
	d, err := plan.DiffConfig(ctx, x.Hub, &catalog.Deployment{Upstream: p.Upstream, App: p.App, Service: p.Service, Env: p.Env}, refresh)
	if err != nil {
		return nil, err
	}
	if why := sameChanges(p.Changes, d.Changes); why != "" {
		return nil, fmt.Errorf("configuration changed since the release was created (%s); create a new release", why)
	}
	return d, nil
}

func sameChanges(want, got []release.ResourceChange) string {
	key := func(c release.ResourceChange) string {
		return c.Action + " " + c.Kind + "/" + c.Namespace + "/" + c.Name
	}
	idx := map[string]release.ResourceChange{}
	for _, c := range got {
		idx[key(c)] = c
	}
	for _, c := range want {
		g, ok := idx[key(c)]
		switch {
		case !ok:
			return key(c) + " no longer pending"
		case !c.Truncated && !g.Truncated && g.Diff != c.Diff:
			return key(c) + " differs"
		}
		delete(idx, key(c))
	}
	for k := range idx {
		return k + " is new"
	}
	return ""
}

func (x Sync) Execute(ctx context.Context, it *release.Item) (string, error) {
	p, c, err := x.clients(ctx, it)
	if err != nil {
		return "", err
	}
	d, err := x.current(ctx, p, false)
	if err != nil {
		return "", err
	}
	started := time.Now().UTC()
	if err := c.ArgoCD.Sync(ctx, p.App, d.Revision, p.Prune); err != nil {
		return "", err
	}
	return "sync@" + started.Format(time.RFC3339) + "@" + d.Revision, nil
}

// syncStarted parses the time Execute recorded in the external reference.
func syncStarted(ref string) (time.Time, bool) {
	parts := strings.SplitN(strings.TrimPrefix(ref, "sync@"), "@", 2)
	t, err := time.Parse(time.RFC3339, parts[0])
	return t, err == nil
}

// Poll: the Argo CD sync operation Tide started succeeded, then (with
// Restart) every workload was restarted, then every workload rolled out.
func (x Sync) Poll(ctx context.Context, it *release.Item) (ItemStatus, error) {
	p, c, err := x.clients(ctx, it)
	if err != nil {
		return ItemStatus{}, err
	}
	started, ok := syncStarted(it.ExternalRef)
	if !ok {
		return ItemStatus{Done: true, Error: "missing sync reference " + it.ExternalRef}, nil
	}
	app, err := c.ArgoCD.GetApplication(ctx, p.App)
	if err != nil {
		return ItemStatus{}, err
	}
	op := app.Status.OperationState
	switch {
	case op == nil || op.StartedAt == nil || op.StartedAt.Before(started.Add(-time.Second)):
		return ItemStatus{Waiting: "waiting for Argo CD to start the sync"}, nil
	case op.Phase == "Failed" || op.Phase == "Error":
		return ItemStatus{Done: true, Error: "Argo CD sync " + strings.ToLower(op.Phase) + ": " + op.Message}, nil
	case op.Phase != "Succeeded":
		return ItemStatus{Waiting: "Argo CD sync " + op.Phase + ": " + op.Message}, nil
	}
	a := c.ArgoCD
	var waiting []string
	for _, w := range p.Workloads {
		obj, err := a.Resource(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name)
		if err != nil {
			return ItemStatus{}, fmt.Errorf("%s/%s: %w", w.Kind, w.Name, err)
		}
		if p.Restart && !restartedSince(obj, started) {
			if err := a.RunResourceAction(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name, "restart"); err != nil {
				return ItemStatus{}, fmt.Errorf("restart %s/%s: %w", w.Kind, w.Name, err)
			}
			waiting = append(waiting, fmt.Sprintf("%s/%s: restart requested", w.Kind, w.Name))
			continue
		}
		if failed, why := rolloutFailed(w.Kind, obj); failed {
			return ItemStatus{Done: true, Error: fmt.Sprintf("%s/%s: %s", w.Kind, w.Name, why)}, nil
		}
		if done, why := rolloutComplete(w.Kind, obj); !done {
			waiting = append(waiting, fmt.Sprintf("%s/%s: %s", w.Kind, w.Name, why))
		}
	}
	if len(waiting) > 0 {
		return ItemStatus{Waiting: strings.Join(waiting, "; ")}, nil
	}
	return ItemStatus{Done: true, Success: true}, nil
}

// restartedSince: the workload carries a restart annotation from t or later.
func restartedSince(obj map[string]any, t time.Time) bool {
	at, err := time.Parse(time.RFC3339, restartedAt(obj))
	return err == nil && !at.Before(t.Truncate(time.Second))
}

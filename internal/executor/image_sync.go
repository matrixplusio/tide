package executor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/plan"
	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

// What a release does about the sync in an environment Argo CD does not sync
// by itself. Such an environment is run that way so that a commit nobody
// reviewed cannot reach the cluster; the release is the review, so it starts
// the sync — and only for what it showed the people who approved it.

type syncStep int

const (
	syncStart   syncStep = iota // nothing has synced the promotion's commit yet
	syncWait                    // an operation is running
	syncSettled                 // the sync has run; the rollout decides the rest
	syncFailed
)

// nextSync reads the Application's last operation. One that started after the
// promotion finished is the sync this release is waiting for, whoever started
// it; an older one says nothing about the new commit.
func nextSync(phase, message string, startedAt *time.Time, since time.Time) (syncStep, string) {
	running := phase == "Running" || phase == "Terminating"
	if startedAt == nil || startedAt.Before(since.Add(-time.Second)) {
		if startedAt != nil && running {
			return syncWait, "an earlier Argo CD operation is still running"
		}
		return syncStart, ""
	}
	switch phase {
	case "Failed", "Error":
		return syncFailed, "Argo CD sync " + strings.ToLower(phase) + ": " + message
	case "Succeeded":
		return syncSettled, ""
	}
	return syncWait, "Argo CD sync " + phase
}

// imageKinds are the objects a promotion changes: the ones that name an image.
var imageKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob", "Job", "Rollout"}

// approveSync decides whether syncing now would apply only what the release
// showed, and whether that includes deleting anything.
//
// recorded is what git held unapplied when the release was created — the list
// its confirmation and approval were given against. pending is the same list
// now, plus the image the promotion just committed. Anything else arrived in
// between and nobody has looked at it, so the sync does not happen.
//
// Deleting needs more than having been listed: the creator has to have chosen
// to send the configuration along. And a Namespace is never deleted this way,
// listed or not — it takes everything in it, and one release is the wrong
// place to decide that.
func approveSync(recorded, pending []release.ResourceChange, withConfig bool) (prune bool, refused string) {
	key := func(c release.ResourceChange) string { return c.Kind + "/" + c.Namespace + "/" + c.Name }
	seen := map[string]release.ResourceChange{}
	for _, c := range recorded {
		seen[key(c)] = c
	}
	for _, c := range pending {
		r, was := seen[key(c)]
		switch {
		case c.Action == release.ChangeDelete:
			if !was || r.Action != release.ChangeDelete {
				return false, fmt.Sprintf("%s would be deleted, which this release did not show", key(c))
			}
			if !withConfig {
				continue // left in the cluster: nobody asked for it to go
			}
			if c.Kind == "Namespace" {
				return false, fmt.Sprintf("%s would be deleted; a release never deletes a namespace", key(c))
			}
			prune = true
		case slices.Contains(imageKinds, c.Kind):
			if was {
				continue // reviewed along with whatever else it carried
			}
			if c.Action != release.ChangeUpdate || c.Truncated || !plan.OnlyImageChanged(c.Diff) {
				return false, fmt.Sprintf("%s changes more than its image, which this release did not show", key(c))
			}
		case !was || r.Action != c.Action:
			return false, fmt.Sprintf("%s changed in git after this release was created", key(c))
		case !c.Truncated && !r.Truncated && r.Diff != c.Diff:
			return false, fmt.Sprintf("%s is not what this release showed any more", key(c))
		}
	}
	return prune, ""
}

// seesTarget reports whether Argo CD has read the promotion's commit: a
// workload is about to change to the target. Until it has, a sync would apply
// the commit before, and report success for a release that changed nothing.
func seesTarget(pending []release.ResourceChange, target release.Artifact) bool {
	if target.Tag == "" {
		return true
	}
	return slices.ContainsFunc(pending, func(c release.ResourceChange) bool {
		return c.Action != release.ChangeDelete && slices.Contains(imageKinds, c.Kind) &&
			(c.Truncated || strings.Contains(c.Diff, target.Tag))
	})
}

func (x Image) syncThenRollout(ctx context.Context, c *catalog.Clients, p *release.ImagePayload, app *argocd.Application, since *time.Time) (ItemStatus, error) {
	if since == nil {
		return ItemStatus{Done: true, Error: "cannot tell when the promotion finished, so not which sync belongs to it; sync " + p.App + " by hand"}, nil
	}
	var phase, message string
	var startedAt *time.Time
	if op := app.Status.OperationState; op != nil {
		phase, message, startedAt = op.Phase, op.Message, op.StartedAt
	}
	switch step, why := nextSync(phase, message, startedAt, *since); step {
	case syncFailed:
		return ItemStatus{Done: true, Error: why}, nil
	case syncWait:
		return ItemStatus{Waiting: why}, nil
	case syncSettled:
		return x.rollout(ctx, c, p, app)
	}
	diff, err := plan.DiffConfig(ctx, x.Hub, &catalog.Deployment{Upstream: p.Upstream, App: p.App, Service: p.Service, Env: p.Env}, true)
	if err != nil {
		return ItemStatus{}, err
	}
	ws, err := x.workloads(ctx, c, p, app)
	if err != nil {
		return ItemStatus{}, err
	}
	runs := slices.ContainsFunc(ws, func(w workload) bool { return runsArtifact(w.obj, p.To) })
	if len(diff.Changes) == 0 && runs {
		return rolloutStatus(ws, p.To), nil // nothing left to apply
	}
	if !runs && !seesTarget(diff.Changes, p.To) {
		return ItemStatus{Waiting: "waiting for Argo CD to read the promotion's commit"}, nil
	}
	prune, refused := approveSync(p.ConfigChanges, diff.Changes, p.WithConfig)
	if refused != "" {
		// The tag is already in git and stays there; saying so is what lets
		// whoever reads this know the cluster and git now disagree.
		return ItemStatus{Done: true, Error: "not synced: " + refused + ". The new tag is committed but nothing was applied; create a new release"}, nil
	}
	// Pinned to the revision just compared, so a commit landing between the
	// comparison and the sync is not applied unseen.
	if err := c.ArgoCD.Sync(ctx, p.App, diff.Revision, prune); err != nil {
		return ItemStatus{}, err
	}
	return ItemStatus{Waiting: "Argo CD sync requested"}, nil
}

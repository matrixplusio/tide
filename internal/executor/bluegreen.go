package executor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/upstream/kargo"
)

// After a blue-green switch the Rollout keeps the previous version's
// ReplicaSet running for its scaleDownDelaySeconds and then scales it to
// nothing on its own. That stretch is the window: rolling back inside it is a
// switch back to pods that are already up. Tide does not time it. It reads the
// deadline the controller wrote onto the ReplicaSet, and ends the window early
// by moving that deadline to now through an Argo CD resource action, so that
// the scaling down is still the controller's own.

// scaleDownDeadline is the annotation Argo Rollouts puts on the previous
// version's ReplicaSet: when it will be scaled down. The key has no prefix;
// read off DefaultReplicaSetScaleDownDeadlineAnnotationKey in the controller,
// not assumed.
const scaleDownDeadline = "scale-down-deadline"

// endObservationAction is the custom Argo CD action on ReplicaSet that moves
// the deadline to now. It is configured in Argo CD, not built in; its Lua
// refuses a ReplicaSet that is not a Rollout's retained previous version.
const endObservationAction = "scale-down-now"

// ErrNotObserving: no previous version is being kept, so there is no window
// to end or to roll back within.
var ErrNotObserving = errors.New("no previous version is being kept")

// Retained is the previous version a blue-green Rollout keeps after a switch.
type Retained struct {
	Rollout    string `json:"rollout"`
	ReplicaSet string `json:"replicaSet"`
	Namespace  string `json:"-"`
	// Images are what its pods run: the version a rollback would switch to.
	Images []string `json:"images"`
	// Until is when the controller will scale it down.
	Until    time.Time `json:"until"`
	Replicas int64     `json:"replicas"`
	Ready    int64     `json:"ready"`
}

// Usable says the previous version can still take traffic back: before its
// deadline, with at least one pod ready. Not all of them: an autoscaler
// growing the Rollout grows the kept ReplicaSet too, and for those seconds
// "every replica ready" is false though nothing is wrong. Waiting for the
// rest is the controller's job — it switches the Service back only once the
// ReplicaSet is fully available, and traffic stays where it is until then.
func (r Retained) Usable(now time.Time) bool {
	return now.Before(r.Until) && r.Replicas > 0 && r.Ready > 0
}

// RetainedOf finds the previous versions app's blue-green Rollouts are
// keeping. None is not an error: most of the time there are none.
func RetainedOf(ctx context.Context, c *catalog.Clients, app string) ([]Retained, error) {
	a, err := c.ArgoCD.GetApplication(ctx, app)
	if err != nil {
		return nil, err
	}
	var rollouts []string
	for _, r := range appWorkloads(a) {
		if r.Kind == rolloutKind {
			rollouts = append(rollouts, r.Namespace+"/"+r.Name)
		}
	}
	if len(rollouts) == 0 {
		return []Retained{}, nil
	}
	tree, err := c.ArgoCD.ResourceTree(ctx, app)
	if err != nil {
		return nil, err
	}
	out := []Retained{}
	for _, n := range tree.Nodes {
		if n.Kind != "ReplicaSet" || n.Group != "apps" {
			continue
		}
		owner := ""
		for _, p := range n.ParentRefs {
			if p.Kind == rolloutKind {
				owner = p.Name
			}
		}
		if owner == "" || !slices.Contains(rollouts, n.Namespace+"/"+owner) {
			continue
		}
		obj, err := c.ArgoCD.Resource(ctx, app, "apps", "v1", "ReplicaSet", n.Namespace, n.Name)
		if err != nil {
			return nil, fmt.Errorf("replicaset %s: %w", n.Name, err)
		}
		r, ok := retainedFrom(obj)
		if !ok {
			continue
		}
		r.Rollout, r.ReplicaSet, r.Namespace = owner, n.Name, n.Namespace
		out = append(out, r)
	}
	return out, nil
}

// retainedFrom reads a ReplicaSet the controller has marked for scaling down
// and has not scaled yet. One already at zero is history, not a window.
func retainedFrom(rs map[string]any) (Retained, bool) {
	v, _ := lookup(rs, "metadata", "annotations", scaleDownDeadline)
	s, _ := v.(string)
	if s == "" {
		return Retained{}, false
	}
	until, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Retained{}, false
	}
	r := Retained{Until: until, Replicas: num(rs, "spec", "replicas"), Ready: num(rs, "status", "readyReplicas"),
		Images: containerImages(rs)}
	if r.Replicas == 0 {
		return Retained{}, false
	}
	return r, true
}

// EndObservation scales down the previous versions app is keeping, now
// instead of at their deadline. It returns how many it ended.
func EndObservation(ctx context.Context, c *catalog.Clients, app string) (int, error) {
	kept, err := RetainedOf(ctx, c, app)
	if err != nil {
		return 0, err
	}
	if len(kept) == 0 {
		return 0, ErrNotObserving
	}
	for i, r := range kept {
		if err := c.ArgoCD.RunResourceAction(ctx, app, "apps", "v1", "ReplicaSet", r.Namespace, r.ReplicaSet, endObservationAction); err != nil {
			return i, fmt.Errorf("replicaset %s %s: %w", r.ReplicaSet, endObservationAction, err)
		}
	}
	return len(kept), nil
}

// Runs says the retained version's pods run a.
func (r Retained) Runs(a release.Artifact) bool {
	return runsArtifact(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": imagesAsContainers(r.Images)}}}}, a)
}

func imagesAsContainers(images []string) []any {
	out := make([]any, len(images))
	for i, img := range images {
		out[i] = map[string]any{"image": img}
	}
	return out
}

// PreviousFreight finds the Freight carrying the version p replaced, as the
// automatic revert after an abort does.
func PreviousFreight(avail []kargo.Freight, p *release.ImagePayload) string {
	if p.From == nil {
		return ""
	}
	return previousFreight(avail, p)
}

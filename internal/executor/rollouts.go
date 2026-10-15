package executor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/upstream/argocd"
)

// Argo Rollouts: a Rollout replaces a Deployment and moves to a new version
// by blue-green or canary, stopping on the way for a person to let it go on.
// Tide reads its state and drives it only through Argo CD's resource actions,
// never against the cluster directly.

const (
	rolloutGroup   = "argoproj.io"
	rolloutVersion = "v1alpha1"
	rolloutKind    = "Rollout"
)

// RolloutAction is what a person may ask of a rollout that is waiting. The
// set is closed: a request names one of these, never an Argo CD action.
type RolloutAction string

const (
	// ActionNext lets a blue-green rollout switch traffic, or a canary go on
	// to its next step.
	ActionNext RolloutAction = "next"
	// ActionFull takes a canary to the new version at once, skipping its
	// remaining steps.
	ActionFull RolloutAction = "full"
	// ActionAbort sends traffic back to the version that was running before.
	ActionAbort RolloutAction = "abort"
)

// argoAction is the name Argo CD's built-in Rollout actions use. Read off a
// running Argo CD (resource/actions on a Rollout lists them), not guessed.
func (a RolloutAction) argoAction() (string, bool) {
	switch a {
	case ActionNext:
		return "resume", true
	case ActionFull:
		return "promote-full", true
	case ActionAbort:
		return "abort", true
	}
	return "", false
}

// ErrNotWaiting: the rollout is not stopped for a person, so there is nothing
// to let through.
var ErrNotWaiting = errors.New("the rollout is not waiting for a decision")

// Pause describes a rollout stopped for a person.
type Pause struct {
	// Since is when the rollout stopped, as the rollout itself recorded it;
	// zero when it did not say.
	Since    time.Time `json:"since"`
	Strategy string    `json:"strategy"` // blueGreen | canary
	Reason   string    `json:"reason,omitempty"`
	// Step and Steps place a canary in its plan, counting from one; Weight
	// is the share of traffic the new version has, -1 when unknown.
	Step   int `json:"step,omitempty"`
	Steps  int `json:"steps,omitempty"`
	Weight int `json:"weight"`
}

// rolloutState is what one Rollout says about a release moving it to target.
type rolloutState struct {
	onTarget bool
	done     bool
	failed   string
	// aborted: someone (or the switch timeout) aborted the rollout. Traffic
	// is back on the old version, but git and Kargo still name the new one.
	aborted bool
	paused  *Pause
	waiting string
}

// judgeRollout reads a Rollout. tmpl is the object holding its pod template:
// the Rollout itself, or the Deployment it adopts through workloadRef — the
// form a service takes when it moves to Argo Rollouts without being
// recreated, where the image lives on the Deployment and the Rollout carries
// none.
func judgeRollout(ro, tmpl map[string]any, target release.Artifact) rolloutState {
	// Nothing in status is about this release until the controller has seen
	// the spec that carries it. The two generations are strings on a Rollout.
	observed := num(ro, "status", "observedGeneration") >= num(ro, "metadata", "generation")
	if _, adopted := lookup(ro, "spec", "workloadRef"); adopted {
		observed = observed && num(ro, "status", "workloadObservedGeneration") >= num(tmpl, "metadata", "generation")
	}
	if !observed || !runsArtifact(tmpl, target) {
		return rolloutState{waiting: "rollout has not picked up " + labelOf(target) + " yet"}
	}
	st := rolloutState{onTarget: true}
	phase, msg := str(ro, "status", "phase"), str(ro, "status", "message")
	current, stable := str(ro, "status", "currentPodHash"), str(ro, "status", "stableRS")
	if v, _ := lookup(ro, "status", "abort"); v == true {
		// Aborted, but the template is the stable version again: that is a
		// rollback finished, which is what a revert waits for.
		if current != "" && current == stable {
			st.done = true
			return st
		}
		st.aborted = true
		st.failed = "rollout aborted; traffic is on the previous version"
		if msg != "" {
			st.failed += ": " + msg
		}
		return st
	}
	if phase == "Degraded" {
		st.failed = "rollout degraded: " + msg
		return st
	}
	if p := pauseOf(ro); p != nil {
		st.paused = p
		return st
	}
	settled := phase == "Healthy" && current != "" && current == stable
	if _, bg := lookup(ro, "spec", "strategy", "blueGreen"); bg {
		settled = settled && str(ro, "status", "blueGreen", "activeSelector") == current
	}
	if settled {
		st.done = true
		return st
	}
	st.waiting = fmt.Sprintf("rollout %s: %d/%d updated, %d ready", strings.ToLower(orNone(phase)),
		num(ro, "status", "updatedReplicas"), num(ro, "spec", "replicas"), num(ro, "status", "readyReplicas"))
	if msg != "" {
		st.waiting += " (" + msg + ")"
	}
	return st
}

// pauseOf reports a Rollout stopped for a person, or nil.
func pauseOf(ro map[string]any) *Pause {
	v, _ := lookup(ro, "status", "pauseConditions")
	conds, _ := v.([]any)
	if len(conds) == 0 && str(ro, "status", "phase") != "Paused" {
		return nil
	}
	p := &Pause{Strategy: "canary", Weight: -1}
	if _, bg := lookup(ro, "spec", "strategy", "blueGreen"); bg {
		p.Strategy = "blueGreen"
	}
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if p.Reason == "" {
			p.Reason, _ = m["reason"].(string)
		}
		if s, _ := m["startTime"].(string); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil && (p.Since.IsZero() || t.Before(p.Since)) {
				p.Since = t
			}
		}
	}
	if p.Strategy == "canary" {
		steps, _ := lookup(ro, "spec", "strategy", "canary", "steps")
		list, _ := steps.([]any)
		p.Steps = len(list)
		idx := -1
		if v, ok := lookup(ro, "status", "currentStepIndex"); ok {
			idx = int(toInt(v))
			p.Step = idx + 1
		}
		// With traffic routing the controller reports the weight it set;
		// without it, the last setWeight reached is the closest there is.
		if w, ok := lookup(ro, "status", "canary", "weights", "canary", "weight"); ok {
			p.Weight = int(toInt(w))
		} else {
			for i := 0; i <= idx && i < len(list); i++ {
				if m, _ := list[i].(map[string]any); m != nil {
					if w, ok := m["setWeight"]; ok {
						p.Weight = int(toInt(w))
					}
				}
			}
		}
	} else {
		p.Weight = 0 // blue-green holds every request on the old version until the switch
	}
	return p
}

// observeWorkloads loads the workloads of app that run containers and pairs
// each Rollout with the object holding its pod template. A Deployment a
// Rollout adopts is scaled to nothing and kept only for its template, so it
// is not judged on its own: zero of zero replicas would read as rolled out.
func observeWorkloads(ctx context.Context, a *argocd.Client, app string, refs []release.Workload) ([]workload, error) {
	var ws []workload
	adopted := map[string]bool{}
	byName := map[string]map[string]any{}
	for _, r := range refs {
		obj, err := a.Resource(ctx, app, r.Group, r.Version, r.Kind, r.Namespace, r.Name)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", r.Kind, r.Name, err)
		}
		if r.Kind == "Deployment" {
			byName[r.Namespace+"/"+r.Name] = obj
		}
		ws = append(ws, workload{kind: r.Kind, name: r.Name, obj: obj, namespace: r.Namespace})
	}
	for i := range ws {
		w := &ws[i]
		if w.kind != rolloutKind {
			continue
		}
		w.tmpl = w.obj
		if ref := str(w.obj, "spec", "workloadRef", "name"); ref != "" && str(w.obj, "spec", "workloadRef", "kind") == "Deployment" {
			key := w.namespace + "/" + ref
			dep, ok := byName[key]
			if !ok {
				obj, err := a.Resource(ctx, app, "apps", "v1", "Deployment", w.namespace, ref)
				if err != nil {
					return nil, fmt.Errorf("deployment %s adopted by rollout %s: %w", ref, w.name, err)
				}
				dep = obj
			}
			w.tmpl = dep
			adopted[key] = true
		}
	}
	return slices.DeleteFunc(ws, func(w workload) bool {
		return w.kind == "Deployment" && adopted[w.namespace+"/"+w.name]
	}), nil
}

// appWorkloads lists the workloads of an Application that a release waits on.
func appWorkloads(app *argocd.Application) []release.Workload {
	var out []release.Workload
	for _, r := range app.Status.Resources {
		apps := r.Group == "apps" && (r.Kind == "Deployment" || r.Kind == "StatefulSet" || r.Kind == "DaemonSet")
		ro := r.Group == rolloutGroup && r.Kind == rolloutKind
		if apps || ro {
			out = append(out, release.Workload{Group: r.Group, Version: r.Version, Kind: r.Kind, Namespace: r.Namespace, Name: r.Name})
		}
	}
	return out
}

// actOnRollouts applies action to every Rollout of app. next and full need a
// rollout that is waiting; abort works on one still in progress too.
func actOnRollouts(ctx context.Context, c *catalog.Clients, app string, action RolloutAction) error {
	name, ok := action.argoAction()
	if !ok {
		return fmt.Errorf("unknown rollout action %q", action)
	}
	a, err := c.ArgoCD.GetApplication(ctx, app)
	if err != nil {
		return err
	}
	n := 0
	for _, r := range appWorkloads(a) {
		if r.Kind != rolloutKind {
			continue
		}
		if action != ActionAbort {
			obj, err := c.ArgoCD.Resource(ctx, app, r.Group, r.Version, r.Kind, r.Namespace, r.Name)
			if err != nil {
				return fmt.Errorf("rollout %s: %w", r.Name, err)
			}
			if pauseOf(obj) == nil {
				continue
			}
		}
		if err := c.ArgoCD.RunResourceAction(ctx, app, r.Group, r.Version, r.Kind, r.Namespace, r.Name, name); err != nil {
			return fmt.Errorf("rollout %s %s: %w", r.Name, name, err)
		}
		n++
	}
	if n == 0 {
		return ErrNotWaiting
	}
	return nil
}

// AppOf names the upstream and Argo CD Application an item acts on.
func AppOf(rel *release.Release, it release.Item) (upstream, app string, ok bool) {
	if p, err := rel.ImagePayload(it); err == nil {
		return p.Upstream, p.App, p.App != ""
	}
	if p, err := rel.SyncPayload(it); err == nil {
		return p.Upstream, p.App, true
	}
	if p, err := rel.RestartPayload(it); err == nil {
		return p.Upstream, p.App, true
	}
	return "", "", false
}

// ActOnItem applies a person's decision to the rollouts an executing item is
// waiting on.
func ActOnItem(ctx context.Context, hub *catalog.Hub, rel *release.Release, it release.Item, action RolloutAction) error {
	upstream, app, ok := AppOf(rel, it)
	if !ok {
		return ErrNotWaiting
	}
	c, err := hub.Named(ctx, upstream)
	if err != nil {
		return err
	}
	return actOnRollouts(ctx, c, app, action)
}

// RolloutView is one Rollout as a release page shows it.
type RolloutView struct {
	Name     string `json:"name"`
	Strategy string `json:"strategy"`
	Phase    string `json:"phase"`
	Message  string `json:"message,omitempty"`
	Paused   *Pause `json:"paused,omitempty"`
	Desired  int64  `json:"desired"`
	Updated  int64  `json:"updated"`
	Ready    int64  `json:"ready"`
	// Stable and Current are pod template hashes: equal once the new version
	// has taken over.
	Stable  string `json:"stable,omitempty"`
	Current string `json:"current,omitempty"`
	// Weight is the share of traffic on the new version, -1 when unknown.
	Weight int `json:"weight"`
}

// ViewRollouts reads the Rollouts of an Application; none for an ordinary
// one.
func ViewRollouts(ctx context.Context, hub *catalog.Hub, upstream, app string) ([]RolloutView, error) {
	c, err := hub.Named(ctx, upstream)
	if err != nil {
		return nil, err
	}
	a, err := c.ArgoCD.GetApplication(ctx, app)
	if err != nil {
		return nil, err
	}
	out := []RolloutView{}
	for _, r := range appWorkloads(a) {
		if r.Kind != rolloutKind {
			continue
		}
		obj, err := c.ArgoCD.Resource(ctx, app, r.Group, r.Version, r.Kind, r.Namespace, r.Name)
		if err != nil {
			return nil, err
		}
		v := RolloutView{Name: r.Name, Strategy: "canary", Phase: str(obj, "status", "phase"), Message: str(obj, "status", "message"),
			Desired: num(obj, "spec", "replicas"), Updated: num(obj, "status", "updatedReplicas"), Ready: num(obj, "status", "readyReplicas"),
			Stable: str(obj, "status", "stableRS"), Current: str(obj, "status", "currentPodHash"), Weight: -1}
		if _, bg := lookup(obj, "spec", "strategy", "blueGreen"); bg {
			v.Strategy = "blueGreen"
		}
		v.Paused = pauseOf(obj)
		switch {
		case v.Paused != nil:
			v.Weight = v.Paused.Weight
		case v.Stable != "" && v.Stable == v.Current:
			v.Weight = 100
		}
		// Once the new version is the stable one the canary weight drops back
		// to zero; it is the whole of the traffic by then, not none of it.
		if w, ok := lookup(obj, "status", "canary", "weights", "canary", "weight"); ok && v.Strategy == "canary" && v.Stable != v.Current {
			v.Weight = int(toInt(w))
		}
		out = append(out, v)
	}
	return out, nil
}

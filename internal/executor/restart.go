package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tide/internal/catalog"
	"tide/internal/release"
)

const restartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

// Restart rolls an Application's workloads through Argo CD's "restart"
// resource action, keeping the running version.
type Restart struct {
	Hub *catalog.Hub
}

func (Restart) Kind() string { return release.KindRestart }

func restartPayload(it *release.Item) (*release.RestartPayload, error) {
	var p release.RestartPayload
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (x Restart) clients(ctx context.Context, it *release.Item) (*release.RestartPayload, *catalog.Clients, error) {
	p, err := restartPayload(it)
	if err != nil {
		return nil, nil, err
	}
	c, err := x.Hub.Named(ctx, p.Upstream)
	if err != nil {
		return nil, nil, err
	}
	return p, c, nil
}

// Validate: every workload still exists, and the version is still the one the
// person confirmed. A restart after someone else's deploy would roll a
// version nobody looked at.
func (x Restart) Validate(ctx context.Context, it *release.Item) error {
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
			return fmt.Errorf("stage %s has promotion %s running; restart after it finishes", p.Stage, stage.Status.CurrentPromotion.Name)
		}
	}
	a := c.ArgoCD
	for _, w := range p.Workloads {
		obj, err := a.Resource(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", w.Kind, w.Name, err)
		}
		if !runsArtifact(obj, p.Current) {
			return fmt.Errorf("%s/%s no longer runs %s; create a new release", w.Kind, w.Name, labelOf(p.Current))
		}
	}
	return nil
}

func labelOf(a release.Artifact) string {
	if a.Version != "" {
		return a.Version
	}
	if a.Tag != "" {
		return a.Tag
	}
	return a.Digest
}

// runsArtifact reports whether a container image in the workload is the
// recorded artifact, by digest or by tag. Unknown artifacts (Applications not
// managed by Kargo) always match.
func runsArtifact(obj map[string]any, a release.Artifact) bool {
	if a.Digest == "" && a.Tag == "" {
		return true
	}
	for _, img := range containerImages(obj) {
		if a.Digest != "" && strings.HasSuffix(img, "@"+a.Digest) {
			return true
		}
		if a.Tag != "" && (strings.HasSuffix(img, ":"+a.Tag) || strings.Contains(img, ":"+a.Tag+"@")) {
			return true
		}
	}
	return false
}

func (x Restart) Execute(ctx context.Context, it *release.Item) (string, error) {
	p, c, err := x.clients(ctx, it)
	if err != nil {
		return "", err
	}
	a := c.ArgoCD
	started := time.Now().UTC()
	for i, w := range p.Workloads {
		if err := a.RunResourceAction(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name, "restart"); err != nil {
			if i > 0 {
				return "", fmt.Errorf("restarted %d of %d workloads, then %s/%s failed: %w", i, len(p.Workloads), w.Kind, w.Name, err)
			}
			return "", fmt.Errorf("%s/%s: %w", w.Kind, w.Name, err)
		}
	}
	return "restart@" + started.Format(time.RFC3339), nil
}

// Poll waits until every workload has rolled out its restarted template.
func (x Restart) Poll(ctx context.Context, it *release.Item) (ItemStatus, error) {
	p, c, err := x.clients(ctx, it)
	if err != nil {
		return ItemStatus{}, err
	}
	a := c.ArgoCD
	var waiting []string
	for _, w := range p.Workloads {
		obj, err := a.Resource(ctx, p.App, w.Group, w.Version, w.Kind, w.Namespace, w.Name)
		if err != nil {
			return ItemStatus{}, fmt.Errorf("%s/%s: %w", w.Kind, w.Name, err)
		}
		if failed, why := rolloutFailed(w.Kind, obj); failed {
			return ItemStatus{Done: true, Error: fmt.Sprintf("%s/%s: %s", w.Kind, w.Name, why)}, nil
		}
		if done, why := rolledOut(w.Kind, obj); !done {
			waiting = append(waiting, fmt.Sprintf("%s/%s: %s", w.Kind, w.Name, why))
		}
	}
	if len(waiting) > 0 {
		return ItemStatus{Waiting: strings.Join(waiting, "; ")}, nil
	}
	return ItemStatus{Done: true, Success: true}, nil
}

// rolledOut is rolloutComplete that additionally requires the restart
// annotation to be present.
func rolledOut(kind string, obj map[string]any) (bool, string) {
	if restartedAt(obj) == "" {
		return false, "restart annotation not applied yet"
	}
	return rolloutComplete(kind, obj)
}

// rolloutComplete mirrors `kubectl rollout status` for the three workload
// kinds: the new generation is observed, every replica is updated and ready,
// and no old replica is left.
func rolloutComplete(kind string, obj map[string]any) (bool, string) {
	gen, observed := num(obj, "metadata", "generation"), num(obj, "status", "observedGeneration")
	if observed < gen {
		return false, "controller has not observed the new generation"
	}
	switch kind {
	case "Deployment", "StatefulSet":
		want := int64(1)
		if r, ok := lookup(obj, "spec", "replicas"); ok {
			want = toInt(r)
		}
		updated, ready := num(obj, "status", "updatedReplicas"), num(obj, "status", "readyReplicas")
		total := num(obj, "status", "replicas")
		if kind == "StatefulSet" {
			cur, upd := str(obj, "status", "currentRevision"), str(obj, "status", "updateRevision")
			if upd != "" && cur != upd {
				return false, "statefulset revision still rolling"
			}
		}
		if updated < want || ready < want || total > want {
			return false, fmt.Sprintf("%d/%d updated, %d ready", updated, want, ready)
		}
		if kind == "Deployment" && num(obj, "status", "availableReplicas") < want {
			return false, "not all replicas available"
		}
	case "DaemonSet":
		desired := num(obj, "status", "desiredNumberScheduled")
		if num(obj, "status", "updatedNumberScheduled") < desired || num(obj, "status", "numberAvailable") < desired {
			return false, "daemonset still rolling"
		}
	}
	return true, ""
}

// rolloutFailed reports a Deployment whose rollout Kubernetes gave up on:
// the new pods did not become available within progressDeadlineSeconds
// (crash loop, image pull failure, failing readiness probe...).
func rolloutFailed(kind string, obj map[string]any) (bool, string) {
	if kind != "Deployment" {
		return false, ""
	}
	v, _ := lookup(obj, "status", "conditions")
	conds, _ := v.([]any)
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Progressing" && m["status"] == "False" && m["reason"] == "ProgressDeadlineExceeded" {
			msg, _ := m["message"].(string)
			return true, "rollout failed (ProgressDeadlineExceeded): " + msg
		}
	}
	return false, ""
}

func restartedAt(obj map[string]any) string {
	return str(obj, "spec", "template", "metadata", "annotations", restartedAtAnnotation)
}

func containerImages(obj map[string]any) []string {
	var out []string
	v, _ := lookup(obj, "spec", "template", "spec")
	spec, _ := v.(map[string]any)
	for _, key := range []string{"initContainers", "containers"} {
		list, _ := spec[key].([]any)
		for _, c := range list {
			if m, ok := c.(map[string]any); ok {
				if img, ok := m["image"].(string); ok {
					out = append(out, img)
				}
			}
		}
	}
	return out
}

func lookup(obj map[string]any, path ...string) (any, bool) {
	var cur any = obj
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func num(obj map[string]any, path ...string) int64 {
	v, _ := lookup(obj, path...)
	return toInt(v)
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

func str(obj map[string]any, path ...string) string {
	v, _ := lookup(obj, path...)
	s, _ := v.(string)
	return s
}

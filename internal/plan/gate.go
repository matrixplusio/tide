package plan

import (
	"context"
	"time"

	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/release"
)

// Gate is a cross-site verification requirement: an image may go to this
// environment only if the same digest was verified in Source's Kargo stage.
// Kargo cannot express it (the stages live in different Kargo instances), so
// Tide enforces it, failing closed when the source cannot be read.
type Gate struct {
	Env      string              `json:"env"`
	Upstream string              `json:"upstream,omitempty"`
	Project  string              `json:"project,omitempty"`
	Stage    string              `json:"stage,omitempty"`
	Source   *catalog.Deployment `json:"-"`
	// Label names the source in candidate marks and messages, e.g. "qa（onprem）".
	Label string `json:"label"`
	// Problem is set when the gate cannot be evaluated; nothing passes then.
	Problem string `json:"problem,omitempty"`
}

// NewGate builds the gate for d from the deployment of the same service in
// the source environment (nil when the service is not deployed there). It
// returns nil when no gate applies: no source configured, or the source
// stage is in the same Kargo project, where Kargo's own upstream stages
// already require verification.
func NewGate(d *catalog.Deployment, sourceEnv string, src *catalog.Deployment) *Gate {
	if sourceEnv == "" {
		return nil
	}
	g := &Gate{Env: sourceEnv, Label: sourceEnv}
	switch {
	case src == nil:
		g.Problem = i18n.T(i18n.Default, "pl.gateNotDeployed", d.Service, sourceEnv)
	case src.KargoProject == "":
		g.Problem = i18n.T(i18n.Default, "pl.gateNotManaged", sourceEnv)
	case src.Upstream == d.Upstream && src.KargoProject == d.KargoProject:
		return nil
	default:
		g.Upstream, g.Project, g.Stage, g.Source = src.Upstream, src.KargoProject, src.KargoStage, src
		if src.Upstream != d.Upstream {
			g.Label = sourceEnv + "（" + src.Upstream + "）"
		}
	}
	return g
}

// verified maps image digest → when it was verified (or, lacking a
// verification, since when it has been running) in the gate's source stage.
func (g *Gate) verified(ctx context.Context, hub *catalog.Hub) (map[string]*time.Time, error) {
	c, err := hub.Named(ctx, g.Upstream)
	if err != nil {
		return nil, err
	}
	list, err := c.Kargo.QueryFreight(ctx, g.Project, "")
	if err != nil {
		return nil, err
	}
	out := map[string]*time.Time{}
	for _, f := range list {
		v, ok := f.Status.VerifiedIn[g.Stage]
		if !ok {
			continue
		}
		at := v.VerifiedAt
		for _, img := range f.Images {
			if img.Digest == "" {
				continue
			}
			if prev, seen := out[img.Digest]; !seen || (at != nil && prev != nil && at.Before(*prev)) {
				out[img.Digest] = at
			}
		}
	}
	return out, nil
}

// apply marks candidates the source never verified as unavailable and adds
// the source verification to the others. An unreadable source blocks all.
func (g *Gate) apply(ctx context.Context, hub *catalog.Hub, cands *Candidates) {
	cands.Gate = g
	cands.UpstreamStages = append(cands.UpstreamStages, g.Label)
	var ok map[string]*time.Time
	if g.Problem == "" {
		var err error
		if ok, err = g.verified(ctx, hub); err != nil {
			g.Problem = i18n.T(i18n.Default, "pl.gateReadFailed", g.Label, err.Error())
		}
	}
	g.mark(cands, ok)
}

// mark applies verification times (digest → when) to the candidates.
func (g *Gate) mark(cands *Candidates, ok map[string]*time.Time) {
	n := 0
	for i := range cands.Items {
		c := &cands.Items[i]
		at, verified := ok[c.Digest]
		if !verified {
			c.Available = false
			continue
		}
		c.VerifiedIn = append(c.VerifiedIn, StageMark{Stage: g.Label, Since: at})
		if c.Available {
			n++
		}
	}
	cands.AvailableCount = n
}

// Verification records, on a release item, which source verified the image.
func (g *Gate) Verification(digest string, at *time.Time) *release.SourceVerification {
	return &release.SourceVerification{Env: g.Env, Upstream: g.Upstream, Project: g.Project, Stage: g.Stage, Digest: digest, VerifiedAt: at}
}

// StillVerified re-checks a recorded verification before execution.
func StillVerified(ctx context.Context, hub *catalog.Hub, v *release.SourceVerification) (bool, error) {
	g := &Gate{Upstream: v.Upstream, Project: v.Project, Stage: v.Stage}
	ok, err := g.verified(ctx, hub)
	if err != nil {
		return false, err
	}
	_, found := ok[v.Digest]
	return found, nil
}

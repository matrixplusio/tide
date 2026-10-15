package executor

import (
	"context"
	"fmt"

	"tide/internal/catalog"
	"tide/internal/release"
	"tide/internal/upstream/kargo"
)

// After an aborted rollout traffic is back on the version before, but git
// still names the new one, and so does the Kargo Stage — which is what Tide
// shows as the current version and builds the next release from. Left that
// way, the next sync, by hand or automated, starts the aborted version again.
// So the executor promotes the previous Freight back, which rewrites git, and
// follows it until the workloads run that version. The item still fails: the
// change it was asked to make did not happen.

// revert starts putting the previous version back. why is the abort as the
// rollout reported it.
func (x Image) revert(ctx context.Context, c *catalog.Clients, p *release.ImagePayload, why string) (ItemStatus, error) {
	inLine := fmt.Sprintf("git and Kargo still name %s; release the running version to bring them in line", labelOf(p.To))
	if p.From == nil {
		return ItemStatus{Done: true, Error: why + "; this was the first deploy, so there is no earlier version to put back. " + inLine}, nil
	}
	stage, err := c.Kargo.GetStage(ctx, p.Project, p.Stage)
	if err != nil {
		return ItemStatus{}, err
	}
	// Only undo what this release did. If the Stage has moved on, someone
	// promoted something since, and putting this release's previous version
	// back would undo their change instead.
	if cur := stage.Current(); cur == nil || cur.Name != p.Freight {
		return ItemStatus{Done: true, Error: why + "; Kargo has moved on to another version since, so nothing was put back"}, nil
	}
	if stage.Status.CurrentPromotion != nil {
		return ItemStatus{Waiting: "rollout aborted; waiting for promotion " + stage.Status.CurrentPromotion.Name + " to finish before putting back " + labelOf(*p.From)}, nil
	}
	avail, err := c.Kargo.QueryFreight(ctx, p.Project, p.Stage)
	if err != nil {
		return ItemStatus{}, err
	}
	freight := previousFreight(avail, p)
	if freight == "" {
		return ItemStatus{Done: true, Error: fmt.Sprintf("%s; Kargo no longer has the Freight of %s, so it could not be put back. %s", why, labelOf(*p.From), inLine)}, nil
	}
	promo, err := c.Kargo.PromoteToStage(ctx, p.Project, p.Stage, freight)
	if err != nil {
		return ItemStatus{}, fmt.Errorf("put back %s: %w", labelOf(*p.From), err)
	}
	if promo == nil || promo.Metadata.Name == "" {
		return ItemStatus{}, fmt.Errorf("put back %s: kargo returned no promotion name", labelOf(*p.From))
	}
	return ItemStatus{Waiting: "rollout aborted; putting back " + labelOf(*p.From), RevertRef: promo.Metadata.Name}, nil
}

// previousFreight finds the Freight carrying the version the release replaced:
// by digest, which is what the release recorded as running; by tag only when
// it recorded no digest.
func previousFreight(avail []kargo.Freight, p *release.ImagePayload) string {
	for _, f := range avail {
		for _, img := range f.Images {
			if p.Image != "" && img.RepoURL != p.Image && len(f.Images) > 1 {
				continue
			}
			if p.From.Digest != "" && img.Digest == p.From.Digest ||
				p.From.Digest == "" && p.From.Tag != "" && img.Tag == p.From.Tag {
				return f.Metadata.Name
			}
		}
	}
	return ""
}

// pollRevert follows the promotion that puts the previous version back, then
// the workloads until they run it.
func (x Image) pollRevert(ctx context.Context, c *catalog.Clients, p *release.ImagePayload, it *release.Item) (ItemStatus, error) {
	from := labelOf(*p.From)
	promo, err := c.Kargo.GetPromotion(ctx, p.Project, it.RevertRef)
	if err != nil {
		return ItemStatus{}, err
	}
	if !promo.Terminal() {
		return ItemStatus{Waiting: "rollout aborted; putting back " + from}, nil
	}
	if promo.Status.Phase != "Succeeded" {
		return ItemStatus{Done: true, Error: fmt.Sprintf("rollout aborted; putting back %s failed (%s: %s). Traffic is on %s, but git and Kargo still name %s; release %s to bring them in line",
			from, promo.Status.Phase, promo.Status.Message, from, labelOf(p.To), from)}, nil
	}
	back := *p
	back.To = *p.From
	since := promo.Status.FinishedAt
	if since == nil {
		since = it.ClockFrom
	}
	st, err := x.landed(ctx, c, &back, since)
	switch {
	case err != nil:
		return ItemStatus{}, err
	case st.Done && st.Success:
		return ItemStatus{Done: true, Error: "rollout aborted; put " + from + " back: traffic, git and Kargo are on the previous version"}, nil
	case st.Done:
		return ItemStatus{Done: true, Error: "rollout aborted; putting back " + from + ": " + st.Error}, nil
	}
	return ItemStatus{Waiting: "rollout aborted; putting back " + from + ": " + st.Waiting}, nil
}

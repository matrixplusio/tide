package plan

import (
	"context"
	"strconv"
	"strings"

	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/settings"
)

// GateFor builds the cross-site verification gate an environment needs, or
// nil when Kargo links the stages itself.
func GateFor(ctx context.Context, hub *catalog.Hub, envs settings.Environments, d *catalog.Deployment) (*Gate, error) {
	e, ok := envs.Named(d.Env)
	if !ok || e.PromotesFrom == "" {
		return nil, nil
	}
	snap, err := hub.Snapshot(ctx, false)
	if err != nil {
		return nil, err
	}
	var src *catalog.Deployment
	if svc := snap.Find(d.Service); svc != nil {
		src = svc.Envs[e.PromotesFrom]
	}
	return NewGate(d, e.PromotesFrom, src), nil
}

// AttachConfigDrift records git changes Argo CD has not applied yet on an
// upgrade: Kargo syncs the whole Application, so they would go out with the
// image. Unless the creator chose that (withConfig) it is an anomaly, and a
// blocking one where the policy enforces it.
func AttachConfigDrift(ctx context.Context, hub *catalog.Hub, d *catalog.Deployment, ip *release.ImagePayload, withConfig bool) error {
	// The catalog's sync status can lag a fresh commit; ask Argo CD to
	// re-read git before comparing.
	diff, err := DiffConfig(ctx, hub, d, true)
	if err != nil {
		return err
	}
	if len(diff.Changes) == 0 {
		return nil
	}
	ip.ConfigChanges, ip.WithConfig = diff.Changes, withConfig
	if withConfig {
		return nil
	}
	names := make([]string, 0, len(diff.Changes))
	for _, ch := range diff.Changes {
		names = append(names, ch.Kind+"/"+ch.Name)
	}
	count, what := strconv.Itoa(len(diff.Changes)), strings.Join(names, ", ")
	ip.Anomalies = append(ip.Anomalies, release.Anomaly{Code: release.AnomalyConfigDrift,
		Message: at("r.configDrift", len(diff.Changes), what), Args: []string{count, what}})
	return nil
}

// EnforcedAnomalies returns the anomaly messages the release policy turns
// into hard stops for this environment; the rest stay highlights.
func EnforcedAnomalies(loc i18n.Locale, policy settings.ReleasePolicy, env, tier string, anomalies []release.Anomaly) []string {
	var out []string
	for _, a := range anomalies {
		switch {
		case a.Code == "short_soak" && rbac.EnvMatches(policy.SoakEnforced, env, tier),
			a.Code == "multi_version_jump" && rbac.EnvMatches(policy.VersionJumpEnforced, env, tier):
			out = append(out, a.Message)
		case a.Code == release.AnomalyConfigDrift && rbac.EnvMatches(policy.ConfigDriftEnforced, env, tier):
			out = append(out, i18n.T(loc, "r.syncFirstHint", a.Message))
		}
	}
	return out
}

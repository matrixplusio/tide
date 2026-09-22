package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"tide/internal/audit"
	"tide/internal/catalog"
	"tide/internal/i18n"
	"tide/internal/notify"
	"tide/internal/plan"
	"tide/internal/rbac"
	"tide/internal/release"
	"tide/internal/settings"
	"tide/internal/store/pg"
)

// Defaults for how patiently an intake waits for its freight. A warehouse
// discovers a new image on its own schedule, so minutes are normal; giving up
// eventually keeps a mistyped digest from waiting forever.
const (
	DefaultWait = 30 * time.Minute
	DefaultTick = 20 * time.Second
)

// errWaiting means "not yet", not "no": the intake stays open and is tried
// again on the next tick.
var errWaiting = errors.New("freight not available yet")

type Service struct {
	PG       *pg.Store
	Settings *settings.Store
	Hub      *catalog.Hub
	Notifier *notify.Notifier

	// Wait is how long an intake looks for its freight before expiring;
	// Tick is how often the worker looks. Zero means the defaults.
	Wait time.Duration
	Tick time.Duration
}

func (s *Service) wait() time.Duration {
	if s.Wait > 0 {
		return s.Wait
	}
	return DefaultWait
}

func (s *Service) tick() time.Duration {
	if s.Tick > 0 {
		return s.Tick
	}
	return DefaultTick
}

// Request is one notification from a pipeline.
type Request struct {
	Service    string
	Env        string
	Image      string
	Digest     string
	Commit     string
	Pipeline   string
	Actor      string
	JiraTicket string
	Reason     string
	// Key makes a retry the same request; empty falls back to the digest,
	// which is what makes re-running a pipeline safe by default.
	Key string
}

// Accept validates what can be checked at once and records the intake. It
// deliberately does not wait for the freight: the pipeline is holding a
// runner open while this call is in flight, and the freight may be minutes
// away. Everything past this point happens on Tide's own clock.
func (s *Service) Accept(ctx context.Context, token *pg.CIToken, req Request) (*pg.CIIntake, bool, error) {
	envs, err := s.Settings.Environments(ctx)
	if err != nil {
		return nil, false, err
	}
	env, ok := envs.Named(req.Env)
	if !ok {
		return nil, false, fmt.Errorf("%w: %s", ErrEnvUnknown, req.Env)
	}
	if env.CIMode() == settings.CIOff {
		return nil, false, fmt.Errorf("%w: %s", ErrCIDisabled, req.Env)
	}
	// A cached snapshot is enough to catch a mistyped service name, and it
	// keeps this call off the upstreams.
	snap, err := s.Hub.Snapshot(ctx, false)
	if err != nil {
		return nil, false, err
	}
	if deploymentIn(snap, req.Service, req.Env) == nil {
		return nil, false, fmt.Errorf("%w: %s → %s", ErrNotDeployed, req.Service, req.Env)
	}
	key := req.Key
	if key == "" {
		key = req.Digest
	}
	in := pg.CIIntake{
		Key: key, Service: req.Service, Env: req.Env, Image: req.Image, Digest: req.Digest,
		Commit: req.Commit, Pipeline: req.Pipeline, Actor: req.Actor,
		JiraTicket: req.JiraTicket, Reason: req.Reason, TokenID: token.ID,
	}
	got, accepted, err := s.PG.CI.Accept(ctx, in)
	if err != nil {
		return nil, false, err
	}
	if accepted {
		_ = s.PG.Audit.Write(ctx, Actor(token), "ci.intake", req.Service, req.JiraTicket, map[string]any{
			"env": req.Env, "image": req.Image, "digest": req.Digest, "commit": req.Commit,
			"pipeline": req.Pipeline, "ciActor": req.Actor, "key": key,
		})
	}
	return got, accepted, nil
}

// Actor is how a token appears in the audit log: the token, not the person
// who created it and not the pipeline that used it. Both of those are in the
// record's detail; the subject is the credential that was presented.
func Actor(t *pg.CIToken) audit.Actor {
	return audit.Actor{Sub: "ci:" + t.ID, Name: t.Name}
}

// Run turns waiting intakes into releases until ctx is cancelled. Only the
// replica holding the advisory lock does it: two replicas would each build a
// release for the same intake, one would lose the target claim, and the
// intake would be marked failed while the other one's release was running —
// the page would say "failed" about a release that is deploying.
func (s *Service) Run(ctx context.Context) {
	pg.Lead(ctx, s.PG.DB(), pg.LockCIIntake, s.tick(), "ci intake", s.Process)
}

// Process makes one pass over the waiting intakes. Exported for tests and for
// the handler, which runs a pass right after accepting so a freight that is
// already there releases without waiting for the next tick.
func (s *Service) Process(ctx context.Context) {
	s.expire(ctx)
	list, err := s.PG.CI.Waiting(ctx, 50)
	if err != nil {
		zap.L().Warn("ci: listing waiting intakes failed", zap.Error(err))
		return
	}
	for _, in := range list {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.process(ctx, in)
	}
}

func (s *Service) expire(ctx context.Context) {
	list, err := s.PG.CI.Expired(ctx, s.wait())
	if err != nil {
		zap.L().Warn("ci: listing expired intakes failed", zap.Error(err))
		return
	}
	for _, in := range list {
		msg := i18n.T(i18n.Default, "ci.freightNeverArrived", in.Digest, s.wait().String())
		if err := s.PG.CI.Resolve(ctx, in.ID, pg.IntakeExpired, "", msg); err != nil {
			zap.L().Warn("ci: expiring intake failed", zap.Int64("intake_id", in.ID), zap.Error(err))
			continue
		}
		zap.L().Warn("ci: intake expired", zap.Int64("intake_id", in.ID), zap.String("service", in.Service),
			zap.String("env", in.Env), zap.String("digest", in.Digest))
	}
}

func (s *Service) process(ctx context.Context, in pg.CIIntake) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rel, err := s.release(ctx, in)
	switch {
	case errors.Is(err, errWaiting):
		if err := s.PG.CI.Attempt(ctx, in.ID); err != nil {
			zap.L().Warn("ci: recording attempt failed", zap.Int64("intake_id", in.ID), zap.Error(err))
		}
		return
	case err != nil:
		zap.L().Warn("ci: intake failed", zap.Int64("intake_id", in.ID), zap.String("service", in.Service),
			zap.String("env", in.Env), zap.Error(err))
		if err := s.PG.CI.Resolve(ctx, in.ID, pg.IntakeFailed, "", truncate(err.Error(), 500)); err != nil {
			zap.L().Warn("ci: resolving failed intake failed", zap.Int64("intake_id", in.ID), zap.Error(err))
		}
		return
	}
	if err := s.PG.CI.Resolve(ctx, in.ID, pg.IntakeReleased, rel.ID, ""); err != nil {
		zap.L().Warn("ci: resolving released intake failed", zap.Int64("intake_id", in.ID), zap.Error(err))
	}
	zap.L().Info("ci: release created", zap.Int64("intake_id", in.ID), zap.String("release", rel.ID),
		zap.String("status", string(rel.Status)))
}

// release builds and submits the release for one intake, then either leaves
// it for a person to confirm or starts it, as the environment says.
func (s *Service) release(ctx context.Context, in pg.CIIntake) (*release.Release, error) {
	envs, err := s.Settings.Environments(ctx)
	if err != nil {
		return nil, err
	}
	env, ok := envs.Named(in.Env)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrEnvUnknown, in.Env)
	}
	// The mode is read now, not when the intake was accepted: turning CI off
	// for an environment must stop releases that have not happened yet.
	mode := env.CIMode()
	if mode == settings.CIOff {
		return nil, fmt.Errorf("%w: %s", ErrCIDisabled, in.Env)
	}
	policy, err := s.Settings.ReleasePolicy(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkPolicy(policy, env, in); err != nil {
		return nil, err
	}
	// Fresh: the whole point is that something changed upstream a moment ago.
	snap, err := s.Hub.Snapshot(ctx, true)
	if err != nil {
		return nil, err
	}
	d := deploymentIn(snap, in.Service, in.Env)
	if d == nil {
		return nil, fmt.Errorf("%w: %s → %s", ErrNotDeployed, in.Service, in.Env)
	}
	if svc := snap.Find(in.Service); svc != nil && len(svc.Conflicts) > 0 {
		return nil, fmt.Errorf("%s: %s", in.Service, strings.Join(svc.Conflicts, "; "))
	}
	gate, err := plan.GateFor(ctx, s.Hub, envs, d)
	if err != nil {
		return nil, err
	}
	freight, err := s.freightFor(ctx, d, gate, in.Digest)
	if err != nil {
		return nil, err
	}
	ever, err := s.PG.Releases.EverDeployed(ctx, in.Service, in.Env)
	if err != nil {
		return nil, err
	}
	payload, err := plan.Build(ctx, s.Hub, policy, d, gate, freight, ever)
	if err != nil {
		return nil, err
	}
	// Nobody is here to decide whether unsynced git changes should ride
	// along, so they never do: the release is refused where the policy
	// enforces that, and flagged everywhere else.
	if err := plan.AttachConfigDrift(ctx, s.Hub, d, payload, false); err != nil {
		return nil, err
	}
	if blocked := plan.EnforcedAnomalies(i18n.Default, policy, in.Env, env.Tier, payload.Anomalies); len(blocked) > 0 {
		return nil, errors.New(strings.Join(blocked, "; "))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return s.submit(ctx, in, env, policy, release.CreateInput{
		Env: in.Env, JiraTicket: in.JiraTicket, Reason: in.Reason, Source: release.SourceCI,
		Items: []release.ItemInput{{Kind: release.KindImage, Sequence: 1, Payload: body}},
	}, payload)
}

// submit creates the release, claims its target and then applies the
// environment's mode. A failure after creation cancels the draft rather than
// leaving one behind holding a target.
func (s *Service) submit(ctx context.Context, in pg.CIIntake, env settings.Environment,
	policy settings.ReleasePolicy, input release.CreateInput, payload *release.ImagePayload) (*release.Release, error) {
	actor, err := s.actor(ctx, in)
	if err != nil {
		return nil, err
	}
	rel, err := s.PG.Releases.Create(ctx, actor, input)
	if err != nil {
		return nil, err
	}
	rel, err = s.PG.Releases.Submit(ctx, actor, rel.ID)
	if err != nil {
		_, _ = s.PG.Releases.Cancel(context.WithoutCancel(ctx), actor, rel.ID, "submit failed: "+err.Error())
		return nil, err
	}
	s.Hub.Reset()
	if env.CIMode() != settings.CIAuto {
		// Held for a person. They confirm it in Tide, echoing the digest, and
		// the audit records their name against the confirmation.
		s.notify(ctx, rel, "pending")
		return rel, nil
	}
	rule := s.approvalRule(ctx, policy, env, in, payload)
	rel, err = s.PG.Releases.Start(ctx, actor, rel.ID, rule, map[string]any{
		"source": release.SourceCI, "pipeline": in.Pipeline, "ciActor": in.Actor, "commit": in.Commit,
	})
	if err != nil {
		_, _ = s.PG.Releases.Cancel(context.WithoutCancel(ctx), actor, rel.ID, "auto start failed: "+err.Error())
		return nil, err
	}
	if rel.Status == release.Approving {
		s.notify(ctx, rel, "approval_requested")
	}
	return rel, nil
}

func (s *Service) notify(ctx context.Context, rel *release.Release, event string) {
	if s.Notifier != nil {
		s.Notifier.ReleaseEvent(ctx, rel, event)
	}
}

// actor resolves the intake's token into the audit subject. A token revoked
// between the notification and the release stops it: the credential has to be
// good at the moment the change is made, not only when it was announced.
func (s *Service) actor(ctx context.Context, in pg.CIIntake) (audit.Actor, error) {
	list, err := s.PG.CI.ListTokens(ctx)
	if err != nil {
		return audit.Actor{}, err
	}
	for _, t := range list {
		if t.ID != in.TokenID {
			continue
		}
		if t.RevokedAt != nil {
			return audit.Actor{}, pg.ErrCITokenUnknown
		}
		return Actor(&t), nil
	}
	return audit.Actor{}, pg.ErrCITokenUnknown
}

// approvalRule is the rule the environment's policy puts on this release.
// Automatic does not mean unapproved: an environment with an approval rule
// still waits for its approvers, CI or no CI.
func (s *Service) approvalRule(ctx context.Context, policy settings.ReleasePolicy, env settings.Environment,
	in pg.CIIntake, payload *release.ImagePayload) *release.ApprovalRule {
	t := rbac.Target{Env: in.Env, Project: payload.Project}
	if snap, err := s.Hub.Snapshot(ctx, false); err == nil {
		if svc := snap.Find(in.Service); svc != nil {
			t.Project = svc.Project
			if cat, err := s.Settings.Catalog(ctx); err == nil && cat.BatchDimension != "" {
				t.Type = svc.Dimensions[cat.BatchDimension]
			}
		}
	}
	return policy.ApprovalFor(in.Env, env.Tier, t.Project, t.Type)
}

// freightFor finds the freight carrying the digest CI pushed. Not finding it
// is the normal case for a while: the warehouse has not looked yet.
func (s *Service) freightFor(ctx context.Context, d *catalog.Deployment, gate *plan.Gate, digest string) (string, error) {
	cands, err := plan.List(ctx, s.Hub, d, gate, true)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(cands.Items, func(c plan.Candidate) bool { return c.Digest == digest })
	if i < 0 {
		return "", errWaiting
	}
	if !cands.Items[i].Available {
		// The freight exists but this stage may not have it yet: upstream
		// verification, or the cross-site gate, has not passed.
		return "", errWaiting
	}
	return cands.Items[i].Freight, nil
}

// checkPolicy applies the parts of the release policy a pipeline can fall
// foul of. A missing ticket or reason fails the intake with words an operator
// can act on rather than being filled in on the pipeline's behalf: the policy
// asks for a person's justification, and inventing one would defeat it.
func checkPolicy(policy settings.ReleasePolicy, env settings.Environment, in pg.CIIntake) error {
	for _, f := range policy.ActiveFreezes(time.Now()) {
		if rbac.EnvMatches(f.Envs, in.Env, env.Tier) {
			return &release.FrozenError{Env: in.Env, Name: f.Name, Until: f.EndsAt, Reason: f.Reason}
		}
	}
	if in.JiraTicket == "" && rbac.EnvMatches(policy.JiraRequired, in.Env, env.Tier) {
		return errors.New(i18n.T(i18n.Default, "ci.jiraRequired", in.Env))
	}
	if in.JiraTicket != "" {
		if !release.ValidJira(in.JiraTicket) {
			return errors.New(i18n.T(i18n.Default, "ci.jiraFormat", in.JiraTicket))
		}
		if project, _, _ := strings.Cut(in.JiraTicket, "-"); len(policy.JiraProjects) > 0 && !slices.Contains(policy.JiraProjects, project) {
			return errors.New(i18n.T(i18n.Default, "ci.jiraProject", project))
		}
	}
	if in.Reason == "" && rbac.EnvMatches(policy.ReasonRequired, in.Env, env.Tier) {
		return errors.New(i18n.T(i18n.Default, "ci.reasonRequired", in.Env))
	}
	return nil
}

func deploymentIn(snap *catalog.Snapshot, service, env string) *catalog.Deployment {
	if svc := snap.Find(service); svc != nil {
		return svc.Envs[env]
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

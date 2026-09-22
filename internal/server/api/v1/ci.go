package v1

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"tide/internal/audit"
	"tide/internal/ci"
	"tide/internal/release"
	"tide/internal/server/api/errcode"
	"tide/internal/server/api/respond"
	"tide/internal/store/pg"
	"tide/internal/validate"
)

var (
	reImageRef   = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,252}$`)
	reCommitSHA  = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	reIdemKey    = regexp.MustCompile(`^[A-Za-z0-9._:@/-]{1,200}$`)
	reTokenID    = regexp.MustCompile(`^[0-9a-f-]{36}$`)
	ciActorRe    = regexp.MustCompile(`^[^\x00-\x1f]{0,100}$`)
	intakeStates = []string{pg.IntakeWaiting, pg.IntakeReleased, pg.IntakeFailed, pg.IntakeExpired}
)

type ciReleaseReq struct {
	Service    string `json:"service" binding:"required,max=253" label:"service"`
	Env        string `json:"env" binding:"required,max=32" label:"env"`
	Digest     string `json:"digest" binding:"required" label:"digest"`
	Image      string `json:"image" binding:"max=253" label:"image"`
	Commit     string `json:"commit" binding:"max=64" label:"commit"`
	Pipeline   string `json:"pipeline" binding:"max=500" label:"pipeline"`
	Actor      string `json:"actor" binding:"max=100" label:"ciActor"`
	JiraTicket string `json:"jiraTicket" binding:"max=64" label:"jira"`
	Reason     string `json:"reason" binding:"max=2000" label:"reason"`
}

func (r *ciReleaseReq) Normalize() {
	trim(&r.Service, &r.Env, &r.Digest, &r.Image, &r.Commit, &r.Pipeline, &r.Actor, &r.JiraTicket, &r.Reason)
	r.JiraTicket = strings.ToUpper(r.JiraTicket)
	// A pipeline usually has the repository and tag to hand but not always
	// the digest on its own; "repo@sha256:..." is accepted and split here.
	if _, d, ok := strings.Cut(r.Digest, "@"); ok {
		r.Digest = d
	}
	r.Digest = strings.ToLower(r.Digest)
}

func (r *ciReleaseReq) Check() error {
	errs := []*validate.FieldError{}
	if !reDNSName.MatchString(r.Service) {
		errs = append(errs, validate.FieldKey("service", "r.serviceFormat"))
	}
	if !reEnv.MatchString(r.Env) {
		errs = append(errs, validate.FieldKey("env", "r.envFormat"))
	}
	if !reDigest.MatchString(r.Digest) {
		errs = append(errs, validate.FieldKey("digest", "r.digestFormat"))
	}
	if r.Image != "" && !reImageRef.MatchString(r.Image) {
		errs = append(errs, validate.FieldKey("image", "ci.imageFormat"))
	}
	if r.Commit != "" && !reCommitSHA.MatchString(r.Commit) {
		errs = append(errs, validate.FieldKey("commit", "ci.commitFormat"))
	}
	errs = append(errs, validate.HTTPURL("pipeline", r.Pipeline, false, validate.AnyURL))
	if !ciActorRe.MatchString(r.Actor) {
		errs = append(errs, validate.FieldKey("actor", "ci.actorFormat"))
	}
	if r.JiraTicket != "" && !release.ValidJira(r.JiraTicket) {
		errs = append(errs, validate.FieldKey("jiraTicket", "r.jiraFormat"))
	}
	return validate.Collect(errs...)
}

// ciToken authenticates a build pipeline. It is deliberately separate from
// the session middleware: a token is not a person, may not browse anything,
// and can only reach the endpoints registered behind this.
func (a *API) ciToken(c *gin.Context) {
	plain := ci.BearerToken(c.GetHeader("Authorization"))
	if plain == "" {
		respond.FailCode(c, errcode.Unauthorized, "")
		c.Abort()
		return
	}
	token, err := a.PG.CI.TokenByHash(c.Request.Context(), ci.HashToken(plain))
	if err != nil {
		if !errors.Is(err, pg.ErrCITokenUnknown) {
			respond.Fail(c, err)
			c.Abort()
			return
		}
		// Unknown and revoked are the same answer: a caller learns only that
		// this token does not work.
		zap.L().Warn("ci: token rejected", zap.String("request_id", c.GetString("request_id")),
			zap.String("client_ip", c.ClientIP()))
		respond.FailCode(c, errcode.CITokenInvalid, "")
		c.Abort()
		return
	}
	if err := a.PG.CI.TouchToken(c.Request.Context(), token.ID); err != nil {
		// Recording the use is bookkeeping; it must not refuse a good token.
		zap.L().Warn("ci: touching token failed", zap.String("token_id", token.ID), zap.Error(err))
	}
	c.Set(ctxCIToken, token)
	c.Set("user_id", "ci:"+token.ID)
	c.Next()
}

func ciTokenOf(c *gin.Context) *pg.CIToken {
	v, _ := c.Get(ctxCIToken)
	t, _ := v.(*pg.CIToken)
	return t
}

// ciRelease is what a pipeline calls after pushing an image. It records the
// notification and returns; it does not wait for the release, because the
// freight may be minutes away and the pipeline is holding a runner open.
func (a *API) ciRelease(c *gin.Context) {
	var req ciReleaseReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key != "" && !reIdemKey.MatchString(key) {
		respond.Fail(c, errcode.Invalid(errcode.FieldError{Field: "Idempotency-Key", Key: "ci.keyFormat"}))
		return
	}
	token := ciTokenOf(c)
	intake, accepted, err := a.CI.Accept(c.Request.Context(), token, ci.Request{
		Service: req.Service, Env: req.Env, Image: req.Image, Digest: req.Digest, Commit: req.Commit,
		Pipeline: req.Pipeline, Actor: req.Actor, JiraTicket: req.JiraTicket, Reason: req.Reason, Key: key,
	})
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if accepted {
		// The freight is often already there when a warehouse watches the
		// registry; try once now so the common case does not wait a tick.
		go a.CI.Process(withoutRequest(c))
	}
	respond.OK(c, ciIntakeView{CIIntake: intake, Accepted: accepted})
}

// ciIntakeView adds whether this call created the intake; a retried pipeline
// gets Accepted=false and the original intake, which is what makes re-running
// a job safe.
type ciIntakeView struct {
	*pg.CIIntake
	Accepted bool `json:"accepted"`
}

func (a *API) listCIIntakes(c *gin.Context) {
	p := newParams(c)
	status := p.enum("status", intakeStates)
	page, size := p.page()
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	items, total, err := a.PG.CI.ListIntakes(c.Request.Context(), status, page, size)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.Page(c, items, total, page, size)
}

func (a *API) listCITokens(c *gin.Context) {
	list, err := a.PG.CI.ListTokens(c.Request.Context())
	if err != nil {
		respond.Fail(c, err)
		return
	}
	respond.OK(c, gin.H{"items": list})
}

type createCITokenReq struct {
	Name string `json:"name" binding:"required,max=64" label:"name"`
}

func (r *createCITokenReq) Normalize() { trim(&r.Name) }

// createCIToken mints a token and returns it once. Tide stores only the hash,
// so there is no second chance to read it: a lost token is replaced.
func (a *API) createCIToken(c *gin.Context) {
	var req createCITokenReq
	if err := bindJSON(c, &req); err != nil {
		respond.Fail(c, err)
		return
	}
	plain, hash, prefix, err := ci.NewToken()
	if err != nil {
		respond.Fail(c, err)
		return
	}
	u := currentUser(c)
	token := pg.CIToken{ID: uuid.NewString(), Name: req.Name, Prefix: prefix, CreatedBy: u.Sub, CreatedByName: u.Name}
	if err := a.PG.CI.CreateToken(c.Request.Context(), token, hash); err != nil {
		respond.Fail(c, err)
		return
	}
	_ = a.PG.Audit.Write(c.Request.Context(), u.Actor(), "ci.token.create", token.ID, "", map[string]any{
		"name": token.Name, "prefix": token.Prefix,
	})
	token.CreatedAt = time.Now()
	respond.OK(c, gin.H{"token": token, "secret": plain})
}

func (a *API) revokeCIToken(c *gin.Context) {
	p := newParams(c)
	id := p.path("token", reTokenID, "label.tokenId")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	if err := a.PG.CI.RevokeToken(c.Request.Context(), id); err != nil {
		respond.Fail(c, err)
		return
	}
	_ = a.PG.Audit.Write(c.Request.Context(), currentUser(c).Actor(), "ci.token.revoke", id, "", nil)
	respond.OK(c, nil)
}

// ciSnippet renders the pipeline step for an environment, so nobody has to
// assemble the call by hand and the field names stay in one place. The token
// is never rendered into it: the snippet reads it from a CI variable.
func (a *API) ciSnippet(c *gin.Context) {
	p := newParams(c)
	env := p.match("env", reEnv, "label.envName")
	if err := p.err(); err != nil {
		respond.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	sys, err := a.Settings.System(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	envs, err := a.Settings.Environments(ctx)
	if err != nil {
		respond.Fail(c, err)
		return
	}
	if env == "" && len(envs.Items) > 0 {
		env = envs.Items[0].Name
	}
	e, ok := envs.Named(env)
	if !ok {
		respond.Fail(c, errcode.NewKey(errcode.NotFound, "s.envNotFound", env))
		return
	}
	base := strings.TrimSuffix(sys.BaseURL, "/")
	if base == "" {
		base = "https://" + c.Request.Host
	}
	respond.OK(c, gin.H{
		"env":      e.Name,
		"mode":     e.CIMode(),
		"url":      base + apiPrefix + "/ci/releases",
		"variable": "TIDE_WEBHOOK_URL",
		"snippet":  ciSnippet(base+apiPrefix+"/ci/releases", e.Name),
	})
}

// ciSnippet is the GitLab job step. It is deliberately non-blocking and
// deliberately cannot fail the job: the image is already pushed, and losing a
// notification is a smaller problem than marking a good build red. Tide is
// idempotent on the digest, so a re-run does not release twice.
func ciSnippet(url, env string) string {
	return `# Tell Tide the image is ready. Needs TIDE_WEBHOOK_URL and TIDE_TOKEN
# (masked, not protected) as group or project CI/CD variables.
notify-tide:
  stage: deploy
  script:
    - |
      if [ -z "${TIDE_WEBHOOK_URL:-}" ]; then echo "TIDE_WEBHOOK_URL is not set, skipping"; exit 0; fi
      curl -sS -m 10 -X POST "$TIDE_WEBHOOK_URL" \
        -H "Authorization: Bearer $TIDE_TOKEN" \
        -H "Content-Type: application/json" \
        -H "Idempotency-Key: $IMAGE_DIGEST" \
        -d "{\"service\":\"$APP_NAME\",\"env\":\"` + env + `\",\"image\":\"${IMAGE_REPO}:${IMAGE_TAG}\",\"digest\":\"$IMAGE_DIGEST\",\"commit\":\"$CI_COMMIT_SHA\",\"pipeline\":\"$CI_PIPELINE_URL\",\"actor\":\"$GITLAB_USER_LOGIN\",\"reason\":\"$CI_COMMIT_TITLE\"}" \
        || echo "Could not reach Tide; the image is pushed and the release can be started there by hand"
  variables:
    TIDE_WEBHOOK_URL: "` + url + `"
`
}

// withoutRequest detaches the background pass from the request that started
// it: the client's connection closing must not cancel a release in progress.
func withoutRequest(c *gin.Context) context.Context {
	ctx := context.WithoutCancel(c.Request.Context())
	return audit.WithMeta(ctx, audit.Meta{RequestID: c.GetString("request_id"), ClientIP: c.ClientIP()})
}

package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrCITokenUnknown  = errors.New("ci token unknown or revoked")
	ErrCIIntakeUnknown = errors.New("ci intake not found")
)

// CIToken is a credential a build pipeline presents. The token itself is not
// stored, only its hash, so this struct never carries a secret.
type CIToken struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"prefix"`
	CreatedBy     string     `json:"createdBy"`
	CreatedByName string     `json:"createdByName"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
}

// Intake states.
const (
	IntakeWaiting  = "waiting"
	IntakeReleased = "released"
	IntakeFailed   = "failed"
	IntakeExpired  = "expired"
	// IntakeBuildFailed is the build itself failing, which is not
	// IntakeFailed: that one means Tide had an image and could not release
	// it. Nothing was ever built here, so nothing is waited for.
	IntakeBuildFailed = "build_failed"
	// IntakeRejected is a report Tide refused before taking it in: an
	// unknown environment, one that takes no CI releases, a service not
	// deployed there. Kept so the refusal can be seen; never worked on.
	IntakeRejected = "rejected"
)

// CIIntake is one notification from a pipeline, kept until Tide can turn it
// into a release (or gives up). It outlives the request because the freight
// the release needs may not exist when the pipeline finishes pushing.
type CIIntake struct {
	ID       int64  `json:"id"`
	Key      string `json:"key"`
	Service  string `json:"service"`
	Env      string `json:"env"`
	Image    string `json:"image"`
	Digest   string `json:"digest"`
	Commit   string `json:"commit,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Pipeline string `json:"pipeline,omitempty"`
	// Repo is the source repository the image was built from, when the
	// pipeline said; see migration 0019.
	Repo string `json:"repo,omitempty"`
	// Commits is the history the pipeline sent with the build, newest first;
	// see migration 0020.
	Commits    []CICommit `json:"commits,omitempty"`
	Actor      string     `json:"actor,omitempty"`
	JiraTicket string     `json:"jiraTicket,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	TokenID    string     `json:"tokenId"`
	Status     string     `json:"status"`
	ReleaseID  string     `json:"releaseId,omitempty"`
	Error      string     `json:"error,omitempty"`
	Warning    string     `json:"warning,omitempty"`
	Attempts   int        `json:"attempts"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// CI stores the tokens build pipelines authenticate with and the notifications
// they send. Both are ordinary rows: unlike the audit log they are edited
// (a token is revoked, an intake is resolved).
type CI struct{ db *gorm.DB }

// Column list, not a credential: the hash column is deliberately absent,
// so nothing that reads a token row can accidentally return it.
const ciTokenCols = `id, name, prefix, created_by, created_by_name, created_at, last_used_at, revoked_at` //nolint:gosec // G101: a column list

// CreateToken stores a token by its hash. The caller keeps the plaintext; it
// is never written down, so a lost token is replaced rather than recovered.
func (r *CI) CreateToken(ctx context.Context, t CIToken, hash string) error {
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO ci_tokens (id, name, hash, prefix, created_by, created_by_name)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.Name, hash, t.Prefix, t.CreatedBy, t.CreatedByName).Error
}

func (r *CI) ListTokens(ctx context.Context) ([]CIToken, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT ` + ciTokenCols + ` FROM ci_tokens ORDER BY created_at DESC`).Rows()
	if err != nil {
		return nil, err
	}
	return scanCITokens(rows)
}

// TokenByHash resolves a presented token. A revoked token resolves to
// ErrTokenUnknown, exactly like one that never existed: a caller learns only
// that it does not work.
func (r *CI) TokenByHash(ctx context.Context, hash string) (*CIToken, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+ciTokenCols+` FROM ci_tokens WHERE hash = $1 AND revoked_at IS NULL`, hash).Rows()
	if err != nil {
		return nil, err
	}
	list, err := scanCITokens(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrCITokenUnknown
	}
	return &list[0], nil
}

// TouchToken records that a token was used. Best effort: a failure here must
// not refuse a request that was authenticated.
func (r *CI) TouchToken(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Exec(`UPDATE ci_tokens SET last_used_at = now() WHERE id = $1`, id).Error
}

func (r *CI) RevokeToken(ctx context.Context, id string) error {
	res := r.db.WithContext(ctx).Exec(`UPDATE ci_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCITokenUnknown
	}
	return nil
}

const ciIntakeCols = `id, idempotency_key, service, env, image, digest, commit_sha, stage, pipeline, repo, ci_actor,
	jira_ticket, reason, token_id, status, COALESCE(release_id, ''), error, warning, attempts, created_at, updated_at, commits`

// CICommit is one entry of the history a pipeline sends with a build.
type CICommit struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Author string    `json:"author,omitempty"`
	At     time.Time `json:"at"`
}

// CIBuild is what CI said about an image when it handed it over: the commit
// it was built from, its title, who pushed, and where the pipeline is. One
// per digest — the latest intake that carried it, whatever environment it
// was aimed at, because the image is the same wherever it went.
type CIBuild struct {
	Digest    string     `json:"digest"`
	Commit    string     `json:"commit,omitempty"`
	Pipeline  string     `json:"pipeline,omitempty"`
	Repo      string     `json:"repo,omitempty"`
	Actor     string     `json:"actor,omitempty"`
	Title     string     `json:"title,omitempty"`
	Commits   []CICommit `json:"commits,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// BuildsByDigest answers "which build is this image" for a set of digests.
// Digests CI never reported are simply absent: an image that came from
// somewhere else has no build to show, and saying nothing is right.
func (r *CI) BuildsByDigest(ctx context.Context, digests []string) (map[string]CIBuild, error) {
	out := map[string]CIBuild{}
	if len(digests) == 0 {
		return out, nil
	}
	rows, err := r.db.WithContext(ctx).Raw(`SELECT DISTINCT ON (digest) digest, commit_sha, pipeline, repo, ci_actor, reason, created_at, commits
		FROM ci_intake WHERE digest = ANY($1) AND digest <> ''
		ORDER BY digest, created_at DESC`, digests).Rows()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b CIBuild
		var commits []byte
		if err := rows.Scan(&b.Digest, &b.Commit, &b.Pipeline, &b.Repo, &b.Actor, &b.Title, &b.CreatedAt, &commits); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(commits, &b.Commits)
		out[b.Digest] = b
	}
	return out, rows.Err()
}

// Accept records one notification from a pipeline. The idempotency key is
// unique in the database, so a retried pipeline returns the first intake
// instead of releasing twice; accepted reports whether this call created it.
// in.Status chooses the state it is recorded in: empty means a build that
// succeeded and is now waiting for its freight, IntakeBuildFailed means one
// that never produced anything and is already over. A key seen before is
// not always the same answer again; see again.
func (r *CI) Accept(ctx context.Context, in CIIntake) (out *CIIntake, accepted bool, err error) {
	in.Key = ScopedKey(in.Service, in.Env, in.Key)
	status := in.Status
	if status == "" {
		status = IntakeWaiting
	}
	res := r.db.WithContext(ctx).Exec(`
		INSERT INTO ci_intake (idempotency_key, service, env, image, digest, commit_sha, stage, pipeline, ci_actor,
			jira_ticket, reason, token_id, status, error, warning, repo, commits)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		in.Key, in.Service, in.Env, in.Image, in.Digest, in.Commit, in.Stage, in.Pipeline, in.Actor,
		in.JiraTicket, in.Reason, in.TokenID, status, in.Error, in.Warning, in.Repo, commitsJSON(in.Commits))
	if res.Error != nil {
		return nil, false, res.Error
	}
	accepted = res.RowsAffected == 1
	if !accepted && status == IntakeWaiting {
		if accepted, err = r.again(ctx, in); err != nil {
			return nil, false, err
		}
	}
	got, err := r.IntakeByKey(ctx, in.Key)
	if err != nil {
		return nil, false, err
	}
	return got, accepted, nil
}

// ScopedKey is the idempotency key as stored: the pipeline's key within one
// service and environment. The pipeline's key is the digest, and the same
// image is reported to dev and then to qa (one commit, a build cache, a
// byte-for-byte equal image): keyed on the digest alone, the qa report came
// back as dev's intake with 200 and was never released. Neither name can
// contain "/", so the three parts cannot run into each other. Migration
// 0023 brought the rows written before this into the same form.
func ScopedKey(service, env, key string) string {
	if strings.HasPrefix(key, service+"/"+env+"/") {
		return key
	}
	return service + "/" + env + "/" + key
}

// again is a successful build reported under a key Tide has seen before —
// which, the key being the digest, is the same image built again. Source
// that did not change builds byte for byte the same image, so this is how a
// pipeline re-run arrives.
//
// An intake that failed or expired starts over as if new: whatever stopped
// it (a stage not created yet, a warehouse watching the wrong repository,
// Kargo not answering) may be fixed now, and answering with the old outcome
// forever would leave that image unreleasable while the pipeline shows green. created_at and attempts are reset because the
// worker's timeout and back-off count from them. Any other state is left
// alone — that is what the key is for — except that a history the first
// report did not carry is kept, since it describes the same image.
func (r *CI) again(ctx context.Context, in CIIntake) (bool, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE ci_intake SET status = 'waiting', release_id = NULL, error = '', attempts = 0,
			created_at = now(), updated_at = now(), image = $2, commit_sha = $3, stage = $4, pipeline = $5,
			ci_actor = $6, jira_ticket = $7, reason = $8, token_id = $9, warning = $10, repo = $11, commits = $12
		WHERE idempotency_key = $1 AND status IN ('failed', 'expired')`,
		in.Key, in.Image, in.Commit, in.Stage, in.Pipeline, in.Actor, in.JiraTicket, in.Reason, in.TokenID,
		in.Warning, in.Repo, commitsJSON(in.Commits))
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		return true, nil
	}
	if len(in.Commits) == 0 {
		return false, nil
	}
	return false, r.db.WithContext(ctx).Exec(`
		UPDATE ci_intake SET commits = $2, repo = CASE WHEN repo = '' THEN $3 ELSE repo END, updated_at = now()
		WHERE idempotency_key = $1 AND commits = '[]'::jsonb`,
		in.Key, commitsJSON(in.Commits), in.Repo).Error
}

func (r *CI) IntakeByKey(ctx context.Context, key string) (*CIIntake, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+ciIntakeCols+` FROM ci_intake WHERE idempotency_key = $1`, key).Rows()
	if err != nil {
		return nil, err
	}
	list, err := scanCIIntakes(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrCIIntakeUnknown
	}
	return &list[0], nil
}

// Waiting returns intakes still looking for their freight, oldest first.
func (r *CI) Waiting(ctx context.Context, limit int) ([]CIIntake, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+ciIntakeCols+`
		FROM ci_intake WHERE status = 'waiting' ORDER BY created_at LIMIT $1`, limit).Rows()
	if err != nil {
		return nil, err
	}
	return scanCIIntakes(rows)
}

// ListIntakes returns one page of intakes, newest first, and the total.
// IntakeFilter narrows ListIntakes; empty fields match everything. Service
// matches any part of the name, because what somebody has in hand is usually
// the name the pipeline used, and the question is often which name Tide
// knows it by.
type IntakeFilter struct {
	Status  string
	Service string
	Env     string
}

func (r *CI) ListIntakes(ctx context.Context, f IntakeFilter, page, pageSize int) ([]CIIntake, int64, error) {
	// "?" throughout, because the paging parameters below use it and GORM
	// binds the two styles independently: a "$1" here would leave the first
	// "?" to swallow the status, and LIMIT would be handed a string.
	where, args := "1=1", []any{}
	if f.Status != "" {
		where, args = where+" AND status = ?", append(args, f.Status)
	}
	if f.Service != "" {
		where, args = where+` AND service ILIKE ? ESCAPE '\'`, append(args, likePattern(f.Service))
	}
	if f.Env != "" {
		where, args = where+" AND env = ?", append(args, f.Env)
	}
	var total int64
	if err := r.db.WithContext(ctx).Raw(`SELECT count(*) FROM ci_intake WHERE `+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+ciIntakeCols+` FROM ci_intake WHERE `+where+`
		ORDER BY created_at DESC LIMIT ? OFFSET ?`, append(args, pageSize, (page-1)*pageSize)...).Rows()
	if err != nil {
		return nil, 0, err
	}
	list, err := scanCIIntakes(rows)
	return list, total, err
}

// Attempt records that the worker looked at an intake and did not resolve it.
func (r *CI) Attempt(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Exec(`UPDATE ci_intake SET attempts = attempts + 1, updated_at = now()
		WHERE id = $1 AND status = 'waiting'`, id).Error
}

// Resolve ends an intake. The WHERE clause keeps it a one-way door: whichever
// replica gets there first wins and the others change nothing.
func (r *CI) Resolve(ctx context.Context, id int64, status, releaseID, errText string) error {
	var rel any
	if releaseID != "" {
		rel = releaseID
	}
	return r.db.WithContext(ctx).Exec(`UPDATE ci_intake
		SET status = $2, release_id = $3, error = $4, attempts = attempts + 1, updated_at = now()
		WHERE id = $1 AND status = 'waiting'`, id, status, rel, errText).Error
}

func scanCITokens(rows *sql.Rows) ([]CIToken, error) {
	defer func() { _ = rows.Close() }()
	out := []CIToken{}
	for rows.Next() {
		var t CIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedBy, &t.CreatedByName, &t.CreatedAt,
			&t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanCIIntakes(rows *sql.Rows) ([]CIIntake, error) {
	defer func() { _ = rows.Close() }()
	out := []CIIntake{}
	for rows.Next() {
		var x CIIntake
		var commits []byte
		if err := rows.Scan(&x.ID, &x.Key, &x.Service, &x.Env, &x.Image, &x.Digest, &x.Commit, &x.Stage,
			&x.Pipeline, &x.Repo, &x.Actor, &x.JiraTicket, &x.Reason, &x.TokenID, &x.Status, &x.ReleaseID, &x.Error,
			&x.Warning, &x.Attempts, &x.CreatedAt, &x.UpdatedAt, &commits); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(commits, &x.Commits) // a row written before 0020 holds '[]'
		out = append(out, x)
	}
	return out, rows.Err()
}

// Expired returns waiting intakes older than age: the freight never appeared.
func (r *CI) Expired(ctx context.Context, age time.Duration) ([]CIIntake, error) {
	rows, err := r.db.WithContext(ctx).Raw(`SELECT `+ciIntakeCols+`
		FROM ci_intake WHERE status = 'waiting' AND created_at < now() - make_interval(secs => $1)
		ORDER BY created_at`, age.Seconds()).Rows()
	if err != nil {
		return nil, err
	}
	return scanCIIntakes(rows)
}

// commitsJSON is what goes in the column: always an array, never NULL, so a
// reader can unmarshal every row the same way.
func commitsJSON(list []CICommit) []byte {
	if len(list) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(list)
	if err != nil {
		return []byte("[]")
	}
	return b
}

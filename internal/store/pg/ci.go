package pg

import (
	"context"
	"database/sql"
	"errors"
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
)

// CIIntake is one notification from a pipeline, kept until Tide can turn it
// into a release (or gives up). It outlives the request because the freight
// the release needs may not exist when the pipeline finishes pushing.
type CIIntake struct {
	ID         int64     `json:"id"`
	Key        string    `json:"key"`
	Service    string    `json:"service"`
	Env        string    `json:"env"`
	Image      string    `json:"image"`
	Digest     string    `json:"digest"`
	Commit     string    `json:"commit,omitempty"`
	Stage      string    `json:"stage,omitempty"`
	Pipeline   string    `json:"pipeline,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	JiraTicket string    `json:"jiraTicket,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	TokenID    string    `json:"tokenId"`
	Status     string    `json:"status"`
	ReleaseID  string    `json:"releaseId,omitempty"`
	Error      string    `json:"error,omitempty"`
	Warning    string    `json:"warning,omitempty"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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

const ciIntakeCols = `id, idempotency_key, service, env, image, digest, commit_sha, stage, pipeline, ci_actor,
	jira_ticket, reason, token_id, status, COALESCE(release_id, ''), error, warning, attempts, created_at, updated_at`

// Accept records one notification from a pipeline. The idempotency key is
// unique in the database, so a retried pipeline returns the first intake
// instead of releasing twice; accepted reports whether this call created it.
// in.Status chooses the state it is recorded in: empty means a build that
// succeeded and is now waiting for its freight, IntakeBuildFailed means one
// that never produced anything and is already over.
func (r *CI) Accept(ctx context.Context, in CIIntake) (out *CIIntake, accepted bool, err error) {
	status := in.Status
	if status == "" {
		status = IntakeWaiting
	}
	res := r.db.WithContext(ctx).Exec(`
		INSERT INTO ci_intake (idempotency_key, service, env, image, digest, commit_sha, stage, pipeline, ci_actor,
			jira_ticket, reason, token_id, status, error, warning)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		in.Key, in.Service, in.Env, in.Image, in.Digest, in.Commit, in.Stage, in.Pipeline, in.Actor,
		in.JiraTicket, in.Reason, in.TokenID, status, in.Error, in.Warning)
	if res.Error != nil {
		return nil, false, res.Error
	}
	got, err := r.IntakeByKey(ctx, in.Key)
	if err != nil {
		return nil, false, err
	}
	return got, res.RowsAffected == 1, nil
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
func (r *CI) ListIntakes(ctx context.Context, status string, page, pageSize int) ([]CIIntake, int64, error) {
	// "?" throughout, because the paging parameters below use it and GORM
	// binds the two styles independently: a "$1" here would leave the first
	// "?" to swallow the status, and LIMIT would be handed a string.
	where, args := "1=1", []any{}
	if status != "" {
		where, args = "status = ?", []any{status}
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
		if err := rows.Scan(&x.ID, &x.Key, &x.Service, &x.Env, &x.Image, &x.Digest, &x.Commit, &x.Stage,
			&x.Pipeline, &x.Actor, &x.JiraTicket, &x.Reason, &x.TokenID, &x.Status, &x.ReleaseID, &x.Error,
			&x.Warning, &x.Attempts, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
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

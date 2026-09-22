package pg

import (
	"context"
	"time"
)

// Brute-force counters are derived from the audit log so every replica sees
// the same numbers; there is no separate mutable counter to reset or forge.

// LoginFailuresForUser counts failed local sign-ins for sub within window,
// ignoring failures before the account's last successful sign-in.
func (a *Accounts) LoginFailuresForUser(ctx context.Context, sub string, window time.Duration) (int64, error) {
	var n int64
	err := a.db.WithContext(ctx).Raw(`
		SELECT count(*) FROM audit_log
		WHERE action = 'auth.login.failed' AND target = $1
		  AND at > now() - make_interval(secs => $2)
		  AND at > COALESCE((SELECT max(at) FROM audit_log WHERE action = 'auth.login' AND target = $1), '-infinity')`,
		sub, window.Seconds()).Scan(&n).Error
	return n, err
}

// AuditCountFromIP counts records of action from ip within window.
func (a *Accounts) AuditCountFromIP(ctx context.Context, action, ip string, window time.Duration) (int64, error) {
	var n int64
	err := a.db.WithContext(ctx).Raw(`
		SELECT count(*) FROM audit_log
		WHERE action = $1 AND client_ip = $2 AND at > now() - make_interval(secs => $3)`,
		action, ip, window.Seconds()).Scan(&n).Error
	return n, err
}

func (a *Accounts) CreateCaptcha(ctx context.Context, id, answerHash, ip string, ttl time.Duration) error {
	return a.db.WithContext(ctx).Exec(`INSERT INTO captcha_challenges (id, answer_hash, client_ip, expires_at)
		VALUES ($1, $2, $3, now() + make_interval(secs => $4))`, id, answerHash, ip, ttl.Seconds()).Error
}

func (a *Accounts) ActiveCaptchasFromIP(ctx context.Context, ip string) (int64, error) {
	var n int64
	err := a.db.WithContext(ctx).Raw(`SELECT count(*) FROM captcha_challenges WHERE client_ip = $1 AND expires_at > now()`, ip).Scan(&n).Error
	return n, err
}

// TakeCaptcha deletes and returns a live challenge: every challenge can be
// checked exactly once, right or wrong.
func (a *Accounts) TakeCaptcha(ctx context.Context, id string) (answerHash, ip string, ok bool, err error) {
	var rows []struct {
		AnswerHash string
		ClientIP   string
	}
	err = a.db.WithContext(ctx).Raw(`DELETE FROM captcha_challenges WHERE id = $1 AND expires_at > now()
		RETURNING answer_hash, client_ip`, id).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return "", "", false, err
	}
	return rows[0].AnswerHash, rows[0].ClientIP, true, nil
}

func (a *Accounts) PurgeExpiredCaptchas(ctx context.Context) error {
	return a.db.WithContext(ctx).Exec(`DELETE FROM captcha_challenges WHERE expires_at < now()`).Error
}

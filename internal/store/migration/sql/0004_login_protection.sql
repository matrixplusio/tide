-- Login brute-force protection that holds across replicas: failures are
-- counted from the audit log, and captcha challenges live in the database so
-- a challenge issued by one replica can be answered on another.
CREATE TABLE captcha_challenges (
    id           TEXT PRIMARY KEY,
    answer_hash  TEXT NOT NULL,           -- sha256(id || lower(answer)); the answer is never stored
    client_ip    TEXT NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX captcha_challenges_expires_idx ON captcha_challenges (expires_at);
CREATE INDEX captcha_challenges_ip_idx ON captcha_challenges (client_ip, expires_at);

CREATE INDEX audit_log_action_target_at_idx ON audit_log (action, target, at);
CREATE INDEX audit_log_action_ip_at_idx ON audit_log (action, client_ip, at);

-- Local accounts: the administrator created in the setup wizard, and a
-- break-glass way in when SSO is misconfigured or down. SSO is configured
-- later from settings.
CREATE TABLE local_users (
    username       TEXT PRIMARY KEY CHECK (username ~ '^[a-z][a-z0-9._-]{1,31}$'),
    name           TEXT NOT NULL,
    password_hash  TEXT NOT NULL,              -- bcrypt
    disabled       BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE sessions ADD COLUMN method TEXT NOT NULL DEFAULT 'oidc';

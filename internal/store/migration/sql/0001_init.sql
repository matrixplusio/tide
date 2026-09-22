-- Tide initial schema.
--
-- Applied by the schema owner (see `tide migrate`), never by tide_app: an owner
-- can always re-grant itself UPDATE/DELETE, so append-only audit only holds if
-- the runtime account does not own the tables.

CREATE TABLE releases (
    id            TEXT PRIMARY KEY,           -- REL-20260917-003
    title         TEXT NOT NULL,
    env           TEXT NOT NULL,              -- dev / qa / uat / prod
    jira_ticket   TEXT NOT NULL CHECK (jira_ticket <> ''),
    reason        TEXT NOT NULL CHECK (reason <> ''),
    created_by    TEXT NOT NULL,              -- OIDC sub
    created_by_name TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('draft','confirming','executing','succeeded','failed','cancelled')),
    submitted_at  TIMESTAMPTZ,                -- entered confirming; the 10s window is measured from here
    confirmed_at  TIMESTAMPTZ,
    confirmed_by  TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ
);

CREATE INDEX releases_status_idx ON releases (status, created_at DESC);
CREATE INDEX releases_jira_idx ON releases (jira_ticket);

CREATE TABLE release_counters (
    day  DATE PRIMARY KEY,
    n    INT NOT NULL
);

CREATE TABLE release_items (
    id            BIGSERIAL PRIMARY KEY,
    release_id    TEXT NOT NULL REFERENCES releases(id),
    kind          TEXT NOT NULL,              -- image; later sql / config
    sequence      INT  NOT NULL,              -- same sequence runs in parallel
    payload       JSONB NOT NULL,
    -- planned: release still draft. pending: claimed the target, waiting to run.
    -- skipped: an earlier sequence group failed. cancelled: release cancelled.
    status        TEXT NOT NULL CHECK (status IN ('planned','pending','executing','succeeded','failed','skipped','cancelled')),
    external_ref  TEXT,                       -- image: actual Kargo Promotion name
    error         TEXT,                       -- upstream error verbatim
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ
);

CREATE INDEX release_items_release_idx ON release_items (release_id, sequence);
CREATE INDEX release_items_executing_idx ON release_items (status) WHERE status = 'executing';

-- One in-flight item per service + env. A partial unique index, not an
-- application check: two concurrent requests both pass a "select then insert".
CREATE UNIQUE INDEX one_active_image_per_target
    ON release_items ((payload->>'service'), (payload->>'env'))
    WHERE kind = 'image' AND status IN ('pending', 'executing');

CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor       TEXT NOT NULL,
    actor_name  TEXT NOT NULL,
    action      TEXT NOT NULL,
    target      TEXT,
    jira_ticket TEXT,
    detail      JSONB NOT NULL
);

CREATE INDEX audit_log_at_idx ON audit_log (at DESC);
CREATE INDEX audit_log_target_idx ON audit_log (target);
CREATE INDEX audit_log_jira_idx ON audit_log (jira_ticket);
CREATE INDEX audit_log_actor_idx ON audit_log (actor);

CREATE TABLE settings (
    section     TEXT PRIMARY KEY,             -- oidc / upstreams / permissions / notify / system / setup
    value       JSONB NOT NULL,               -- secrets inside are encrypted with ENCRYPTION_KEY
    updated_by  TEXT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,             -- sha256 of the cookie value
    sub         TEXT NOT NULL,
    name        TEXT NOT NULL,
    email       TEXT,
    groups      JSONB NOT NULL DEFAULT '[]',
    is_admin    BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE admins (
    sub         TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

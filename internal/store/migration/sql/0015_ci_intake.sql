-- CI-triggered releases. A pipeline that has pushed an image tells Tide once
-- and ends; Tide waits for Kargo's warehouse to produce the matching freight,
-- then builds a release the environment's ci mode decides what to do with.

-- Tokens the pipelines authenticate with. Only the hash is stored; the token
-- itself is shown once, at creation.
CREATE TABLE ci_tokens (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    hash            TEXT NOT NULL UNIQUE,
    prefix          TEXT NOT NULL,          -- leading characters, so a token can be told apart in a list
    created_by      TEXT NOT NULL,
    created_by_name TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

-- One notification from CI. It outlives the request: the freight may not
-- exist yet when the pipeline finishes pushing.
CREATE TABLE ci_intake (
    id              BIGSERIAL PRIMARY KEY,
    -- What makes a retry the same request. Defaults to the digest.
    idempotency_key TEXT NOT NULL UNIQUE,
    service         TEXT NOT NULL,
    env             TEXT NOT NULL,
    image           TEXT NOT NULL DEFAULT '',
    digest          TEXT NOT NULL,
    commit_sha      TEXT NOT NULL DEFAULT '',
    pipeline        TEXT NOT NULL DEFAULT '',
    ci_actor        TEXT NOT NULL DEFAULT '',
    jira_ticket     TEXT NOT NULL DEFAULT '',
    reason          TEXT NOT NULL DEFAULT '',
    token_id        TEXT NOT NULL REFERENCES ci_tokens(id),
    status          TEXT NOT NULL CHECK (status IN ('waiting','released','failed','expired')),
    release_id      TEXT REFERENCES releases(id),
    error           TEXT NOT NULL DEFAULT '',
    attempts        INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ci_intake_waiting_idx ON ci_intake (created_at) WHERE status = 'waiting';
CREATE INDEX ci_intake_created_idx ON ci_intake (created_at DESC);

-- Where a release came from. Existing rows were all created in the UI.
ALTER TABLE releases ADD COLUMN source TEXT NOT NULL DEFAULT 'ui'
    CHECK (source IN ('ui', 'ci'));

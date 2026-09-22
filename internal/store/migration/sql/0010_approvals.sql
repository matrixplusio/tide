-- In-Tide approvals: a confirmed release whose environment has an approval
-- rule waits in 'approving' until approved (then executes) or rejected.
ALTER TABLE releases DROP CONSTRAINT releases_status_check;
ALTER TABLE releases ADD CONSTRAINT releases_status_check
    CHECK (status IN ('draft','confirming','approving','executing','succeeded','failed','cancelled','rejected'));

-- The rule as it was when the release was confirmed; later edits do not apply.
ALTER TABLE releases ADD COLUMN approval_rule JSONB;
ALTER TABLE releases ADD COLUMN approval_expires_at TIMESTAMPTZ;

-- One decision per person per release. Append-only for the runtime account.
CREATE TABLE release_approvals (
    id          BIGSERIAL PRIMARY KEY,
    release_id  TEXT NOT NULL REFERENCES releases(id),
    sub         TEXT NOT NULL,
    name        TEXT NOT NULL,
    decision    TEXT NOT NULL CHECK (decision IN ('approve','reject')),
    note        TEXT NOT NULL DEFAULT '',
    decided_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (release_id, sub)
);
CREATE INDEX releases_approving_idx ON releases (approval_expires_at) WHERE status = 'approving';

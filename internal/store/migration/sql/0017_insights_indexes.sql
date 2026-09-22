-- Indexes for the insights page.
--
-- Every report is "all releases in a period, grouped by something". The
-- existing indexes are built for the release list — status first, then
-- created_at — which does not help a query that filters on created_at alone
-- and then reads every row in the period.
--
-- These change no data and no schema. Dropping them costs query time and
-- nothing else.

-- The period filter every insights query starts from.
CREATE INDEX IF NOT EXISTS releases_created_at_idx ON releases (created_at);

-- Grouping by environment inside a period.
CREATE INDEX IF NOT EXISTS releases_env_created_at_idx ON releases (env, created_at);

-- "Which services moved" and "is this service in scope" both reach through
-- release_items by the service name inside the payload. Without this, every
-- report sequentially scans the items table once per query.
CREATE INDEX IF NOT EXISTS release_items_service_idx
    ON release_items ((payload->>'service'));

-- Items are always looked up by their release; the foreign key alone does not
-- create an index on this side.
CREATE INDEX IF NOT EXISTS release_items_release_idx ON release_items (release_id);

-- Approval decisions are read per release (the latest one per release).
CREATE INDEX IF NOT EXISTS release_approvals_release_idx
    ON release_approvals (release_id, decided_at DESC);

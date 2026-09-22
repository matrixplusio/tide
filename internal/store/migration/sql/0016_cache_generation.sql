-- Cross-replica cache invalidation.
--
-- Every replica keeps in-process caches (settings, the RBAC policy, the
-- service catalog). Before this table a replica only ever invalidated its
-- own: an administrator setting a change freeze on one pod left the other
-- pod enforcing the old policy, with no TTL on the settings cache to save it.
--
-- A counter here is bumped inside the same transaction as the write it
-- describes, so a replica that reads the counter and the data sees a pair
-- that agrees. That is why this lives in PostgreSQL and not in a cache
-- server: nothing outside the transaction can be made atomic with it.
CREATE TABLE cache_generation (
    scope TEXT PRIMARY KEY,
    n     BIGINT NOT NULL DEFAULT 0,
    at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO cache_generation (scope) VALUES ('settings'), ('access'), ('catalog');

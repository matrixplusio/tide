-- The idempotency key was the pipeline's own, which is the image digest, and
-- unique across every service and environment. The same image reported to
-- dev and then to qa therefore came back as dev's intake and was never
-- released to qa. Keys are now scoped to service and environment (see
-- pg.ScopedKey); the rows written before get the same form, so a retry of
-- one of them still finds it. Neither name can contain "/". The guard compares the
-- prefix exactly (LIKE would read an "_" in a name as a wildcard) and
-- makes running this twice change nothing.
UPDATE ci_intake
   SET idempotency_key = service || '/' || env || '/' || idempotency_key
 WHERE left(idempotency_key, length(service) + length(env) + 2) <> service || '/' || env || '/';

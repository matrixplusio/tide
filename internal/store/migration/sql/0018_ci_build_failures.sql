-- A pipeline now reports both outcomes, not only the one that leads to a
-- release. A build that failed has nothing to deploy, so it never becomes a
-- release; it is recorded here and announced, and that is the whole of it.

-- A failed build has no image and no digest to report.
ALTER TABLE ci_intake ALTER COLUMN digest SET DEFAULT '';

-- Which job failed. The pipeline is split into compile / package / notify,
-- so it can say where it broke, which "pipeline failed" cannot.
ALTER TABLE ci_intake ADD COLUMN stage TEXT NOT NULL DEFAULT '';

-- A build that succeeded but could not finish telling Tide about it: the
-- image is pushed and the pipeline is green, so nothing else would ever
-- surface it.
ALTER TABLE ci_intake ADD COLUMN warning TEXT NOT NULL DEFAULT '';

-- 'build_failed' is a terminal state of its own. It is not 'failed', which
-- means Tide had an image and could not release it.
--
-- The old constraint was declared inline on the column, so its name was
-- chosen by Postgres. Looking it up beats guessing it: a guess that is wrong
-- fails the migration, and a migration that fails is a deployment that does
-- not start.
DO $$
DECLARE c TEXT;
BEGIN
    SELECT conname INTO c FROM pg_constraint
     WHERE conrelid = 'ci_intake'::regclass AND contype = 'c'
       AND pg_get_constraintdef(oid) LIKE '%status%';
    IF c IS NOT NULL THEN
        EXECUTE format('ALTER TABLE ci_intake DROP CONSTRAINT %I', c);
    END IF;
END $$;

ALTER TABLE ci_intake ADD CONSTRAINT ci_intake_status_check
    CHECK (status IN ('waiting','released','failed','expired','build_failed'));

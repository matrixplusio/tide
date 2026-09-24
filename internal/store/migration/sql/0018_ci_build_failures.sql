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
ALTER TABLE ci_intake DROP CONSTRAINT ci_intake_status_check;
ALTER TABLE ci_intake ADD CONSTRAINT ci_intake_status_check
    CHECK (status IN ('waiting','released','failed','expired','build_failed'));

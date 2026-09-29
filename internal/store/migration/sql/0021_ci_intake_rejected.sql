-- A report Tide refused (unknown environment, CI off there, service not
-- deployed) used to leave no row: the notify step swallows the non-2xx and
-- the pipeline stays green, so the refusal was visible nowhere. It is kept
-- now, in a state of its own that the worker never picks up.
ALTER TABLE ci_intake DROP CONSTRAINT ci_intake_status_check;
ALTER TABLE ci_intake ADD CONSTRAINT ci_intake_status_check
    CHECK (status IN ('waiting','released','failed','expired','build_failed','rejected'));

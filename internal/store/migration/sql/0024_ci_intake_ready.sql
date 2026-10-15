-- An environment can take CI reports only to have its new image found:
-- Tide asks Kargo to look, and once the freight is there the report is done,
-- with no release. That outcome is a state of its own, not "released".
ALTER TABLE ci_intake DROP CONSTRAINT ci_intake_status_check;
ALTER TABLE ci_intake ADD CONSTRAINT ci_intake_status_check
    CHECK (status IN ('waiting','released','failed','expired','build_failed','rejected','ready'));

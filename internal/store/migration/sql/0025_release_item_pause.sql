-- A blue-green or canary rollout stops and waits for a person, possibly for
-- hours. The executor has to know since when, across restarts and across a
-- change of the replica that leads, or it would re-announce the wait and
-- misjudge the deadline. clock_from restarts the execution timeout once the
-- wait is over: measured from started_at, every item that waited would time
-- out the moment it was let through.
ALTER TABLE release_items
    ADD COLUMN paused_since timestamptz,
    ADD COLUMN reminded_at  timestamptz,
    ADD COLUMN clock_from   timestamptz;

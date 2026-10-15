-- An aborted blue-green or canary rollout leaves traffic on the old version
-- while git and Kargo still name the new one. The executor puts them back with
-- a second promotion; external_ref keeps the first, so the services page can
-- still tell which release a promotion belonged to.
ALTER TABLE release_items
    ADD COLUMN revert_ref text;

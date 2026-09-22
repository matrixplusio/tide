-- Restarts join image promotions: at most one in-flight change of any kind
-- per service + environment.
DROP INDEX one_active_image_per_target;
CREATE UNIQUE INDEX one_active_change_per_target
    ON release_items ((payload->>'service'), (payload->>'env'))
    WHERE kind IN ('image', 'restart') AND status IN ('pending', 'executing');

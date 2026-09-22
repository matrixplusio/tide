-- Restart gets its own permission so it can be granted without upgrades.
-- The builtin operator role keeps being able to do both.
UPDATE roles SET permissions = permissions || '["releases.restart"]'::jsonb, updated_at = now()
WHERE id = 'operator' AND NOT permissions ? 'releases.restart';

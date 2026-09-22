-- Environment permissions can be narrowed to projects and service types
-- (the catalog's batch dimension). Empty lists mean all.
ALTER TABLE role_bindings
    ADD COLUMN projects JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN types    JSONB NOT NULL DEFAULT '[]';

-- The same role may be granted to a subject several times with different
-- scopes (e.g. acme backend in uat, globex in qa); only identical grants clash.
ALTER TABLE role_bindings DROP CONSTRAINT role_bindings_role_id_subject_key;
CREATE UNIQUE INDEX role_bindings_scope_key ON role_bindings (role_id, subject, envs, projects, types);

-- Read-only by default: signed-in users no longer release anywhere until an
-- administrator grants a scope. Only the untouched default binding changes.
UPDATE role_bindings SET role_id = 'viewer'
WHERE role_id = 'operator' AND subject = '*' AND created_by = 'migration' AND envs = '["*"]'
  AND NOT EXISTS (SELECT 1 FROM role_bindings v WHERE v.role_id = 'viewer' AND v.subject = '*' AND v.envs = '["*"]');
DELETE FROM role_bindings
WHERE role_id = 'operator' AND subject = '*' AND created_by = 'migration' AND envs = '["*"]';

-- Viewing is scoped too: services.view, releases.view and audit.view became
-- environment permissions, so a grant limited to a project or a service type
-- now limits what its holder can see, not just what they can change.
--
-- The default "everyone is a viewer everywhere" grant would defeat that: a new
-- SSO user would still see every project and the whole audit trail. Remove it;
-- administrators grant the viewer role per project instead.
DELETE FROM role_bindings
WHERE role_id = 'viewer' AND subject = '*' AND created_by = 'migration' AND envs = '["*"]';

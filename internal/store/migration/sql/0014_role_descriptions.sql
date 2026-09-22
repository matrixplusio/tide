-- The builtin descriptions promised more than the roles now give: what they
-- show is limited by the scope of each grant.
UPDATE roles SET description = '在授权范围内查看，并能在授权范围内发起发布、查看 Pod 日志', updated_at = now()
WHERE id = 'operator' AND builtin;
UPDATE roles SET description = '在授权范围内查看服务、发布单和审计', updated_at = now()
WHERE id = 'viewer' AND builtin;

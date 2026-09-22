-- Config syncs join image promotions and restarts: at most one in-flight
-- change of any kind per service + environment.
DROP INDEX one_active_change_per_target;
CREATE UNIQUE INDEX one_active_change_per_target
    ON release_items ((payload->>'service'), (payload->>'env'))
    WHERE kind IN ('image', 'restart', 'sync') AND status IN ('pending', 'executing');

-- Config sync gets its own permission; the builtin operator role can do it.
UPDATE roles SET permissions = permissions || '["releases.sync"]'::jsonb, updated_at = now()
WHERE id = 'operator' AND NOT permissions ? 'releases.sync';
UPDATE roles SET description = '查看全部，并能在授权的环境发起升级、重启服务、同步配置、查看 Pod 日志', updated_at = now()
WHERE id = 'operator' AND builtin;

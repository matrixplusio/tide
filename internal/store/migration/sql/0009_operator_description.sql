-- The builtin operator role can restart too (0007); say so.
UPDATE roles SET description = '查看全部，并能在授权的环境发起升级、重启服务、查看 Pod 日志', updated_at = now()
WHERE id = 'operator' AND builtin;

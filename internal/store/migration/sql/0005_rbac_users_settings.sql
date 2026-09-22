-- Users, groups and role-based access control; settings split into
-- environments / security / release / system. See docs/designs/admin-console.md.

-- Every person who has signed in (SSO) or was created locally.
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    sub           TEXT NOT NULL UNIQUE CHECK (length(sub) BETWEEN 1 AND 255),
    method        TEXT NOT NULL CHECK (method IN ('local', 'oidc')),
    username      TEXT UNIQUE CHECK (username ~ '^[a-z][a-z0-9._-]{1,31}$'),
    name          TEXT NOT NULL,
    email         TEXT,
    idp_groups    JSONB NOT NULL DEFAULT '[]',   -- groups claim at the last SSO sign-in
    disabled      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ,
    CHECK ((method = 'local') = (username IS NOT NULL))
);

CREATE TABLE local_credentials (
    user_id             BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash       TEXT NOT NULL,             -- bcrypt
    password_changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO users (sub, method, username, name, disabled, created_at)
SELECT 'local:' || username, 'local', username, name, disabled, created_at FROM local_users;

INSERT INTO local_credentials (user_id, password_hash, password_changed_at)
SELECT u.id, l.password_hash, l.password_changed_at FROM local_users l JOIN users u ON u.username = l.username;

INSERT INTO users (sub, method, name)
SELECT sub, 'oidc', name FROM admins WHERE sub NOT LIKE 'local:%'
ON CONFLICT (sub) DO NOTHING;

-- Local groups. A binding to group:<name> also matches an IdP group of the same name.
CREATE TABLE groups (
    name        TEXT PRIMARY KEY CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE group_members (
    group_name TEXT NOT NULL REFERENCES groups (name) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_name, user_id)
);
CREATE INDEX group_members_user_idx ON group_members (user_id);

CREATE TABLE roles (
    id          TEXT PRIMARY KEY CHECK (id ~ '^[a-z][a-z0-9-]{1,31}$'),
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    builtin     BOOLEAN NOT NULL DEFAULT false,
    permissions JSONB NOT NULL,                    -- ["*"] = every permission, present and future
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO roles (id, name, description, builtin, permissions) VALUES
    ('admin', '管理员', '拥有全部权限', true, '["*"]'),
    ('operator', '发布者', '查看全部，并能在授权的环境发起发布、查看 Pod 日志', true,
        '["services.view", "releases.view", "audit.view", "pods.view", "releases.create"]'),
    ('viewer', '只读', '只能查看服务、发布单和审计', true,
        '["services.view", "releases.view", "audit.view"]');

-- subject: "*" (every signed-in user) | "user:<sub>" | "group:<name>"
-- envs:    ["*"] | ["tier:<tier>", "<env>", ...]; only env-scoped permissions use it
CREATE TABLE role_bindings (
    id         BIGSERIAL PRIMARY KEY,
    role_id    TEXT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    subject    TEXT NOT NULL CHECK (subject = '*' OR subject ~ '^(user|group):.+$'),
    envs       JSONB NOT NULL DEFAULT '["*"]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT NOT NULL,
    UNIQUE (role_id, subject)
);
CREATE INDEX role_bindings_subject_idx ON role_bindings (subject);

-- Product boundary: environment permissions are open by default.
INSERT INTO role_bindings (role_id, subject, envs, created_by) VALUES ('operator', '*', '["*"]', 'migration');
INSERT INTO role_bindings (role_id, subject, envs, created_by) SELECT 'admin', 'user:' || sub, '["*"]', 'migration' FROM admins;

DROP TABLE admins;
DROP TABLE local_users;

-- Sessions now point at users; profile data is read from users on every
-- request. Existing sessions are dropped: everyone signs in once more.
DROP TABLE sessions;
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,                 -- sha256 of the cookie value
    user_id     BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_ip   TEXT,
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Settings: environments become their own section (order = promotion chain).
INSERT INTO settings (section, value, updated_by)
SELECT 'environments', jsonb_build_object('items', jsonb_agg(jsonb_build_object(
        'name', e.name,
        'displayName', e.name,
        'tier', CASE
            WHEN e.name LIKE 'prod%' THEN 'production'
            WHEN e.name = 'uat' OR e.name LIKE 'stag%' OR e.name LIKE 'pre%' THEN 'staging'
            WHEN e.name IN ('qa', 'sit') OR e.name LIKE 'test%' THEN 'testing'
            ELSE 'development' END,
        'description', '',
        'upstream', COALESCE((SELECT it->>'name' FROM jsonb_array_elements(s.value->'items') it
                              WHERE it->'envs' ? e.name LIMIT 1), '')
    ) ORDER BY e.ord)), 'migration'
FROM settings s, jsonb_array_elements_text(COALESCE(s.value->'envOrder', '[]')) WITH ORDINALITY AS e (name, ord)
WHERE s.section = 'upstreams'
GROUP BY s.section;

UPDATE settings SET value = jsonb_build_object('items',
    COALESCE((SELECT jsonb_agg(it - 'envs') FROM jsonb_array_elements(value->'items') it), '[]'))
WHERE section = 'upstreams';

-- Notifications: webhooks become channels plus one catch-all rule.
UPDATE settings SET value = jsonb_build_object(
    'channels', COALESCE((SELECT jsonb_agg(jsonb_build_object('name', w->>'name', 'kind', w->>'kind', 'url', w->>'url',
                                                             'secret', '', 'enabled', true))
                          FROM jsonb_array_elements(value->'webhooks') w), '[]'),
    'rules', CASE WHEN jsonb_array_length(COALESCE(value->'webhooks', '[]')) = 0 THEN '[]'::jsonb
        ELSE jsonb_build_array(jsonb_build_object('name', '默认', 'enabled', true, 'envs', '["*"]'::jsonb,
            'events', '["release.started", "release.succeeded", "release.failed"]'::jsonb,
            'channels', (SELECT jsonb_agg(w->>'name') FROM jsonb_array_elements(value->'webhooks') w))) END)
WHERE section = 'notify';

-- System splits into security / release / system. Missing keys take defaults on load.
INSERT INTO settings (section, value, updated_by)
SELECT 'security', jsonb_strip_nulls(jsonb_build_object('sessionTtlMinutes', value->'sessionTtlMinutes')), 'migration'
FROM settings WHERE section = 'system';

INSERT INTO settings (section, value, updated_by)
SELECT 'release', jsonb_strip_nulls(jsonb_build_object('minSoakMinutes', value->'minSoakMinutes',
                                                       'multiVersionJump', value->'multiVersionJump')), 'migration'
FROM settings WHERE section = 'system';

UPDATE settings SET value = value - 'sessionTtlMinutes' - 'minSoakMinutes' - 'multiVersionJump' WHERE section = 'system';

DELETE FROM settings WHERE section = 'permissions';

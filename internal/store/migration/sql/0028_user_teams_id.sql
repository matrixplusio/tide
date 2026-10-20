-- A Teams card mentions a person by their Microsoft Entra object id, the
-- one id an incoming webhook or a Power Automate workflow can resolve. Kept
-- per user beside the Lark id: from the SSO token when the IdP sends it,
-- otherwise as an administrator entered it.
ALTER TABLE users
    ADD COLUMN teams_id        text CHECK (teams_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'),
    ADD COLUMN teams_id_source text CHECK (teams_id_source IN ('sso', 'manual'));

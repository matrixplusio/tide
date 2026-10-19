-- Lark mentions a person in a group bot's message only by open_id or
-- user_id, never by email. Tide keeps that id per user: from the SSO token
-- when the IdP sends it, otherwise as an administrator entered it.
ALTER TABLE users
    ADD COLUMN lark_id        text CHECK (lark_id ~ '^[A-Za-z0-9_-]{1,64}$'),
    ADD COLUMN lark_id_source text CHECK (lark_id_source IN ('sso', 'manual'));

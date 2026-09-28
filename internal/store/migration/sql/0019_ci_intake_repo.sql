-- Where the image was built from. With it, two releases of a service can be
-- compared as commits rather than as tags — "what is in this one" is the
-- question a developer asks before pressing confirm, and a tag cannot
-- answer it. Optional: pipelines on an older component version do not send
-- it, and an intake without it still does everything it did before.
ALTER TABLE ci_intake ADD COLUMN repo TEXT NOT NULL DEFAULT '';

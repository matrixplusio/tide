-- The last commits behind the image, as the pipeline saw them: newest first,
-- id / title / author / at, up to fifty. Two builds' histories are enough to
-- say what one release changes over another, without Tide holding any
-- credential for the source host — the pipeline already had the history.
ALTER TABLE ci_intake ADD COLUMN commits JSONB NOT NULL DEFAULT '[]'::jsonb;

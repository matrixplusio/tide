-- A release made from a CI report showed the token's name as its creator:
-- every one read "gitlab-ci" whoever ran the pipeline. It now carries the
-- pipeline's actor, and the ones already made get theirs from the report
-- they came from. Only the name: created_by stays ci:<token>, which is what
-- tells a CI release from a person's.
UPDATE releases r
   SET created_by_name = i.ci_actor
  FROM ci_intake i
 WHERE i.release_id = r.id
   AND r.source = 'ci'
   AND i.ci_actor <> '';

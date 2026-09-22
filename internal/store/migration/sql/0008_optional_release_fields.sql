-- Jira ticket and reason are required only where the release policy says so
-- (Jira: production-tier environments by default; reason: everywhere by
-- default); elsewhere a release may leave them empty.
ALTER TABLE releases DROP CONSTRAINT IF EXISTS releases_jira_ticket_check;
ALTER TABLE releases DROP CONSTRAINT IF EXISTS releases_reason_check;

-- Trace every audit record back to the HTTP request and client that caused it.
ALTER TABLE audit_log ADD COLUMN request_id TEXT;
ALTER TABLE audit_log ADD COLUMN client_ip  TEXT;
CREATE INDEX audit_log_action_idx ON audit_log (action);

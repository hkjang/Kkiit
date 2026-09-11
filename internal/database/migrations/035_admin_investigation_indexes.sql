-- The audit log gained filters, and a filter without an index behind it turns
-- the answer to "who changed this" into a scan of every action ever recorded.
CREATE INDEX IF NOT EXISTS audit_logs_action_idx ON audit_logs(action, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_resource_idx ON audit_logs(resource_type, resource_id, occurred_at DESC);

-- The case file for one person counts their disputes and reports from both
-- sides, which nothing indexed before because nothing asked for it.
CREATE INDEX IF NOT EXISTS disputes_opened_by_idx ON disputes(opened_by);
CREATE INDEX IF NOT EXISTS reports_reporter_idx ON reports(reporter_id);
CREATE INDEX IF NOT EXISTS reports_resource_idx ON reports(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS organization_users_member_idx ON organization_users(user_id);

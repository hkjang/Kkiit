-- The dispute queue showed the newest case first, which starves the oldest one:
-- the person who has been waiting longest with their money frozen sank to the
-- bottom of the list. The ordering is fixed in code; this records the promise
-- the console now measures against.
UPDATE system_settings
SET value = '{"dispute_sla_hours":72}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'marketplace.policy';

-- Both queues now scan for the oldest unhandled row.
CREATE INDEX IF NOT EXISTS disputes_open_age_idx ON disputes(created_at)
	WHERE state IN ('open', 'under_review');
CREATE INDEX IF NOT EXISTS reports_open_age_idx ON reports(created_at)
	WHERE state IN ('open', 'reviewing');

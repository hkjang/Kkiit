-- sla.policy already carried a dispute response target when a second one was
-- added to marketplace.policy. An operator tightening the number that reads
-- like the right one changed nothing, which is the same failure as a setting
-- nothing reads at all, only harder to notice.
UPDATE system_settings
SET value = jsonb_build_object('approval_hours', 48) || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'sla.policy';

UPDATE system_settings
SET value = (value - 'dispute_sla_hours') - 'approval_sla_hours',
    version = version + 1,
    updated_at = now()
WHERE key = 'marketplace.policy';

-- The event retry limit is read from notification.dispatch, so the duplicate
-- here was decorative in the same way.
UPDATE system_settings
SET value = value - 'outbox_retry_limit',
    version = version + 1,
    updated_at = now()
WHERE key = 'workflow.defaults';

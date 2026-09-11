-- Nothing happened when a paid order passed the date it was promised for. The
-- buyer's money stayed in escrow, neither side was told, and the only way out
-- was for the buyer to notice by themselves and open a dispute. Auto accept
-- already protects a seller from an inattentive buyer; this is the mirror that
-- was missing.
INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
	('OrderOverdue','web','ko-KR','납기 초과 {{order_number}}','{{talent_title}} 주문이 약속한 납기를 지났습니다. 진행 상황을 확인해 주세요.')
ON CONFLICT DO NOTHING;

-- overdue_cancel_days is the grace a seller still has after the promised date
-- before the buyer can end the order themselves. Setting it to 0 turns the
-- self service path off and sends every cancellation back to an operator.
UPDATE system_settings
SET value = '{"overdue_cancel_days":7}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'marketplace.policy';

-- The overdue scan asks whether an order has already been flagged, once per
-- candidate order.
CREATE INDEX IF NOT EXISTS domain_events_aggregate_type_idx ON domain_events(aggregate_id, event_type);

-- The scan itself looks for late orders in the pre-delivery states.
CREATE INDEX IF NOT EXISTS orders_due_idx ON orders(due_at)
	WHERE state IN ('PAID', 'REQUIREMENT_PENDING', 'READY', 'IN_PROGRESS', 'REVISION_REQUESTED', 'CANCEL_REQUESTED');

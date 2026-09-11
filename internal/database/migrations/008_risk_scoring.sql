-- Existing operator values win; only the newly introduced keys are filled in.
UPDATE system_settings
SET value = '{"enabled":true,"medium_threshold":40,"scan_interval_minutes":5,"scan_batch":200}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'risk.policy';

UPDATE system_settings
SET value = '{"batch_size":100}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'settlement.policy';

CREATE INDEX IF NOT EXISTS risk_scores_resource_idx ON risk_scores(resource_type, resource_id, calculated_at DESC);
CREATE INDEX IF NOT EXISTS settlements_due_idx ON settlements(state, scheduled_at);

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('SettlementConfirmed','web','ko-KR','정산 확정','{{order_number}} 주문의 정산이 지급 대기 상태가 되었습니다.'),
('SettlementHeld','web','ko-KR','정산 보류','{{order_number}} 주문의 정산이 보류되었습니다. 사유: {{hold_reason}}')
ON CONFLICT (key) DO NOTHING;

CREATE INDEX IF NOT EXISTS disputes_queue_idx ON disputes(state, created_at DESC);
CREATE INDEX IF NOT EXISTS disputes_order_idx ON disputes(order_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS disputes_open_per_order_idx ON disputes(order_id) WHERE state IN ('open', 'under_review');
CREATE INDEX IF NOT EXISTS refunds_order_idx ON refunds(order_id, created_at DESC);

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('DisputeOpened','web','ko-KR','분쟁 접수 {{order_number}}','{{talent_title}} 주문에 분쟁이 접수되었습니다. 사유: {{reason}}'),
('DisputeResolved','web','ko-KR','분쟁 처리 완료 {{order_number}}','{{talent_title}} 주문의 분쟁이 {{outcome_label}}(으)로 처리되었습니다.')
ON CONFLICT (key) DO NOTHING;

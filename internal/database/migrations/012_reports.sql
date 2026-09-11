CREATE INDEX IF NOT EXISTS reports_queue_idx ON reports(state, created_at DESC);
CREATE INDEX IF NOT EXISTS reports_resource_idx ON reports(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS reports_reporter_idx ON reports(reporter_id, created_at DESC);
-- One open report per reporter and resource keeps the queue from filling with
-- the same complaint submitted repeatedly.
CREATE UNIQUE INDEX IF NOT EXISTS reports_open_unique_idx ON reports(reporter_id, resource_type, resource_id) WHERE state IN ('open', 'reviewing');

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('ReportResolved','web','ko-KR','신고 처리 완료','접수하신 신고가 {{action_label}}(으)로 처리되었습니다.'),
('TalentPaused','web','ko-KR','상품 비공개 전환','{{talent_title}} 상품이 운영 검토로 비공개 처리되었습니다.')
ON CONFLICT (key) DO NOTHING;

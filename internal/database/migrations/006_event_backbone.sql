ALTER TABLE notifications ADD COLUMN IF NOT EXISTS event_id uuid REFERENCES domain_events(id) ON DELETE SET NULL;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS event_type text NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS link text NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS read_at timestamptz;
CREATE INDEX IF NOT EXISTS notifications_inbox_idx ON notifications(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS notifications_unread_idx ON notifications(user_id) WHERE read_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS notifications_event_user_idx ON notifications(event_id, user_id, channel) WHERE event_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS domain_events_recent_idx ON domain_events(created_at DESC);
ALTER TABLE webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_event_id_fkey;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_event_id_fkey FOREIGN KEY (event_id) REFERENCES domain_events(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS webhook_deliveries_pending_idx ON webhook_deliveries(state, next_attempt_at) WHERE state IN ('pending','retry');
CREATE INDEX IF NOT EXISTS webhook_deliveries_webhook_idx ON webhook_deliveries(webhook_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS webhook_deliveries_unique_idx ON webhook_deliveries(webhook_id, event_id);

INSERT INTO permissions(code, name, description) VALUES
('webhooks.manage.self','개인 웹훅 관리','자신의 이벤트 웹훅 등록과 재전송'),
('events.manage','이벤트 운영','이벤트 아웃박스와 알림 전달 상태 조회 및 재처리')
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description;

INSERT INTO role_permissions(role_code, permission_code) VALUES
('super_admin','webhooks.manage.self'),('super_admin','events.manage'),
('operator','events.manage'),
('security_admin','events.manage'),
('buyer','webhooks.manage.self'),
('seller','webhooks.manage.self')
ON CONFLICT DO NOTHING;

INSERT INTO system_settings(key, value, description) VALUES
('notification.webhook','{"enabled":true,"max_attempts":6,"timeout_seconds":10,"allow_private_targets":true,"signature_algorithm":"sha256"}'::jsonb,'이벤트 웹훅 전달 정책'),
('notification.dispatch','{"poll_seconds":2,"event_batch":50,"delivery_batch":25,"event_retry_limit":10,"stuck_after_minutes":5,"retention_days":90}'::jsonb,'이벤트 아웃박스 디스패처 정책')
ON CONFLICT (key) DO NOTHING;

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('OrderCreated','web','ko-KR','새 주문 {{order_number}}','{{talent_title}} 주문이 접수되었습니다.'),
('OrderPAYMENT_PENDING','web','ko-KR','결제 대기 {{order_number}}','{{talent_title}} 주문의 결제를 기다리고 있습니다.'),
('OrderPAID','web','ko-KR','결제 완료 {{order_number}}','{{talent_title}} 주문 결제가 확인되어 에스크로에 보관되었습니다.'),
('OrderREQUIREMENT_PENDING','web','ko-KR','요구사항 대기 {{order_number}}','{{talent_title}} 주문의 요구사항 제출을 기다리고 있습니다.'),
('OrderREADY','web','ko-KR','작업 준비 완료 {{order_number}}','{{talent_title}} 주문이 작업 시작 준비 상태가 되었습니다.'),
('OrderIN_PROGRESS','web','ko-KR','작업 시작 {{order_number}}','{{talent_title}} 주문 작업이 시작되었습니다.'),
('OrderDELIVERED','web','ko-KR','납품 도착 {{order_number}}','{{talent_title}} 주문의 결과물이 납품되었습니다.'),
('OrderREVISION_REQUESTED','web','ko-KR','수정 요청 {{order_number}}','{{talent_title}} 주문에 수정 요청이 접수되었습니다.'),
('OrderACCEPTED','web','ko-KR','구매확정 {{order_number}}','{{talent_title}} 주문이 구매확정되었습니다.'),
('OrderCOMPLETED','web','ko-KR','거래 완료 {{order_number}}','{{talent_title}} 주문이 완료되었습니다.'),
('OrderCANCEL_REQUESTED','web','ko-KR','취소 요청 {{order_number}}','{{talent_title}} 주문에 취소 요청이 접수되었습니다.'),
('OrderCANCELLED','web','ko-KR','주문 취소 {{order_number}}','{{talent_title}} 주문이 취소되었습니다.'),
('OrderDISPUTED','web','ko-KR','분쟁 접수 {{order_number}}','{{talent_title}} 주문에 분쟁이 접수되었습니다.'),
('OrderREFUNDED','web','ko-KR','환불 처리 {{order_number}}','{{talent_title}} 주문이 환불 처리되었습니다.'),
('MessageCreated','web','ko-KR','새 메시지 {{order_number}}','{{actor_name}}님이 주문 대화에 메시지를 남겼습니다.'),
('ReviewCreated','web','ko-KR','리뷰 등록 {{order_number}}','구매자가 거래 리뷰를 남겼습니다.'),
('TalentPublished','web','ko-KR','상품 공개','{{talent_title}} 상품이 공개되었습니다.'),
('TalentRejected','web','ko-KR','상품 반려','{{talent_title}} 상품 공개 요청이 반려되었습니다.'),
('SettlementCreated','web','ko-KR','정산 예정','정산 {{net_amount}}원이 예정되었습니다.'),
('QuoteCreated','web','ko-KR','새 견적 도착','{{rfq_title}} 프로젝트에 새 견적이 도착했습니다.')
ON CONFLICT (key) DO NOTHING;

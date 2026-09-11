-- A quote had no way to become an order: orders must reference a talent, so a
-- quote now names the service the seller will deliver it through.
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS talent_id uuid REFERENCES talents(id);
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS order_id uuid REFERENCES orders(id);
CREATE INDEX IF NOT EXISTS quotes_state_idx ON quotes(rfq_id, state);

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('QuoteAccepted','web','ko-KR','견적이 선택되었습니다','{{rfq_title}} 프로젝트의 견적이 선택되어 주문 {{order_number}}이 생성되었습니다.'),
('QuoteRejected','web','ko-KR','다른 견적이 선택되었습니다','{{rfq_title}} 프로젝트에서 다른 판매자의 견적이 선택되었습니다.')
ON CONFLICT (key) DO NOTHING;

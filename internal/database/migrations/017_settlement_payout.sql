INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('SettlementPaid','web','ko-KR','정산 지급 완료','{{order_number}} 주문의 정산 {{net_amount}}원이 지급되었습니다.')
ON CONFLICT (key) DO NOTHING;

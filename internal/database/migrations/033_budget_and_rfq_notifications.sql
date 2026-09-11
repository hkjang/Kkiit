-- BudgetExhausted was advertised in the webhook catalogue and had never been
-- emitted, and RFQCreated was emitted with no template to render it. Both are
-- the same failure: the product offers to tell someone something and then does
-- not, with nothing anywhere reporting an error.
INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
	('BudgetExhausted','web','ko-KR','예산 소진 {{budget_name}}','{{budget_name}} 예산의 잔액이 부족해 구성원의 주문이 거절되었습니다. 예산을 늘리거나 새 예산을 만들어 주세요.'),
	('RFQCreated','web','ko-KR','견적 요청 등록 {{rfq_title}}','{{rfq_title}} 견적 요청이 등록되었습니다. 판매자 견적이 도착하면 알려드립니다.')
ON CONFLICT DO NOTHING;

-- A budget had no updated_at, so there was no way to tell a top-up from the
-- original row. Announcing exhaustion once per budget forever would go silent
-- after the first refusal; announcing every refusal would spam. The column is
-- what separates the two.
ALTER TABLE budgets ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

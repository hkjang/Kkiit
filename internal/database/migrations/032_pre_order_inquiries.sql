-- A buyer with a question about scope had two options: order and hope, or open
-- a request for quotation, which is a heavyweight thing to do when you only
-- want to ask whether a logo package includes the source files. Messages only
-- existed inside an order, so the conversation that decides whether there will
-- be an order had nowhere to happen. The buyers who are unsure simply leave,
-- and the ones who order anyway arrive with assumptions that turn into
-- revisions and disputes.
CREATE TABLE IF NOT EXISTS inquiries (
	id uuid PRIMARY KEY,
	talent_id uuid NOT NULL REFERENCES talents(id) ON DELETE CASCADE,
	buyer_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	seller_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	state text NOT NULL DEFAULT 'open',
	last_message_at timestamptz NOT NULL DEFAULT now(),
	created_at timestamptz NOT NULL DEFAULT now()
);

-- One thread per buyer per listing. A buyer who asks again is continuing the
-- same conversation, and it also bounds how much a single account can open.
CREATE UNIQUE INDEX IF NOT EXISTS inquiries_pair_idx ON inquiries(talent_id, buyer_id);
CREATE INDEX IF NOT EXISTS inquiries_seller_idx ON inquiries(seller_id, last_message_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS inquiries_buyer_idx ON inquiries(buyer_id, last_message_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS inquiry_messages (
	id uuid PRIMARY KEY,
	inquiry_id uuid NOT NULL REFERENCES inquiries(id) ON DELETE CASCADE,
	sender_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	body text NOT NULL,
	read_at timestamptz,
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS inquiry_messages_thread_idx ON inquiry_messages(inquiry_id, created_at);

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
	('InquiryMessageCreated','web','ko-KR','상품 문의 {{talent_title}}','{{talent_title}} 상품에 새 문의 메시지가 도착했습니다.')
ON CONFLICT DO NOTHING;

-- Event notifications over the company SMTP relay. Nothing here runs on a
-- request path: the dispatcher queues a row in the same transaction as the
-- inbox notification and a later pass sends it, so a relay that is slow or
-- down never slows a payment. Every attempt stays in this table so an
-- administrator can answer "it never arrived" with what actually left.
--
-- The body is kept only while a row is in flight and is cleared once it is
-- sent or given up on. The record shows subject and recipient; a copy of the
-- table is not a copy of every mail.
CREATE TABLE IF NOT EXISTS mail_deliveries (
    id uuid PRIMARY KEY,
    event_id uuid REFERENCES domain_events(id) ON DELETE SET NULL,
    event_type text NOT NULL,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    recipient text NOT NULL,
    subject text NOT NULL,
    link text NOT NULL DEFAULT '',
    body text,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sending','retry','sent','failed')),
    attempts integer NOT NULL DEFAULT 0,
    last_error text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS mail_deliveries_pending_idx ON mail_deliveries(next_attempt_at) WHERE status IN ('queued','retry');
CREATE INDEX IF NOT EXISTS mail_deliveries_recent_idx ON mail_deliveries(created_at DESC);
-- Re-dispatching an event must not mail the same person twice.
CREATE UNIQUE INDEX IF NOT EXISTS mail_deliveries_event_user_idx ON mail_deliveries(event_id, user_id) WHERE event_id IS NOT NULL;

-- Off by default. The password is the row's encrypted secret, never the value.
-- An internal relay is usually port 25 without credentials or TLS, so that is
-- what the defaults describe; authentication and encryption are used when the
-- relay offers them.
INSERT INTO system_settings(key, value, is_secret, description) VALUES
('mail','{"enabled":false,"smtp_host":"","smtp_port":25,"security":"auto","skip_tls_verify":false,"username":"","from_address":"","from_name":"Kkiit","base_url":"","timeout_seconds":10,"notify_order_paid":true,"notify_order_delivered":true,"notify_revision_requested":true,"notify_quote":true,"notify_dispute":true,"notify_settlement_held":true}'::jsonb,true,'사내 SMTP 릴레이 메일 알림')
ON CONFLICT (key) DO NOTHING;

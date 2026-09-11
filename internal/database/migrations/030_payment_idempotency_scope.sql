-- The idempotency key was unique across every payment ever made, but it is
-- looked up per order. A buyer whose client sends a plain key like "retry-1"
-- therefore fails at the insert with an opaque server error the moment anyone
-- else has ever used that string, and a caller could reserve common keys to
-- keep other people's payments from going through. The key belongs to the
-- order it retries, so that is where it is unique.
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_idempotency_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS payments_order_idempotency_idx ON payments(order_id, idempotency_key);

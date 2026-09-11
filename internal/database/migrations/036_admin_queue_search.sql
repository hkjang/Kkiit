-- The operator queues gained search, and a prefix search on an order number
-- without an index is a scan of every order the platform has taken.
CREATE INDEX IF NOT EXISTS orders_number_idx ON orders(order_number text_pattern_ops);
CREATE INDEX IF NOT EXISTS settlements_state_idx ON settlements(state, created_at DESC);
CREATE INDEX IF NOT EXISTS talents_status_updated_idx ON talents(status, updated_at DESC);

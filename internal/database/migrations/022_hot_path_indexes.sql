-- Every one of these columns is read on a request path but had no index behind
-- it, so PostgreSQL scanned the whole table to answer. Measured on 200k rows,
-- the order conversation lookup went from a 10.8ms parallel sequential scan to
-- a 0.2ms index scan, and the gap widens linearly as the table grows.

-- The order workspace and the message socket both read a conversation in
-- chronological order, so the sort column joins the index.
CREATE INDEX IF NOT EXISTS messages_order_idx ON messages(order_id, created_at);

-- Escrow balance is recomputed on payment, delivery, acceptance, dispute,
-- refund and settlement. It is the most frequently summed table in the product.
CREATE INDEX IF NOT EXISTS ledger_entries_order_idx ON ledger_entries(order_id, account);
CREATE INDEX IF NOT EXISTS payments_order_idx ON payments(order_id, created_at DESC);

-- The talent detail page and every order that prices options read these by
-- talent, and the marketplace listing joins orders by talent for its stats.
CREATE INDEX IF NOT EXISTS talent_options_talent_idx ON talent_options(talent_id);
CREATE INDEX IF NOT EXISTS talent_requirements_talent_idx ON talent_requirements(talent_id);
CREATE INDEX IF NOT EXISTS orders_talent_idx ON orders(talent_id);

-- Seller rating is recomputed after every review and read on every seller card.
CREATE INDEX IF NOT EXISTS reviews_seller_idx ON reviews(seller_id);

-- The order list runs this as a correlated subquery, once per row on screen.
CREATE INDEX IF NOT EXISTS revisions_order_idx ON revisions(order_id);

CREATE INDEX IF NOT EXISTS rfqs_buyer_idx ON rfqs(buyer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS quotes_order_idx ON quotes(order_id) WHERE order_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS quotes_seller_idx ON quotes(seller_id, created_at DESC);
CREATE INDEX IF NOT EXISTS webhooks_owner_idx ON webhooks(owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS webhook_deliveries_event_idx ON webhook_deliveries(event_id);
CREATE INDEX IF NOT EXISTS ai_usage_month_idx ON ai_usage(occurred_at DESC);

-- Deliberately left unindexed: columns that only ever appear in the SELECT list
-- or are reached through the row's own primary key (orders.budget_id,
-- orders.coupon_id, orders.package_id, revisions.delivery_id), and audit style
-- "who did this" columns that nothing filters on. An index nobody reads still
-- costs every write.

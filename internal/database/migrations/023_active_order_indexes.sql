-- Seller capacity is checked on every order creation and rendered on every
-- marketplace card, and it counts a seller's live orders. Without a predicate
-- index that count walks every order the seller has ever had, so the check gets
-- slower the more successful the seller is. Live orders are a small and roughly
-- constant slice of the table, which is exactly what a partial index is for.
CREATE INDEX IF NOT EXISTS orders_seller_active_idx ON orders(seller_id)
	WHERE state NOT IN ('COMPLETED', 'CANCELLED', 'REFUNDED');

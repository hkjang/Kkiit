-- The discount is funded by the platform: the buyer pays less while the seller
-- still settles on the full order amount, so the order keeps the gross price
-- and records what was taken off separately.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS coupon_id uuid REFERENCES coupons(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS discount_amount bigint NOT NULL DEFAULT 0 CHECK (discount_amount >= 0);

CREATE TABLE IF NOT EXISTS coupon_redemptions (
    id uuid PRIMARY KEY,
    coupon_id uuid NOT NULL REFERENCES coupons(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    discount_amount bigint NOT NULL CHECK (discount_amount >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(order_id)
);
CREATE INDEX IF NOT EXISTS coupon_redemptions_coupon_idx ON coupon_redemptions(coupon_id, created_at DESC);
CREATE INDEX IF NOT EXISTS coupon_redemptions_user_idx ON coupon_redemptions(coupon_id, user_id);

ALTER TABLE coupons ADD COLUMN IF NOT EXISTS created_by uuid REFERENCES users(id);
ALTER TABLE coupons ADD CONSTRAINT coupons_discount_type_check CHECK (discount_type IN ('percent','fixed')) NOT VALID;

INSERT INTO permissions(code, name, description) VALUES
('coupons.manage','쿠폰 관리','할인 쿠폰 발행과 중단')
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description;

INSERT INTO role_permissions(role_code, permission_code) VALUES
('super_admin','coupons.manage'),
('operator','coupons.manage')
ON CONFLICT DO NOTHING;

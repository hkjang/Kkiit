-- Orders placed for a company need to be attributable to it, not only to the
-- person who clicked buy.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS organization_id uuid REFERENCES organizations(id);
CREATE INDEX IF NOT EXISTS orders_organization_idx ON orders(organization_id, created_at DESC);
-- Tracking the reserved amount on the order makes releasing it idempotent: a
-- refund can only ever give back what this order still holds.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS budget_id uuid REFERENCES budgets(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS budget_consumed bigint NOT NULL DEFAULT 0 CHECK (budget_consumed >= 0);

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE organization_users ADD CONSTRAINT organization_users_role_check CHECK (role IN ('owner','admin','member')) NOT VALID;
CREATE INDEX IF NOT EXISTS organization_users_user_idx ON organization_users(user_id);

ALTER TABLE budgets ADD CONSTRAINT budgets_scope_check CHECK (scope_type IN ('organization','team','user')) NOT VALID;
CREATE INDEX IF NOT EXISTS budgets_active_idx ON budgets(organization_id, scope_type, ends_at);

INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
('OrganizationMemberAdded','web','ko-KR','조직에 초대되었습니다','{{organization_name}} 조직의 {{role_label}}로 추가되었습니다.'),
('BudgetExhausted','web','ko-KR','예산 소진 임박','{{organization_name}} 조직의 {{budget_name}} 예산이 {{consumed_percent}}% 사용되었습니다.')
ON CONFLICT (key) DO NOTHING;

-- Unpaid orders used to sit for ever, holding an organization's budget and a
-- coupon use. The dispatcher now expires them on this schedule.
UPDATE system_settings
SET value = '{"order_expiry_hours":72}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'marketplace.policy';

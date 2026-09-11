-- Keyset paging was added to the operator lists but nothing backed the sort.
-- Without a composite index on (sort key, id) PostgreSQL sorts the whole table
-- to return one page, so the lists that grow forever were also the slowest.
CREATE INDEX IF NOT EXISTS audit_logs_keyset_idx ON audit_logs(occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS orders_keyset_idx ON orders(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS settlements_keyset_idx ON settlements(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS users_keyset_idx ON users(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS talents_keyset_idx ON talents(updated_at DESC, id DESC);

-- The per seller and per organization pages filter first, so the owning column
-- leads the index.
CREATE INDEX IF NOT EXISTS settlements_seller_keyset_idx ON settlements(seller_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS orders_organization_keyset_idx ON orders(organization_id, created_at DESC, id DESC) WHERE organization_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS favorites_talent_idx ON favorites(talent_id);
CREATE INDEX IF NOT EXISTS favorites_user_idx ON favorites(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS portfolios_owner_idx ON portfolios(owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS file_objects_owner_idx ON file_objects(owner_id, created_at DESC);

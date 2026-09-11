-- A single file was capped but the number of them was not, so any order party
-- could fill the database one 50MB row at a time.
UPDATE system_settings
SET value = '{"max_files_per_order":100,"daily_upload_mb_per_user":500}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'storage.policy';

CREATE INDEX IF NOT EXISTS file_objects_order_idx ON file_objects(order_id);

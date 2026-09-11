-- Linking a provider to an account that already exists has to be a deliberate
-- act by the signed in user, so the authorisation request carries who asked.
ALTER TABLE oauth_states ADD COLUMN IF NOT EXISTS link_user_id uuid REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS external_identities_user_idx ON external_identities(user_id);

UPDATE system_settings
SET value = '{"link_by_verified_email":true}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'auth.oauth';

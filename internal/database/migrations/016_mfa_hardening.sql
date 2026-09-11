-- A TOTP code stays valid for its whole window, so a stolen one could be used
-- twice. Remembering the consumed step makes each code single use.
ALTER TABLE mfa_factors ADD COLUMN IF NOT EXISTS last_step bigint NOT NULL DEFAULT 0;

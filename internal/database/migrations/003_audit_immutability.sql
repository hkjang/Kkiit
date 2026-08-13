CREATE OR REPLACE FUNCTION prevent_audit_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs are immutable';
END;
$$;

DROP TRIGGER IF EXISTS audit_logs_immutable ON audit_logs;
CREATE TRIGGER audit_logs_immutable
BEFORE UPDATE OR DELETE ON audit_logs
FOR EACH ROW EXECUTE FUNCTION prevent_audit_mutation();

CREATE TABLE IF NOT EXISTS mfa_factors (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    factor_type text NOT NULL CHECK (factor_type IN ('totp','webauthn')),
    name text NOT NULL,
    secret_encrypted bytea,
    credential jsonb,
    enabled boolean NOT NULL DEFAULT false,
    confirmed_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, factor_type, name)
);

CREATE TABLE IF NOT EXISTS login_challenges (
    token_hash bytea PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge_type text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

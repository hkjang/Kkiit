UPDATE system_settings
SET value = value - 'admin_mfa_required',
    version = version + 1,
    updated_at = now()
WHERE key = 'security.policy' AND value ? 'admin_mfa_required';

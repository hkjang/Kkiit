CREATE INDEX IF NOT EXISTS ai_usage_period_idx ON ai_usage(occurred_at DESC);
CREATE INDEX IF NOT EXISTS ai_usage_feature_idx ON ai_usage(feature, occurred_at DESC);

-- The monthly budget existed as a number nobody read. Pricing per model is what
-- turns token counts into the spend that budget can be measured against.
UPDATE system_settings
SET value = '{"pricing":{},"currency":"USD"}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'ai.gateway';

-- The approval queue was already worked oldest first but had no ceiling, so
-- asking for decided requests returned every approval the platform had ever
-- made. It also measured nothing: a seller cannot sell while their listing sits
-- in review, and nothing said how long that had been true.
UPDATE system_settings
SET value = '{"approval_sla_hours":48}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'marketplace.policy';

CREATE INDEX IF NOT EXISTS approval_requests_state_age_idx ON approval_requests(state, created_at);

-- The risk queue now leads with the worst score inside a severity band, so the
-- index carries the score with the level.
CREATE INDEX IF NOT EXISTS risk_scores_level_score_idx ON risk_scores(level, score DESC, calculated_at DESC);

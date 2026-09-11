-- seller_profiles.score held the 1-5 review average. It becomes a 0-100 trust
-- score, so the raw rating moves to its own column and keeps its meaning.
ALTER TABLE seller_profiles ADD COLUMN IF NOT EXISTS rating numeric(3,2) NOT NULL DEFAULT 0;
ALTER TABLE seller_profiles ADD COLUMN IF NOT EXISTS rating_count integer NOT NULL DEFAULT 0;
ALTER TABLE seller_profiles ADD COLUMN IF NOT EXISTS scored_at timestamptz;

UPDATE seller_profiles sp
SET rating = LEAST(5, GREATEST(0, sp.score)),
    rating_count = COALESCE((SELECT count(*) FROM reviews r WHERE r.seller_id = sp.user_id), 0),
    score = 0
WHERE sp.scored_at IS NULL;

CREATE INDEX IF NOT EXISTS talent_scores_latest_idx ON talent_scores(talent_id, calculated_at DESC);
CREATE INDEX IF NOT EXISTS talents_ranking_idx ON talents(status, quality_score DESC NULLS LAST, published_at DESC);

INSERT INTO system_settings(key, value, description) VALUES
('seller.grading','{"enabled":true,"scan_interval_minutes":15,"scan_batch":500,"levels":[{"code":"ELITE","min_score":85,"min_orders":20},{"code":"PRO","min_score":70,"min_orders":8},{"code":"RISING","min_score":50,"min_orders":3},{"code":"NEW","min_score":0,"min_orders":0}]}'::jsonb,'판매자 신뢰 점수와 등급 기준')
ON CONFLICT (key) DO NOTHING;

UPDATE system_settings
SET value = '{"text_weight":0.5,"quality_weight":0.35,"recency_weight":0.15,"recency_half_life_days":30}'::jsonb || value,
    version = version + 1,
    updated_at = now()
WHERE key = 'search.policy';

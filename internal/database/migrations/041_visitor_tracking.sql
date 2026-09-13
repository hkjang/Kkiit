-- This file was 040 on the branch it came from and was merged next to another
-- 040. The runner records one version per number, so whichever of the two
-- sorted first was applied and the other was silently skipped. Both files are
-- idempotent, so re-running under this number completes either kind of
-- installation: the silent SSO columns are repeated here for the ones that
-- recorded 40 for this file.
ALTER TABLE oauth_states ADD COLUMN IF NOT EXISTS silent boolean NOT NULL DEFAULT false;
ALTER TABLE oauth_states ADD COLUMN IF NOT EXISTS return_to text NOT NULL DEFAULT '/';

-- Every internal service measures "what is actually used" on its own, and the
-- answers gather nowhere. This setting lets an administrator attach a visitor
-- tracking script from the console. It ships off: a fresh installation serves
-- exactly the pages and the policy it served before.
--
-- Momento is first and the default because it is the self-hosted collector,
-- the one choice where visit data never leaves the network. With momento_proxy
-- on, the browser talks only to this application, which forwards /momento/*
-- to the collector, so the content security policy names no outside origin.
INSERT INTO system_settings(key, value, description) VALUES
('analytics.tracking','{"enabled":false,"provider":"momento","momento_url":"","momento_site_id":"","momento_proxy":true,"measurement_id":"","matomo_url":"","matomo_site_id":"","custom_snippet":"","allowed_hosts":"","include_admin":false,"placement":"head"}'::jsonb,'방문 추적 스크립트')
ON CONFLICT (key) DO NOTHING;

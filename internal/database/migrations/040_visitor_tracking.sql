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

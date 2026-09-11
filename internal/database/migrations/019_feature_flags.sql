-- Flags were seeded off and never read, so enabling them did nothing and the
-- product behaved as if every feature were on. Enforcement starts now, which
-- means the shipped features must be flagged on or an upgrade would silently
-- switch them off.
UPDATE feature_flags SET enabled=true, updated_at=now()
WHERE key IN ('ai_matching','smart_quote','enterprise','agent_marketplace');

UPDATE feature_flags SET description='AI 상품 초안·요구 분석과 추천' WHERE key='ai_matching';
UPDATE feature_flags SET description='견적 요청과 견적 비교' WHERE key='smart_quote';
UPDATE feature_flags SET description='조직 계정과 예산' WHERE key='enterprise';
UPDATE feature_flags SET description='MCP를 통한 AI Agent 거래' WHERE key='agent_marketplace';

-- These named features do not exist yet. A switch that changes nothing is worse
-- than no switch, so they are removed until there is something to gate.
DELETE FROM feature_flags WHERE key IN ('subscription','milestone_payment');

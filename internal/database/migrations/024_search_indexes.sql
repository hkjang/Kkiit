-- Marketplace search ORs a full text match with a title substring match, and a
-- substring match with a leading wildcard cannot use a normal index. That one
-- branch forced the whole search to a sequential scan even though the tsvector
-- index was right there. A trigram index makes the substring branch reachable,
-- and the two branches then combine as a bitmap OR: measured on 100k published
-- talents, 57ms down to 3.2ms.
--
-- pg_trgm ships with the standard contrib package but an installation can be
-- built without it, and search must keep working there. The index is therefore
-- optional: if the extension cannot be created the search still returns the
-- same rows, just by scanning.
DO $$
BEGIN
	CREATE EXTENSION IF NOT EXISTS pg_trgm;
	CREATE INDEX IF NOT EXISTS talents_title_trgm_idx ON talents USING gin(title gin_trgm_ops);
EXCEPTION WHEN OTHERS THEN
	RAISE NOTICE 'pg_trgm을 사용할 수 없어 제목 부분 일치 인덱스를 건너뜁니다: %', SQLERRM;
END $$;

CREATE INDEX IF NOT EXISTS domain_events_type_idx ON domain_events(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS rfqs_open_idx ON rfqs(created_at DESC, id DESC) WHERE state = 'open';

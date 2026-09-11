-- A seller could see that nothing was selling but not why: no views meant the
-- listing was never found, while views without orders meant it was found and
-- rejected. Those two need opposite fixes, and the product told them apart for
-- nobody. The tracked_events table already existed and had never been written
-- to; it now carries listing views.

-- One view per viewer per listing per day. A seller refreshing their own page,
-- or a buyer comparing two listings back and forth, must not inflate the
-- number a seller is going to make decisions from. The day is computed in UTC
-- because only that expression is immutable enough to index.
CREATE UNIQUE INDEX IF NOT EXISTS tracked_events_talent_view_daily_idx
	ON tracked_events (resource_id, COALESCE(user_id::text, session_key), ((occurred_at AT TIME ZONE 'UTC')::date))
	WHERE event_name = 'talent_view';

CREATE INDEX IF NOT EXISTS tracked_events_resource_idx
	ON tracked_events (resource_id, occurred_at DESC) WHERE event_name = 'talent_view';

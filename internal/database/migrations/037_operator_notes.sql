-- The audit log records what an operator did, and only that. The judgement
-- behind it goes nowhere: "third late delivery from this seller, warned them,
-- watching" has no home, and neither does "looked into this, nothing wrong" —
-- which is the most expensive thing to lose, because the next operator starts
-- the same investigation from zero and reaches the same conclusion.
CREATE TABLE IF NOT EXISTS operator_notes (
	id uuid PRIMARY KEY,
	subject_type text NOT NULL CHECK (subject_type IN ('user', 'order', 'talent', 'dispute')),
	subject_id uuid NOT NULL,
	author_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	body text NOT NULL,
	-- A pinned note is the standing context someone needs before they act,
	-- rather than the running commentary underneath it.
	pinned boolean NOT NULL DEFAULT false,
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS operator_notes_subject_idx
	ON operator_notes(subject_type, subject_id, pinned DESC, created_at DESC);

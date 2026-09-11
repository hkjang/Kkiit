-- The report queue now shows whether a case has already been looked at, which
-- only means anything if a note can be attached to a report in the first place.
ALTER TABLE operator_notes DROP CONSTRAINT IF EXISTS operator_notes_subject_type_check;
ALTER TABLE operator_notes ADD CONSTRAINT operator_notes_subject_type_check
	CHECK (subject_type IN ('user', 'order', 'talent', 'dispute', 'report'));

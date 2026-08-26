-- `integration` did not exist before this migration. Map rows back to the
-- closest pre-migration kind before restoring the narrower constraint.
UPDATE research_discussion
SET kind = 'assimilation'
WHERE kind = 'integration';

ALTER TABLE research_discussion
  DROP CONSTRAINT research_discussion_kind_check;

ALTER TABLE research_discussion
  ADD CONSTRAINT research_discussion_kind_check
  CHECK (kind IN ('match', 'promotion', 'assimilation', 'dispute'));

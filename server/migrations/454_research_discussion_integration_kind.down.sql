ALTER TABLE research_discussion
  DROP CONSTRAINT research_discussion_kind_check;

ALTER TABLE research_discussion
  ADD CONSTRAINT research_discussion_kind_check
  CHECK (kind IN ('match', 'promotion', 'assimilation', 'dispute'));

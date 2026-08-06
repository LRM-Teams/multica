ALTER TABLE research_message
  DROP COLUMN IF EXISTS meta,
  DROP COLUMN IF EXISTS card_kind;

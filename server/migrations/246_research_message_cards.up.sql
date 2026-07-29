-- Process cards in research session chat (kickoff, tool ops, stage gates).

ALTER TABLE research_message
  ADD COLUMN IF NOT EXISTS card_kind TEXT NOT NULL DEFAULT 'chat'
    CHECK (card_kind IN ('chat', 'process')),
  ADD COLUMN IF NOT EXISTS meta JSONB NOT NULL DEFAULT '{}'::jsonb;

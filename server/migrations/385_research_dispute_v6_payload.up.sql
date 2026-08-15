-- Preserve the frozen V6 dispute position object losslessly. V6 deliberately
-- treats position contents as an extensible object, so indexed columns remain
-- best-effort projections and cannot require a Claim for non-Claim subjects.

ALTER TABLE research_dispute
  ADD COLUMN client_key TEXT;

UPDATE research_dispute SET client_key = 'legacy:' || id::text WHERE client_key IS NULL;

ALTER TABLE research_dispute
  ALTER COLUMN client_key SET NOT NULL,
  ADD CONSTRAINT research_dispute_client_key_format
    CHECK (client_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$'),
  ADD CONSTRAINT research_dispute_client_key_unique
    UNIQUE (workspace_id, session_id, client_key);

ALTER TABLE research_dispute_position
  ADD COLUMN position_payload JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(position_payload) = 'object'),
  DROP CONSTRAINT IF EXISTS research_dispute_position_claim_ids_check,
  ADD CONSTRAINT research_dispute_position_claim_ids_check
    CHECK (jsonb_typeof(claim_ids) = 'array');

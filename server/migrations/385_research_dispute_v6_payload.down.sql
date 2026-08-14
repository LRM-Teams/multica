ALTER TABLE research_dispute_position
  DROP CONSTRAINT IF EXISTS research_dispute_position_claim_ids_check,
  ADD CONSTRAINT research_dispute_position_claim_ids_check
    CHECK (jsonb_typeof(claim_ids) = 'array' AND jsonb_array_length(claim_ids) > 0),
  DROP COLUMN IF EXISTS position_payload;

ALTER TABLE research_dispute
  DROP CONSTRAINT IF EXISTS research_dispute_client_key_unique,
  DROP CONSTRAINT IF EXISTS research_dispute_client_key_format,
  DROP COLUMN IF EXISTS client_key;

-- Chapter E2b-write prerequisite: persist the V6 client keys used to resolve
-- batch-local Inquiry Graph references and idempotent retries.

ALTER TABLE research_hypothesis ADD COLUMN client_key TEXT;
ALTER TABLE research_branch ADD COLUMN client_key TEXT;
ALTER TABLE research_insight ADD COLUMN client_key TEXT;
ALTER TABLE research_inquiry_edge ADD COLUMN client_key TEXT;

UPDATE research_hypothesis SET client_key = 'legacy:' || id::text WHERE client_key IS NULL;
UPDATE research_branch SET client_key = 'legacy:' || id::text WHERE client_key IS NULL;
UPDATE research_insight SET client_key = 'legacy:' || id::text WHERE client_key IS NULL;
UPDATE research_inquiry_edge SET client_key = 'legacy:' || id::text WHERE client_key IS NULL;

ALTER TABLE research_hypothesis
  ALTER COLUMN client_key SET NOT NULL,
  ADD CONSTRAINT research_hypothesis_client_key_format
    CHECK (client_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$'),
  ADD CONSTRAINT research_hypothesis_client_key_unique
    UNIQUE (workspace_id, session_id, client_key);
ALTER TABLE research_branch
  ALTER COLUMN client_key SET NOT NULL,
  ADD CONSTRAINT research_branch_client_key_format
    CHECK (client_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$'),
  ADD CONSTRAINT research_branch_client_key_unique
    UNIQUE (workspace_id, session_id, client_key);
ALTER TABLE research_insight
  ALTER COLUMN client_key SET NOT NULL,
  ADD CONSTRAINT research_insight_client_key_format
    CHECK (client_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$'),
  ADD CONSTRAINT research_insight_client_key_unique
    UNIQUE (workspace_id, session_id, client_key);
ALTER TABLE research_inquiry_edge
  ALTER COLUMN client_key SET NOT NULL,
  ADD CONSTRAINT research_inquiry_edge_client_key_format
    CHECK (client_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$'),
  ADD CONSTRAINT research_inquiry_edge_client_key_unique
    UNIQUE (workspace_id, session_id, client_key);

-- Align persisted manifest omission reasons with the Chapter D policy vocabulary.
CREATE OR REPLACE FUNCTION research_artifact_context_omission_reason_allowed(reason TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT reason IN (
    'access_denied', 'stale', 'superseded', 'duplicate', 'token_budget', 'irrelevant',
    'insufficient_clearance', 'evaluation_compartment', 'lifecycle', 'policy_denied'
  );
$$;

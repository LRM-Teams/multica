-- Activation evidence is an audit ledger. Existing rows must never be
-- rewritten or removed after review.
CREATE OR REPLACE FUNCTION research_v6_activation_evidence_append_only_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'research_v6_activation_evidence is append-only'
    USING ERRCODE = '55000';
END;
$$;

DROP TRIGGER IF EXISTS research_v6_activation_evidence_append_only
  ON research_v6_activation_evidence;
CREATE TRIGGER research_v6_activation_evidence_append_only
  BEFORE UPDATE OR DELETE ON research_v6_activation_evidence
  FOR EACH ROW EXECUTE FUNCTION research_v6_activation_evidence_append_only_fn();

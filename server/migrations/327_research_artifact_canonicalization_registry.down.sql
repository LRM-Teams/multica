-- Roll back Chapter D1j canonicalization registry.

ALTER TABLE research_artifact_version
  DROP CONSTRAINT IF EXISTS research_artifact_version_schema_family_check;
DROP FUNCTION IF EXISTS research_artifact_schema_family_allowed(TEXT, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_canonicalization_version_allowed(TEXT);

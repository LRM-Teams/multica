-- Chapter D1j: canonicalization and schema-family registry (design §4.3, §16 D1).

CREATE OR REPLACE FUNCTION research_artifact_canonicalization_version_allowed(version TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT version IN ('research-artifact-c14n-v1');
$$;

CREATE OR REPLACE FUNCTION research_artifact_schema_family_allowed(
  p_schema_name TEXT,
  p_schema_version TEXT,
  p_canonicalization_version TEXT
)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT research_artifact_entity_kind_allowed(p_schema_name)
    AND p_schema_version = 'legacy-v1'
    AND research_artifact_canonicalization_version_allowed(p_canonicalization_version);
$$;

ALTER TABLE research_artifact_version
  ADD CONSTRAINT research_artifact_version_schema_family_check
  CHECK (research_artifact_schema_family_allowed(schema_name, schema_version, canonicalization_version));

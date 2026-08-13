ALTER TABLE research_artifact_context_entry
  DROP CONSTRAINT IF EXISTS research_artifact_context_entry_selection_output_count_check,
  DROP CONSTRAINT IF EXISTS research_artifact_context_entry_selection_input_count_check,
  DROP CONSTRAINT IF EXISTS research_artifact_context_entry_selection_version_count_check,
  DROP CONSTRAINT IF EXISTS research_artifact_context_entry_selection_provenance_check,
  DROP CONSTRAINT IF EXISTS research_artifact_context_entry_selection_lifecycle_check,
  DROP COLUMN IF EXISTS selection_output_reference_count,
  DROP COLUMN IF EXISTS selection_input_reference_count,
  DROP COLUMN IF EXISTS selection_version_count,
  DROP COLUMN IF EXISTS selection_provenance_completeness,
  DROP COLUMN IF EXISTS selection_lifecycle_status;

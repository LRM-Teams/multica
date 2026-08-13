ALTER TABLE research_artifact_context_entry
  ADD COLUMN selection_lifecycle_status TEXT,
  ADD COLUMN selection_provenance_completeness TEXT,
  ADD COLUMN selection_version_count INTEGER,
  ADD COLUMN selection_input_reference_count INTEGER,
  ADD COLUMN selection_output_reference_count INTEGER;

ALTER TABLE research_artifact_context_entry
  ADD CONSTRAINT research_artifact_context_entry_selection_lifecycle_check
    CHECK (selection_lifecycle_status IS NULL OR research_artifact_lifecycle_status_allowed(selection_lifecycle_status)),
  ADD CONSTRAINT research_artifact_context_entry_selection_provenance_check
    CHECK (selection_provenance_completeness IS NULL OR research_artifact_provenance_completeness_allowed(selection_provenance_completeness)),
  ADD CONSTRAINT research_artifact_context_entry_selection_version_count_check
    CHECK (selection_version_count IS NULL OR selection_version_count >= 1),
  ADD CONSTRAINT research_artifact_context_entry_selection_input_count_check
    CHECK (selection_input_reference_count IS NULL OR selection_input_reference_count >= 0),
  ADD CONSTRAINT research_artifact_context_entry_selection_output_count_check
    CHECK (selection_output_reference_count IS NULL OR selection_output_reference_count >= 0);

COMMENT ON COLUMN research_artifact_context_entry.selection_lifecycle_status IS
  'Passport lifecycle observed under lock when this Manifest selected the version; NULL only for pre-354 history.';
COMMENT ON COLUMN research_artifact_context_entry.selection_provenance_completeness IS
  'Passport provenance observed under lock when this Manifest selected the version; NULL only for pre-354 history.';
COMMENT ON COLUMN research_artifact_context_entry.selection_version_count IS
  'Version count observed when selected; NULL only for pre-354 history.';
COMMENT ON COLUMN research_artifact_context_entry.selection_input_reference_count IS
  'Input-reference count observed when selected; NULL only for pre-354 history.';
COMMENT ON COLUMN research_artifact_context_entry.selection_output_reference_count IS
  'Output-reference count observed when selected; NULL only for pre-354 history.';

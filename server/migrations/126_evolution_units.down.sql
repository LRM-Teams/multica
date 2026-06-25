DROP TABLE IF EXISTS shared_evolution_unit_file;
DROP TABLE IF EXISTS evolution_unit_submission_file;
ALTER TABLE IF EXISTS shared_evolution_unit DROP CONSTRAINT IF EXISTS shared_evolution_unit_current_version_fkey;
DROP TABLE IF EXISTS shared_evolution_unit_version;
ALTER TABLE IF EXISTS evolution_unit_submission DROP CONSTRAINT IF EXISTS evolution_unit_submission_promoted_unit_fkey;
DROP TABLE IF EXISTS shared_evolution_unit;
DROP TABLE IF EXISTS evolution_unit_submission;

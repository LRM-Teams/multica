ALTER TABLE research_report
  DROP COLUMN IF EXISTS design_dossier,
  DROP COLUMN IF EXISTS direction_coverage,
  DROP COLUMN IF EXISTS maturity;

DROP INDEX IF EXISTS research_v6_team_one_reporter_idx;
ALTER TABLE research_team_membership DROP COLUMN IF EXISTS role;

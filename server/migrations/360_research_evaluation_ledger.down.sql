DROP TRIGGER IF EXISTS research_evaluation_report_immutable ON research_evaluation_report;
DROP TRIGGER IF EXISTS research_evaluation_grade_immutable ON research_evaluation_grade;
DROP TRIGGER IF EXISTS research_evaluation_trial_immutable ON research_evaluation_trial;
DROP FUNCTION IF EXISTS research_evaluation_immutable_row_fn();

DROP TABLE IF EXISTS research_evaluation_report;
DROP TABLE IF EXISTS research_evaluation_grade;
DROP TABLE IF EXISTS research_evaluation_trial;
DROP TABLE IF EXISTS research_evaluation_run;

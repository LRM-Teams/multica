DROP TRIGGER IF EXISTS research_task_inquiry_target_immutable ON research_task_inquiry_target;
DROP FUNCTION IF EXISTS research_task_inquiry_target_append_only();
DROP TRIGGER IF EXISTS research_task_inquiry_target_insert_guard ON research_task_inquiry_target;
DROP FUNCTION IF EXISTS research_task_inquiry_target_guard();
DROP TABLE IF EXISTS research_task_inquiry_target;

DROP TRIGGER IF EXISTS research_dispatch_outbox_status_transition_guard ON research_dispatch_outbox;
DROP TRIGGER IF EXISTS research_task_attempt_status_transition_guard ON research_task_attempt;
DROP TRIGGER IF EXISTS research_task_status_transition_guard ON research_task;

DROP FUNCTION IF EXISTS enforce_research_dispatch_status_transition();
DROP FUNCTION IF EXISTS enforce_research_attempt_status_transition();
DROP FUNCTION IF EXISTS enforce_research_task_status_transition();
DROP FUNCTION IF EXISTS research_dispatch_status_transition_allowed(TEXT, TEXT);
DROP FUNCTION IF EXISTS research_attempt_status_transition_allowed(TEXT, TEXT);
DROP FUNCTION IF EXISTS research_task_status_transition_allowed(TEXT, TEXT);

DROP TRIGGER IF EXISTS research_v6_backing_task_status_sync ON research_work_item;
DROP FUNCTION IF EXISTS research_v6_backing_task_status_sync_fn();

CREATE OR REPLACE FUNCTION enforce_research_task_status_transition()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_task_status_transition_allowed(OLD.status, NEW.status) THEN
    RAISE EXCEPTION 'illegal research_task status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514', CONSTRAINT = 'research_task_status_transition_check';
  END IF;
  RETURN NEW;
END
$$;

DROP FUNCTION IF EXISTS research_v6_task_status_for_work_item(TEXT);

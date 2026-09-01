-- Restore the single-shape task contract. Channel-message-shaped recalls
-- cannot satisfy the agent_inbox_event foreign key, so they are removed
-- first; their ledger history is not portable back to the task shape.

DELETE FROM graph_memory_recall WHERE task_shape = 'channel_message';

ALTER TABLE graph_memory_recall
  ADD CONSTRAINT graph_memory_recall_task_id_fkey
  FOREIGN KEY (task_id) REFERENCES agent_inbox_event(id) ON DELETE CASCADE;

ALTER TABLE graph_memory_recall DROP COLUMN task_shape;

CREATE OR REPLACE FUNCTION graph_memory_recall_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws     uuid;
  v_daemon text;
BEGIN
  SELECT workspace_id INTO v_ws FROM agent_inbox_event WHERE id = NEW.task_id;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_recall: task % is not in workspace %', NEW.task_id, NEW.workspace_id;
  END IF;
  IF NEW.runtime_id IS NOT NULL THEN
    SELECT workspace_id, daemon_id INTO v_ws, v_daemon FROM agent_runtime WHERE id = NEW.runtime_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id OR v_daemon IS DISTINCT FROM NEW.daemon_id THEN
      RAISE EXCEPTION 'graph_memory_recall: runtime % does not match workspace/daemon', NEW.runtime_id;
    END IF;
  END IF;
  IF NEW.graph_kind = 'project' THEN
    SELECT workspace_id INTO v_ws FROM project WHERE id = NEW.graph_owner_id;
  ELSE
    SELECT workspace_id INTO v_ws FROM channel WHERE id = NEW.graph_owner_id;
  END IF;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_recall: % owner % is not in workspace %', NEW.graph_kind, NEW.graph_owner_id, NEW.workspace_id;
  END IF;
  RETURN NEW;
END;
$$;

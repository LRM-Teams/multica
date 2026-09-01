-- Dual-shape recall task identity (recovery spec A1): the resident Message
-- path stamps a channel_message id into Task.ID (message_runtime.go
-- residentMessageMemoryTask), and #2295 removed the inbox-event projection
-- for channel turns, so the storage layer must accept both canonical task
-- shapes. The ledger records which shape it actually resolved so daily
-- reason-distribution monitoring can split the population.

ALTER TABLE graph_memory_recall
  ADD COLUMN task_shape text NOT NULL DEFAULT 'agent_inbox_event'
  CHECK (task_shape IN ('agent_inbox_event', 'channel_message'));

-- A polymorphic task id cannot keep a single-table foreign key; the identity
-- trigger below keeps the workspace-ownership guarantee for both shapes.
ALTER TABLE graph_memory_recall
  DROP CONSTRAINT graph_memory_recall_task_id_fkey;

CREATE OR REPLACE FUNCTION graph_memory_recall_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws     uuid;
  v_daemon text;
BEGIN
  -- The task id must resolve, inside this workspace, in the table its
  -- recorded task_shape names (spec §16 tenant consistency, recovery spec A1).
  IF NEW.task_shape = 'channel_message' THEN
    SELECT workspace_id INTO v_ws FROM channel_message WHERE id = NEW.task_id;
  ELSE
    SELECT workspace_id INTO v_ws FROM agent_inbox_event WHERE id = NEW.task_id;
  END IF;
  IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_recall: task % (% shape) is not in workspace %', NEW.task_id, NEW.task_shape, NEW.workspace_id;
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

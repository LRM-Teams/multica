-- Immutable multimodal source-layer registry (spec §10, brief D16/D17).
-- PG holds identity, scope, blob metadata and the per-graph journal seq;
-- node bodies and has_attachment edges live in the graph's shared source
-- store. Ingest writers land in a later task; this migration only creates
-- the schema. Identity triggers reject cross-kind/foreign references even
-- when application validation is bypassed (spec §16).

CREATE TABLE IF NOT EXISTS graph_memory_source (
  id               uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id     uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_kind       text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id   uuid        NOT NULL,
  source_kind      text        NOT NULL CHECK (source_kind IN ('segment', 'file')),
  source_node_id   text        NOT NULL,
  attachment_id    uuid,
  blob_sha256      text        NOT NULL DEFAULT '',
  mime             text        NOT NULL DEFAULT '',
  size_bytes       bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  visibility       text        NOT NULL DEFAULT 'project' CHECK (visibility IN ('project', 'channel')),
  channel_id       uuid,
  agent_id         uuid,
  task_id          uuid,
  source_seq       bigint      NOT NULL CHECK (source_seq >= 1),
  status           text        NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'quarantined')),
  schema_version   integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (graph_kind, graph_owner_id, source_kind, source_node_id),
  CHECK (
    (source_kind = 'segment' AND attachment_id IS NULL) OR
    (source_kind = 'file' AND attachment_id IS NOT NULL)
  )
);
CREATE UNIQUE INDEX IF NOT EXISTS graph_memory_source_scope_attachment
  ON graph_memory_source (graph_kind, graph_owner_id, attachment_id)
  WHERE attachment_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS graph_memory_source_ws_created
  ON graph_memory_source (workspace_id, created_at DESC);

-- Channel graphs require channel_id (the graph owner); project graphs
-- must not carry a channel binding. graph_owner_id must exist in the
-- table matching graph_kind inside the same workspace. Optional
-- agent/task refs must also belong to that workspace.
CREATE OR REPLACE FUNCTION graph_memory_source_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws uuid;
BEGIN
  IF NEW.graph_kind = 'project' THEN
    SELECT workspace_id INTO v_ws FROM project WHERE id = NEW.graph_owner_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_source: project owner % is not in workspace %', NEW.graph_owner_id, NEW.workspace_id;
    END IF;
    IF NEW.channel_id IS NOT NULL THEN
      RAISE EXCEPTION 'graph_memory_source: project graph must have channel_id NULL';
    END IF;
  ELSE
    SELECT workspace_id INTO v_ws FROM channel WHERE id = NEW.graph_owner_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_source: channel owner % is not in workspace %', NEW.graph_owner_id, NEW.workspace_id;
    END IF;
    IF NEW.channel_id IS NULL THEN
      RAISE EXCEPTION 'graph_memory_source: channel graph requires channel_id';
    END IF;
    IF NEW.channel_id <> NEW.graph_owner_id THEN
      RAISE EXCEPTION 'graph_memory_source: channel_id does not match graph_owner_id';
    END IF;
  END IF;

  IF NEW.channel_id IS NOT NULL THEN
    SELECT workspace_id INTO v_ws FROM channel WHERE id = NEW.channel_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_source: channel % is not in workspace %', NEW.channel_id, NEW.workspace_id;
    END IF;
  END IF;

  IF NEW.agent_id IS NOT NULL THEN
    SELECT workspace_id INTO v_ws FROM agent WHERE id = NEW.agent_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_source: agent % is not in workspace %', NEW.agent_id, NEW.workspace_id;
    END IF;
  END IF;

  IF NEW.task_id IS NOT NULL THEN
    SELECT workspace_id INTO v_ws FROM agent_inbox_event WHERE id = NEW.task_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_source: task % is not in workspace %', NEW.task_id, NEW.workspace_id;
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_source_identity
  BEFORE INSERT OR UPDATE ON graph_memory_source
  FOR EACH ROW EXECUTE FUNCTION graph_memory_source_validate_identity();

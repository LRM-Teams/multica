-- Persistent graph-scoped necessary-information catalog (spec §8/§16).
-- Items are stable identities deduped by normalized statement hash; node
-- groups are OR-equivalence sets; recall links are the per-query required
-- set (catalog membership alone does not make an item required). Identity
-- triggers reject foreign owners and cross-scope recall links even when
-- application validation is bypassed.

CREATE TABLE IF NOT EXISTS graph_memory_info_item (
  id              uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  graph_kind      text        NOT NULL CHECK (graph_kind IN ('project', 'channel')),
  graph_owner_id  uuid        NOT NULL,
  statement       text        NOT NULL,
  statement_hash  text        NOT NULL,
  rationale       text        NOT NULL DEFAULT '',
  source_refs     jsonb       NOT NULL DEFAULT '[]'::jsonb,
  status          text        NOT NULL DEFAULT 'authoritative'
    CHECK (status IN ('authoritative', 'incomplete', 'judge_failed', 'legacy_non_authoritative')),
  schema_version  integer     NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (graph_kind, graph_owner_id, statement_hash)
);
CREATE INDEX IF NOT EXISTS graph_memory_info_item_ws_created
  ON graph_memory_info_item (workspace_id, created_at DESC);

CREATE TABLE IF NOT EXISTS graph_memory_info_item_node (
  item_id    uuid        NOT NULL REFERENCES graph_memory_info_item(id) ON DELETE CASCADE,
  node_id    text        NOT NULL,
  added_by   text        NOT NULL DEFAULT 'dive' CHECK (added_by IN ('explore', 'dive', 'migration')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (item_id, node_id)
);

CREATE TABLE IF NOT EXISTS graph_memory_recall_info_item (
  recall_id  uuid        NOT NULL REFERENCES graph_memory_recall(id) ON DELETE CASCADE,
  item_id    uuid        NOT NULL REFERENCES graph_memory_info_item(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (recall_id, item_id)
);

-- Item graph owner must exist in the table matching graph_kind inside the
-- same workspace (mirrors graph_memory_source_validate_identity).
CREATE OR REPLACE FUNCTION graph_memory_info_item_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws uuid;
BEGIN
  IF NEW.graph_kind = 'project' THEN
    SELECT workspace_id INTO v_ws FROM project WHERE id = NEW.graph_owner_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_info_item: project owner % is not in workspace %', NEW.graph_owner_id, NEW.workspace_id;
    END IF;
  ELSE
    SELECT workspace_id INTO v_ws FROM channel WHERE id = NEW.graph_owner_id;
    IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
      RAISE EXCEPTION 'graph_memory_info_item: channel owner % is not in workspace %', NEW.graph_owner_id, NEW.workspace_id;
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_info_item_identity
  BEFORE INSERT OR UPDATE ON graph_memory_info_item
  FOR EACH ROW EXECUTE FUNCTION graph_memory_info_item_validate_identity();

-- A recall↔item link must share workspace and (graph_kind, graph_owner_id).
CREATE OR REPLACE FUNCTION graph_memory_recall_info_item_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws    uuid;
  v_kind  text;
  v_owner uuid;
  i_ws    uuid;
  i_kind  text;
  i_owner uuid;
BEGIN
  SELECT workspace_id, graph_kind, graph_owner_id
    INTO v_ws, v_kind, v_owner
    FROM graph_memory_recall WHERE id = NEW.recall_id;
  IF v_ws IS NULL THEN
    RAISE EXCEPTION 'graph_memory_recall_info_item: recall % does not exist', NEW.recall_id;
  END IF;

  SELECT workspace_id, graph_kind, graph_owner_id
    INTO i_ws, i_kind, i_owner
    FROM graph_memory_info_item WHERE id = NEW.item_id;
  IF i_ws IS NULL THEN
    RAISE EXCEPTION 'graph_memory_recall_info_item: item % does not exist', NEW.item_id;
  END IF;

  IF v_ws <> i_ws THEN
    RAISE EXCEPTION 'graph_memory_recall_info_item: recall % workspace does not match item %', NEW.recall_id, NEW.item_id;
  END IF;
  IF v_kind <> i_kind OR v_owner <> i_owner THEN
    RAISE EXCEPTION 'graph_memory_recall_info_item: recall % graph identity does not match item %', NEW.recall_id, NEW.item_id;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_recall_info_item_identity
  BEFORE INSERT OR UPDATE ON graph_memory_recall_info_item
  FOR EACH ROW EXECUTE FUNCTION graph_memory_recall_info_item_validate_identity();

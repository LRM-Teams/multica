-- Attachment/blob decoupling (spec §15, A28/D32). Physical bytes are
-- identified by storage_url; attachment rows and graph sources retain them
-- through graph_memory_blob_ref. GC collects only active blobs with zero
-- open refs, under an advisory lock + recheck.

CREATE TABLE IF NOT EXISTS graph_memory_blob (
  id            uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  storage_url   text        NOT NULL,
  blob_sha256   text        NOT NULL DEFAULT '',
  size_bytes    bigint      NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  status        text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  retired_at    timestamptz,
  UNIQUE (workspace_id, storage_url)
);

CREATE TABLE IF NOT EXISTS graph_memory_blob_ref (
  id            uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  blob_id       uuid        NOT NULL REFERENCES graph_memory_blob(id) ON DELETE CASCADE,
  ref_kind      text        NOT NULL CHECK (ref_kind IN ('attachment', 'graph_source', 'graph_version')),
  ref_id        uuid        NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  released_at   timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS graph_memory_blob_ref_open_unique
  ON graph_memory_blob_ref (blob_id, ref_kind, ref_id)
  WHERE released_at IS NULL;
CREATE INDEX IF NOT EXISTS graph_memory_blob_ref_open_blob
  ON graph_memory_blob_ref (blob_id)
  WHERE released_at IS NULL;

-- Ref workspace must equal its blob's workspace. graph_source / attachment
-- targets must exist in that workspace. graph_version has no FK target
-- (versions are files) but still requires the workspace match. Target
-- existence is skipped when releasing (released_at IS NOT NULL) so a
-- referrer can drop its row before the ref is released.
CREATE OR REPLACE FUNCTION graph_memory_blob_ref_validate_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_ws uuid;
BEGIN
  SELECT workspace_id INTO v_ws FROM graph_memory_blob WHERE id = NEW.blob_id;
  IF v_ws IS NULL THEN
    RAISE EXCEPTION 'graph_memory_blob_ref: blob % does not exist', NEW.blob_id;
  END IF;
  IF v_ws <> NEW.workspace_id THEN
    RAISE EXCEPTION 'graph_memory_blob_ref: blob % is not in workspace %', NEW.blob_id, NEW.workspace_id;
  END IF;

  IF TG_OP = 'INSERT' OR NEW.released_at IS NULL THEN
    IF NEW.ref_kind = 'graph_source' THEN
      SELECT workspace_id INTO v_ws FROM graph_memory_source WHERE id = NEW.ref_id;
      IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
        RAISE EXCEPTION 'graph_memory_blob_ref: graph_source % is not in workspace %', NEW.ref_id, NEW.workspace_id;
      END IF;
    ELSIF NEW.ref_kind = 'attachment' THEN
      SELECT workspace_id INTO v_ws FROM attachment WHERE id = NEW.ref_id;
      IF v_ws IS NULL OR v_ws <> NEW.workspace_id THEN
        RAISE EXCEPTION 'graph_memory_blob_ref: attachment % is not in workspace %', NEW.ref_id, NEW.workspace_id;
      END IF;
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER graph_memory_blob_ref_identity
  BEFORE INSERT OR UPDATE ON graph_memory_blob_ref
  FOR EACH ROW EXECUTE FUNCTION graph_memory_blob_ref_validate_identity();

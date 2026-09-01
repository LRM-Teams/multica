-- Research→memory unification export state (unification spec §4.2): the
-- research→memory exporter polls the research ledgers and writes the
-- workspace research-scope memory graph. This migration stores its
-- per-workspace watermark and per-source idempotency keys so an export run
-- is resumable, replay-safe, and cheap to re-run.
--
-- Cursor design: research_graph_node rows carry no monotonic id, so the
-- watermark is the max updated_at already exported and polling uses
-- updated_at >= cursor. Rows landing on an already-exported timestamp are
-- re-fetched and skipped by the export keys (same content hash), never
-- missed. V6 insight/result rows are re-scanned per poll within the batch
-- budget and change-detected by content hash for the same reason.

CREATE TABLE research_graph_export_state (
  workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
  node_cursor TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE research_graph_export_key (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  source_kind TEXT NOT NULL CHECK (source_kind IN ('research_node', 'research_insight', 'research_result')),
  source_id UUID NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  memory_node_id TEXT NOT NULL,
  exported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, source_kind, source_id)
);

CREATE INDEX research_graph_export_key_memory_idx ON research_graph_export_key (workspace_id, memory_node_id);

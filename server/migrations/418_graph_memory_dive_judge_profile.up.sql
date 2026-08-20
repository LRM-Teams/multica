-- Dive-Judge-era workspace profile tunables (spec §2, brief D2/D25).
-- explore_agents (migration 346, default 4) is the saved per-recall TTT
-- concurrency K; ttt_enabled gates whether K>1 is effective. Existing rows
-- migrate with TTT disabled. CHECK bounds are absolute storage sanity
-- ceilings — both-sided so NaN/+Inf can never become authoritative
-- (spec §16 numeric fail-closed). Server env ceilings enforced at the
-- service layer sit within these bounds.
ALTER TABLE graph_memory_profile
  ADD COLUMN IF NOT EXISTS ttt_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS explore_nodes_per_expansion integer NOT NULL DEFAULT 1
    CHECK (explore_nodes_per_expansion BETWEEN 1 AND 16),
  ADD COLUMN IF NOT EXISTS max_hierarchy_fanout integer NOT NULL DEFAULT 8
    CHECK (max_hierarchy_fanout BETWEEN 1 AND 64),
  ADD COLUMN IF NOT EXISTS max_relation_edges_per_node integer NOT NULL DEFAULT 8
    CHECK (max_relation_edges_per_node BETWEEN 1 AND 64),
  ADD COLUMN IF NOT EXISTS dive_max_rounds integer NOT NULL DEFAULT 6
    CHECK (dive_max_rounds BETWEEN 1 AND 64),
  ADD COLUMN IF NOT EXISTS dive_max_viewed_nodes integer NOT NULL DEFAULT 24
    CHECK (dive_max_viewed_nodes BETWEEN 1 AND 1024),
  ADD COLUMN IF NOT EXISTS dive_max_source_files integer NOT NULL DEFAULT 4
    CHECK (dive_max_source_files BETWEEN 1 AND 64),
  ADD COLUMN IF NOT EXISTS dive_timeout_seconds integer NOT NULL DEFAULT 600
    CHECK (dive_timeout_seconds BETWEEN 30 AND 7200),
  ADD COLUMN IF NOT EXISTS w_round double precision NOT NULL DEFAULT 0.1
    CHECK (w_round >= 0.0 AND w_round <= 10.0),
  ADD COLUMN IF NOT EXISTS source_max_file_bytes bigint NOT NULL DEFAULT 20971520
    CHECK (source_max_file_bytes BETWEEN 1024 AND 4294967296),
  ADD COLUMN IF NOT EXISTS source_max_total_bytes bigint NOT NULL DEFAULT 52428800
    CHECK (source_max_total_bytes BETWEEN 1024 AND 17179869184),
  ADD COLUMN IF NOT EXISTS source_max_pdf_pages integer NOT NULL DEFAULT 50
    CHECK (source_max_pdf_pages BETWEEN 1 AND 5000),
  ADD COLUMN IF NOT EXISTS source_max_av_seconds integer NOT NULL DEFAULT 600
    CHECK (source_max_av_seconds BETWEEN 1 AND 14400),
  ADD COLUMN IF NOT EXISTS source_max_image_megapixels integer NOT NULL DEFAULT 40
    CHECK (source_max_image_megapixels BETWEEN 1 AND 1000),
  ADD COLUMN IF NOT EXISTS dive_model text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS dive_provider text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS config_version bigint NOT NULL DEFAULT 1
    CHECK (config_version >= 1),
  ADD COLUMN IF NOT EXISTS schema_version integer NOT NULL DEFAULT 1
    CHECK (schema_version >= 1);

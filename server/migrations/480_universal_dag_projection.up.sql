-- 465_universal_dag_projection: canonical Universal mapping for the Mixed-RL
-- frozen run projection. The frozen snapshot stops being an independently
-- writable fact source: every projected row records the canonical Universal
-- Segment/Edge it was derived from, and rows the canonical store cannot
-- confirm deterministically are recorded for explicit audit instead of being
-- guessed onto a mapping.

CREATE TABLE interaction_dag_projection_backfill_audit (
  audit_id bigserial PRIMARY KEY,
  table_name text NOT NULL CHECK (
    table_name IN ('interaction_dag_run_segment', 'interaction_dag_causal_edge')
  ),
  row_pk text NOT NULL CHECK (length(btrim(row_pk)) > 0),
  reason text NOT NULL CHECK (length(btrim(reason)) > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE interaction_dag_run_segment
  ADD COLUMN universal_segment_id text
    REFERENCES interaction_dag_segment(segment_id) ON DELETE CASCADE;

CREATE UNIQUE INDEX interaction_dag_run_segment_universal_uidx
  ON interaction_dag_run_segment (run_id, universal_segment_id)
  WHERE universal_segment_id IS NOT NULL;

ALTER TABLE interaction_dag_causal_edge
  ADD COLUMN universal_edge_id bigint
    REFERENCES interaction_dag_edge(id) ON DELETE CASCADE;

CREATE UNIQUE INDEX interaction_dag_causal_edge_universal_uidx
  ON interaction_dag_causal_edge (run_id, universal_edge_id)
  WHERE universal_edge_id IS NOT NULL;

-- Backfill, deterministic matches only. A frozen message/reaction row whose
-- canonical action identifies exactly one non-legacy canonical Segment of the
-- same run and kind adopts that mapping. Ambiguous pairs, unmatched rows, and
-- terminal buckets stay unmapped and are audited below.
WITH candidates AS (
  SELECT rs.segment_id AS frozen_id, us.segment_id AS universal_id,
         count(*) OVER (PARTITION BY rs.segment_id) AS frozen_matches,
         count(*) OVER (PARTITION BY us.segment_id) AS universal_matches
  FROM interaction_dag_run_segment rs
  JOIN interaction_dag_segment us
    ON us.run_id = rs.run_id
   AND us.canonical_action_id = rs.canonical_action_id
   AND us.close_action_kind = rs.kind
   AND us.content_status <> 'legacy_unverified'
  WHERE rs.canonical_action_id IS NOT NULL
    AND rs.universal_segment_id IS NULL
)
UPDATE interaction_dag_run_segment rs
SET universal_segment_id = candidates.universal_id
FROM candidates
WHERE rs.segment_id = candidates.frozen_id
  AND candidates.frozen_matches = 1
  AND candidates.universal_matches = 1;

INSERT INTO interaction_dag_projection_backfill_audit (table_name, row_pk, reason)
SELECT 'interaction_dag_run_segment', rs.segment_id,
       'no unique canonical segment for canonical action'
FROM interaction_dag_run_segment rs
WHERE rs.universal_segment_id IS NULL
  AND rs.canonical_action_id IS NOT NULL;

INSERT INTO interaction_dag_projection_backfill_audit (table_name, row_pk, reason)
SELECT 'interaction_dag_run_segment', rs.segment_id,
       'terminal bucket without a canonical terminal close'
FROM interaction_dag_run_segment rs
WHERE rs.universal_segment_id IS NULL
  AND rs.canonical_action_id IS NULL;

-- Frozen causal edges map onto the canonical Edge that shares their trigger
-- message and whose canonical destination endpoint is the frozen edge's
-- source segment. Trigger-less or ambiguous edges stay unmapped and are
-- audited rather than guessed.
WITH edge_candidates AS (
  SELECT ce.edge_id, ue.id AS universal_edge_id,
         count(*) OVER (PARTITION BY ce.edge_id) AS candidate_count
  FROM interaction_dag_causal_edge ce
  JOIN interaction_dag_run_segment rs
    ON rs.run_id = ce.run_id
   AND rs.segment_id = ce.src_segment_id
   AND rs.universal_segment_id IS NOT NULL
  JOIN interaction_dag_edge ue
    ON ue.trigger_message_id = ce.trigger_message_id
   AND ue.dst_segment_id = rs.universal_segment_id
  WHERE ce.universal_edge_id IS NULL
    AND ce.trigger_message_id IS NOT NULL
)
UPDATE interaction_dag_causal_edge ce
SET universal_edge_id = edge_candidates.universal_edge_id
FROM edge_candidates
WHERE ce.edge_id = edge_candidates.edge_id
  AND edge_candidates.candidate_count = 1;

INSERT INTO interaction_dag_projection_backfill_audit (table_name, row_pk, reason)
SELECT 'interaction_dag_causal_edge', ce.edge_id::text,
       'no unique canonical edge for trigger provenance'
FROM interaction_dag_causal_edge ce
WHERE ce.universal_edge_id IS NULL;

-- Task 22: approximate historical backfill (spec §8.2, §19.11, AC54/55/58).
--
-- A legacy_backfill Segment is one approximate Segment per completed Task,
-- projected by an owner-authorized, rate-limited job behind the final rollout
-- gate. The boundary_quality marker distinguishes it from every canonical
-- (exact) boundary so downstream consumers can treat the two provenance
-- classes differently without guessing:
--
--   * NULL (the canonical default) — the boundary was recorded by the live
--     Universal writer at the durable action itself; ranges, generations and
--     edges are exact.
--   * 'approximate' — the boundary was reconstructed after the fact from a
--     completed Task's full message range. No generation split, no causal
--     edge and no historical scope decision is fabricated; scope, sanitizer
--     and eligibility are re-derived at execution time by the durable
--     publish pipeline. Approximate Segments are excluded from training
--     selection by default (ListTrainingSegmentCandidates).
ALTER TABLE interaction_dag_segment
  ADD COLUMN boundary_quality text,
  ADD CONSTRAINT ck_segment_boundary_quality_valid CHECK (
    boundary_quality IS NULL OR boundary_quality = 'approximate'
  );

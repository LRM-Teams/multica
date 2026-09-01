-- Down: drop the approximate-boundary marker column and its constraint.
ALTER TABLE interaction_dag_segment
  DROP CONSTRAINT ck_segment_boundary_quality_valid,
  DROP COLUMN boundary_quality;

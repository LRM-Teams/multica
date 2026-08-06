-- Drop the interaction_dag_step_reward table.
DROP TABLE interaction_dag_step_reward;

-- Remove the start_seq and end_seq columns from interaction_dag_segment.
ALTER TABLE interaction_dag_segment
    DROP COLUMN end_seq,
    DROP COLUMN start_seq;

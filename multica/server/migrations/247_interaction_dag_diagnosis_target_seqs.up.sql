-- 247_interaction_dag_diagnosis_target_seqs: freeze the exact assistant
-- message sequence numbers a diagnosis segment must score. Counts alone are
-- insufficient because task-message sequences may be sparse or interleaved
-- with user/tool messages.

ALTER TABLE interaction_dag_diagnosis_segment
  ADD COLUMN expected_reward_seqs jsonb NOT NULL DEFAULT '[]'::jsonb;

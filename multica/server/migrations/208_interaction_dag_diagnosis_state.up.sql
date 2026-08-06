-- 208_interaction_dag_diagnosis_state: resumable diagnosis state for the
-- on-demand diagnosis agent. One run row per diagnosis attempt plus one
-- checkpoint row per segment; the tables persist enough state (ordered segment
-- snapshot, per-segment cursor/fetch/reward coverage) to resume a run after a
-- Pi or process failure without re-reading finished segments. Training rewards
-- themselves continue to live only in interaction_dag_step_reward; these tables
-- hold progress/recovery metadata only.

CREATE TABLE interaction_dag_diagnosis_run (
  run_id                  text PRIMARY KEY,
  project_id              text NOT NULL,
  task_id                 text NOT NULL,
  topology_hash           text NOT NULL,
  ordered_segment_ids     jsonb NOT NULL DEFAULT '[]'::jsonb,
  status                  text NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'compacting', 'completed', 'failed')),
  current_segment_ordinal integer NOT NULL DEFAULT 0
    CHECK (current_segment_ordinal >= 0),
  pi_session_id           text NOT NULL DEFAULT '',
  last_error              text NOT NULL DEFAULT '',
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  completed_at            timestamptz
);

-- Resumable-run lookup: latest still-active run for a (project, task).
CREATE INDEX idx_interaction_dag_diagnosis_run_resumable
  ON interaction_dag_diagnosis_run (project_id, task_id, status, updated_at);

CREATE TABLE interaction_dag_diagnosis_segment (
  run_id                  text NOT NULL
    REFERENCES interaction_dag_diagnosis_run (run_id) ON DELETE CASCADE,
  segment_id              text NOT NULL,
  ordinal                 integer NOT NULL CHECK (ordinal >= 0),
  expected_message_count  integer NOT NULL DEFAULT 0
    CHECK (expected_message_count >= 0),
  fetched_message_count   integer NOT NULL DEFAULT 0
    CHECK (fetched_message_count >= 0),
  expected_reward_count   integer NOT NULL DEFAULT 0
    CHECK (expected_reward_count >= 0),
  reward_count            integer NOT NULL DEFAULT 0
    CHECK (reward_count >= 0),
  next_cursor             text NOT NULL DEFAULT '',
  status                  text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'in_progress', 'completed')),
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  completed_at            timestamptz,
  PRIMARY KEY (run_id, segment_id)
);

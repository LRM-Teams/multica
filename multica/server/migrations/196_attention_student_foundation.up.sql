-- PR-6 foundation: collect teacher examples, record offline/shadow evals,
-- and keep student/runtime rollout switches disabled until real model artifacts exist.

CREATE TABLE evolution_model_runtime_config (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  model_kind TEXT NOT NULL CHECK (model_kind IN ('attention_student', 'context_filter')),
  mode TEXT NOT NULL DEFAULT 'off' CHECK (mode IN ('off', 'shadow', 'canary')),
  active_version TEXT NOT NULL DEFAULT '',
  candidate_version TEXT NOT NULL DEFAULT '',
  rollout_percent INT NOT NULL DEFAULT 0 CHECK (rollout_percent >= 0 AND rollout_percent <= 100),
  config JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  updated_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, model_kind),
  CHECK (mode = 'off' OR candidate_version <> '')
);

CREATE TABLE evolution_training_example (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  model_kind TEXT NOT NULL CHECK (model_kind IN ('attention_student', 'context_filter')),
  source_kind TEXT NOT NULL CHECK (source_kind IN ('attention_participant', 'manual', 'context_filter_teacher')),
  source_id UUID,
  agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  input JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(input) = 'object'),
  teacher_label JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(teacher_label) = 'object'),
  student_prediction JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(student_prediction) = 'object'),
  split TEXT NOT NULL DEFAULT 'unassigned' CHECK (split IN ('unassigned', 'train', 'validation', 'test', 'holdout')),
  status TEXT NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate', 'gold', 'rejected', 'archived')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, model_kind, source_kind, source_id),
  CHECK (source_kind = 'manual' OR source_id IS NOT NULL)
);

CREATE INDEX idx_evolution_training_example_workspace
  ON evolution_training_example(workspace_id, model_kind, status, split, created_at DESC);

CREATE INDEX idx_evolution_training_example_source
  ON evolution_training_example(source_kind, source_id)
  WHERE source_id IS NOT NULL;

CREATE TABLE evolution_model_eval_run (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  model_kind TEXT NOT NULL CHECK (model_kind IN ('attention_student', 'context_filter')),
  model_version TEXT NOT NULL,
  mode TEXT NOT NULL CHECK (mode IN ('offline', 'shadow', 'canary')),
  status TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'running', 'failed')),
  dataset_filter JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(dataset_filter) = 'object'),
  metrics JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metrics) = 'object'),
  example_count INT NOT NULL DEFAULT 0 CHECK (example_count >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_evolution_model_eval_run_workspace
  ON evolution_model_eval_run(workspace_id, model_kind, created_at DESC);

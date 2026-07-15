CREATE TABLE memory_curator_profile (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  enabled BOOLEAN NOT NULL DEFAULT false,
  mode TEXT NOT NULL DEFAULT 'review'
    CHECK (mode IN ('observe', 'review', 'auto_safe', 'auto')),
  runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  curator_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  model_override TEXT NOT NULL DEFAULT '',
  target_scope TEXT NOT NULL DEFAULT 'owned_all'
    CHECK (target_scope IN ('owned_all', 'selected')),
  timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  schedule_hour SMALLINT NOT NULL DEFAULT 1
    CHECK (schedule_hour >= 0 AND schedule_hour <= 23),
  catch_up_enabled BOOLEAN NOT NULL DEFAULT true,
  confidence_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.8
    CHECK (confidence_threshold >= 0 AND confidence_threshold <= 1),
  config_version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, user_id)
);

CREATE TABLE memory_curator_target (
  profile_id UUID NOT NULL REFERENCES memory_curator_profile(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  PRIMARY KEY (profile_id, agent_id)
);

ALTER TABLE memory_curation_run
  DROP CONSTRAINT memory_curation_run_status_check,
  ADD CONSTRAINT memory_curation_run_status_check
    CHECK (status IN ('queued', 'waiting_runtime', 'running', 'succeeded', 'failed', 'invalid_config', 'cancelled')),
  ADD COLUMN profile_id UUID REFERENCES memory_curator_profile(id) ON DELETE SET NULL,
  ADD COLUMN owner_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
  ADD COLUMN runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
  ADD COLUMN curator_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  ADD COLUMN curator_model TEXT NOT NULL DEFAULT '',
  ADD COLUMN curator_mode TEXT NOT NULL DEFAULT 'review'
    CHECK (curator_mode IN ('observe', 'review', 'auto_safe', 'auto')),
  ADD COLUMN confidence_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.8
    CHECK (confidence_threshold >= 0 AND confidence_threshold <= 1),
  ADD COLUMN config_version BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN target_agent_ids UUID[] NOT NULL DEFAULT '{}',
  ADD COLUMN execution_owner TEXT NOT NULL DEFAULT 'daemon'
    CHECK (execution_owner IN ('daemon')),
  ADD COLUMN attempt INT NOT NULL DEFAULT 0,
  ADD COLUMN claimed_at TIMESTAMPTZ,
  ADD COLUMN claim_token UUID;

CREATE INDEX idx_memory_curator_profile_runtime
  ON memory_curator_profile(runtime_id)
  WHERE runtime_id IS NOT NULL;
CREATE INDEX idx_memory_curator_profile_schedule
  ON memory_curator_profile(enabled, schedule_hour)
  WHERE enabled = true;
CREATE INDEX idx_memory_curation_run_runtime_queue
  ON memory_curation_run(runtime_id, status, created_at)
  WHERE runtime_id IS NOT NULL AND status IN ('queued', 'waiting_runtime', 'running');
CREATE UNIQUE INDEX idx_memory_curation_run_scheduled_profile_date
  ON memory_curation_run(profile_id, stage, date_from)
  WHERE trigger_kind = 'scheduled' AND profile_id IS NOT NULL;

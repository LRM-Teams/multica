-- Governance loop for local memory/skill candidates and shared evolution units.

CREATE TABLE evolution_unit_submission (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  source_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  source_member_id UUID REFERENCES member(id) ON DELETE SET NULL,

  unit_type TEXT NOT NULL CHECK (unit_type IN ('memory', 'skill', 'workflow', 'tool_pattern', 'preference')),
  local_unit_id TEXT NOT NULL,

  title TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',

  payload JSONB NOT NULL DEFAULT '{}',
  sanitized_payload JSONB NOT NULL DEFAULT '{}',

  content_hash TEXT NOT NULL DEFAULT '',
  bundle_hash TEXT NOT NULL DEFAULT '',
  bundle_ref TEXT NOT NULL DEFAULT '',

  sensitivity TEXT NOT NULL DEFAULT 'unknown'
    CHECK (sensitivity IN ('none', 'local_path', 'personal', 'secret', 'unknown')),
  confidence TEXT NOT NULL DEFAULT 'medium'
    CHECK (confidence IN ('low', 'medium', 'high')),
  suggested_scope TEXT NOT NULL DEFAULT 'workspace',

  evidence JSONB NOT NULL DEFAULT '{}',
  applies JSONB NOT NULL DEFAULT '{}',

  tags TEXT[] NOT NULL DEFAULT '{}',
  tools TEXT[] NOT NULL DEFAULT '{}',
  task_types TEXT[] NOT NULL DEFAULT '{}',
  project_types TEXT[] NOT NULL DEFAULT '{}',
  languages TEXT[] NOT NULL DEFAULT '{}',
  frameworks TEXT[] NOT NULL DEFAULT '{}',

  status TEXT NOT NULL DEFAULT 'candidate'
    CHECK (status IN ('candidate', 'rejected', 'clustered', 'promoted', 'archived')),
  reject_reason TEXT NOT NULL DEFAULT '',
  promoted_unit_id UUID,

  source_created_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  UNIQUE(workspace_id, source_agent_id, local_unit_id)
);

CREATE TABLE shared_evolution_unit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,

  unit_type TEXT NOT NULL CHECK (unit_type IN ('memory', 'skill', 'workflow', 'tool_pattern', 'preference')),
  title TEXT NOT NULL,
  canonical_summary TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',

  metadata JSONB NOT NULL DEFAULT '{}',
  applies JSONB NOT NULL DEFAULT '{}',
  failure_cases JSONB NOT NULL DEFAULT '[]',

  scope TEXT NOT NULL DEFAULT 'workspace',

  tags TEXT[] NOT NULL DEFAULT '{}',
  tools TEXT[] NOT NULL DEFAULT '{}',
  task_types TEXT[] NOT NULL DEFAULT '{}',
  project_types TEXT[] NOT NULL DEFAULT '{}',
  languages TEXT[] NOT NULL DEFAULT '{}',
  frameworks TEXT[] NOT NULL DEFAULT '{}',
  applicable_agent_types TEXT[] NOT NULL DEFAULT '{}',
  applicable_projects TEXT[] NOT NULL DEFAULT '{}',

  priority INT NOT NULL DEFAULT 0,
  score DOUBLE PRECISION NOT NULL DEFAULT 0,

  success_count INT NOT NULL DEFAULT 0,
  failure_count INT NOT NULL DEFAULT 0,
  ignored_count INT NOT NULL DEFAULT 0,
  conflict_count INT NOT NULL DEFAULT 0,
  last_used_at TIMESTAMPTZ,

  status TEXT NOT NULL DEFAULT 'candidate'
    CHECK (status IN ('candidate', 'active', 'deprecated', 'rejected', 'archived', 'quarantined')),
  current_version_id UUID,

  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE evolution_unit_submission
  ADD CONSTRAINT evolution_unit_submission_promoted_unit_fkey
  FOREIGN KEY (promoted_unit_id) REFERENCES shared_evolution_unit(id) ON DELETE SET NULL;

CREATE TABLE shared_evolution_unit_version (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  unit_id UUID NOT NULL REFERENCES shared_evolution_unit(id) ON DELETE CASCADE,
  version INT NOT NULL,

  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}',
  applies JSONB NOT NULL DEFAULT '{}',
  failure_cases JSONB NOT NULL DEFAULT '[]',

  source_submission_ids UUID[] NOT NULL DEFAULT '{}',
  change_reason TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT 'center_curator',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

  UNIQUE(unit_id, version)
);

ALTER TABLE shared_evolution_unit
  ADD CONSTRAINT shared_evolution_unit_current_version_fkey
  FOREIGN KEY (current_version_id) REFERENCES shared_evolution_unit_version(id) ON DELETE SET NULL;

CREATE TABLE evolution_unit_submission_file (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  submission_id UUID NOT NULL REFERENCES evolution_unit_submission(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT 'text/plain',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(submission_id, path)
);

CREATE TABLE shared_evolution_unit_file (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  unit_id UUID NOT NULL REFERENCES shared_evolution_unit(id) ON DELETE CASCADE,
  version_id UUID REFERENCES shared_evolution_unit_version(id) ON DELETE CASCADE,
  path TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT 'text/plain',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(unit_id, version_id, path)
);

CREATE INDEX idx_evolution_submission_workspace ON evolution_unit_submission(workspace_id, created_at DESC);
CREATE INDEX idx_evolution_submission_agent ON evolution_unit_submission(source_agent_id, created_at DESC);
CREATE INDEX idx_evolution_submission_status ON evolution_unit_submission(workspace_id, status);
CREATE INDEX idx_evolution_submission_type ON evolution_unit_submission(workspace_id, unit_type);
CREATE INDEX idx_shared_evolution_unit_workspace ON shared_evolution_unit(workspace_id, status, score DESC);
CREATE INDEX idx_shared_evolution_unit_type ON shared_evolution_unit(workspace_id, unit_type, status);
CREATE INDEX idx_shared_evolution_unit_version_unit ON shared_evolution_unit_version(unit_id, version DESC);
CREATE INDEX idx_evolution_submission_file_submission ON evolution_unit_submission_file(submission_id);
CREATE INDEX idx_shared_evolution_unit_file_unit ON shared_evolution_unit_file(unit_id, version_id);

-- Universal Interaction DAG training governance (spec 14.1): selection
-- manifests, tenant/pooled grants, execution identity and the deletion /
-- unlearning ledger.
--
-- Nothing here activates training by itself:
--   * every EXISTING workspace is backfilled tenant=pending_owner_ack and
--     pooled=disabled — a database default may never silently activate a
--     historical workspace;
--   * the global singleton policy row starts with selection/execution
--     disabled (reward shadow / calibration kill switch, spec 14.1/14.4) —
--     no selection, export, replay task or model update until it is enabled;
--   * legacy online RL sessions opened directly by source tasks are closed
--     and only re-open through a manifest-backed training execution.

CREATE TABLE interaction_dag_training_grant (
  grant_id             uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id         uuid        NOT NULL UNIQUE REFERENCES workspace(id) ON DELETE CASCADE,
  tenant_status        text        NOT NULL DEFAULT 'pending_owner_ack'
    CHECK (tenant_status IN ('pending_owner_ack', 'active', 'revoked')),
  tenant_policy_version bigint     NOT NULL DEFAULT 0 CHECK (tenant_policy_version >= 0),
  tenant_granted_by    text,
  tenant_granted_at    timestamptz,
  pooled_status        text        NOT NULL DEFAULT 'disabled'
    CHECK (pooled_status IN ('disabled', 'active', 'revoked')),
  pooled_policy_version bigint     NOT NULL DEFAULT 0 CHECK (pooled_policy_version >= 0),
  pooled_granted_by    text,
  pooled_granted_at    timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

-- Backfill: existing workspaces must be explicitly acknowledged by an
-- owner/admin (CAS) before tenant selection can ever run.
INSERT INTO interaction_dag_training_grant (workspace_id, tenant_status, pooled_status)
SELECT w.id, 'pending_owner_ack', 'disabled'
FROM workspace w
ON CONFLICT (workspace_id) DO NOTHING;

-- Global reward shadow / calibration kill switch (singleton row). Both
-- switches default OFF: selection/export and training execution stay closed
-- until calibration (spec 14.1/14.4) flips them.
CREATE TABLE interaction_dag_training_policy (
  id                      integer     NOT NULL PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  selection_enabled       boolean     NOT NULL DEFAULT false,
  execution_enabled       boolean     NOT NULL DEFAULT false,
  reward_policy_version   bigint      NOT NULL DEFAULT 0 CHECK (reward_policy_version >= 0),
  per_agent_sample_cap    integer     NOT NULL DEFAULT 10 CHECK (per_agent_sample_cap > 0),
  per_channel_sample_cap  integer     NOT NULL DEFAULT 50 CHECK (per_channel_sample_cap > 0),
  per_workspace_sample_cap integer    NOT NULL DEFAULT 200 CHECK (per_workspace_sample_cap > 0),
  updated_by              text,
  updated_at              timestamptz NOT NULL DEFAULT now()
);
INSERT INTO interaction_dag_training_policy (id) VALUES (1);

-- Per-sample CAS state machine (spec 14.1):
-- eligible -> selected -> exported -> execution_started -> consumed, plus
-- terminal retracted | revoked. sample_kind distinguishes Universal DAG
-- segments from graph-memory explore trajectories so one manifest schema
-- governs both training-data families.
CREATE TABLE interaction_dag_training_sample (
  sample_kind  text        NOT NULL CHECK (sample_kind IN ('segment', 'graph_trajectory')),
  sample_key   text        NOT NULL,
  workspace_id uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  status       text        NOT NULL
    CHECK (status IN ('eligible', 'selected', 'exported', 'execution_started', 'consumed', 'retracted', 'revoked')),
  manifest_id  uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (sample_kind, sample_key)
);
CREATE INDEX interaction_dag_training_sample_queue
  ON interaction_dag_training_sample (workspace_id, status);

-- One immutable selection snapshot. The grant identity, policy version,
-- actor, granted_at, purpose and the workspace sampling config are frozen at
-- selection time; a later grant change can never rewrite history here.
CREATE TABLE interaction_dag_training_manifest (
  manifest_id         uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id        uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  purpose             text        NOT NULL CHECK (purpose IN ('tenant', 'pooled')),
  grant_id            uuid        NOT NULL REFERENCES interaction_dag_training_grant(grant_id),
  grant_policy_version bigint     NOT NULL,
  grant_actor         text        NOT NULL,
  granted_at          timestamptz NOT NULL,
  workspace_config    jsonb       NOT NULL DEFAULT '{}',
  reward_policy_version bigint     NOT NULL DEFAULT 0,
  item_count          integer     NOT NULL CHECK (item_count >= 0),
  content_hash        text        NOT NULL,
  status              text        NOT NULL
    CHECK (status IN ('selected', 'exported', 'execution_started', 'consumed', 'invalidated')),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX interaction_dag_training_manifest_recent
  ON interaction_dag_training_manifest (workspace_id, purpose, created_at DESC);

-- Manifest items fix each sample's identity, content hash, sanitizer /
-- policy versions, scope and the reward revision observed at selection.
-- sanitizer/policy versions are mandatory for Universal DAG segments and
-- intentionally empty for graph trajectories (sanitized upstream).
CREATE TABLE interaction_dag_training_manifest_item (
  manifest_id      uuid    NOT NULL REFERENCES interaction_dag_training_manifest(manifest_id) ON DELETE CASCADE,
  item_kind        text    NOT NULL CHECK (item_kind IN ('segment', 'graph_trajectory')),
  item_key         text    NOT NULL,
  item_hash        text    NOT NULL,
  sanitizer_version text,
  policy_version   text,
  scope            jsonb   NOT NULL DEFAULT '{}',
  reward_status    text    NOT NULL CHECK (reward_status IN ('available', 'unavailable')),
  reward_revision  bigint  NOT NULL DEFAULT 0 CHECK (reward_revision >= 0),
  PRIMARY KEY (manifest_id, item_kind, item_key),
  CONSTRAINT interaction_dag_training_item_sanitizer_shape CHECK (
    (item_kind = 'segment' AND sanitizer_version IS NOT NULL AND policy_version IS NOT NULL)
    OR (item_kind = 'graph_trajectory')
  )
);
CREATE INDEX interaction_dag_training_item_hash
  ON interaction_dag_training_manifest_item (item_hash);

-- Execution identity: the distinct replay/training task that consumed an
-- exported manifest. SetReward / terminal routing apply only to tasks whose
-- execution row exists here (spec 14.1 delayed execution).
CREATE TABLE interaction_dag_training_execution (
  execution_id    uuid        NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
  manifest_id     uuid        NOT NULL UNIQUE REFERENCES interaction_dag_training_manifest(manifest_id),
  training_task_id uuid       UNIQUE,
  status          text        NOT NULL DEFAULT 'started'
    CHECK (status IN ('started', 'consumed', 'failed')),
  started_at      timestamptz NOT NULL DEFAULT now(),
  consumed_at     timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- Deletion / unlearning ledger (spec 14.1/13/14): consumed samples whose
-- grant was later revoked land here and are excluded from all later
-- training. The product does not claim immediate exact unlearning.
CREATE TABLE interaction_dag_training_deletion_ledger (
  ledger_id    bigint      NOT NULL GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id uuid        NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  sample_kind  text        NOT NULL,
  sample_key   text        NOT NULL,
  manifest_id  uuid,
  purpose      text,
  reason       text        NOT NULL,
  requested_by text        NOT NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  UNIQUE (sample_kind, sample_key, reason)
);
CREATE INDEX interaction_dag_training_deletion_pending
  ON interaction_dag_training_deletion_ledger (requested_at)
  WHERE processed_at IS NULL;

-- The manifest-backed training execution creates a DISTINCT replay task
-- (reason 'training_replay'); without this constraint extension the insert
-- would be rejected by the migration 449 reason check.
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention','dm','ambient','thread_reply','channel_message',
    'chat_session','voice_call','issue_thread_backflow','collaboration_turn',
    'collaboration_manager_fallback','channel_onboarding','issue','quick_create',
    'autopilot','agent_radar','training','training_replay','environment_dispatch',
    'memory_curation','reminder','channel_role_changed','goal_graph_delta',
    'goal_controller','note_worker'
  ));

-- Legacy online RL sessions opened directly by source tasks are closed;
-- explore reward collection keeps running through the reward outbox
-- (shadow), and a session only re-opens through a manifest-backed training
-- execution once the global switches allow it.
UPDATE graph_memory_rl_session
SET status = 'closed', closed_at = COALESCE(closed_at, now()), updated_at = now()
WHERE status IN ('opening', 'open');

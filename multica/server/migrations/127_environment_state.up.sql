-- 127_environment_state.up.sql
-- environment: a sandbox state handle. Base envs (mode='base') have no
-- project; scratch/branch envs are forked from a parent env and associated
-- with projects via project.env_id (1:1 — a branch always forks, so a state
-- env is never shared across projects).
--
-- sandbox_ids holds ONE OR MORE sandbox handles: an environment can host many
-- agents, and each agent runs in its own sandbox. Base envs are booted with a
-- single sandbox; branching an env forks every sandbox in the set so each
-- agent's state is preserved independently.
CREATE TABLE environment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    sandbox_ids TEXT[] NOT NULL DEFAULT '{}',
    parent_env_id UUID REFERENCES environment(id) ON DELETE SET NULL,
    mode TEXT NOT NULL CHECK (mode IN ('base', 'scratch', 'branch')),
    domain TEXT CHECK (domain IN ('swe_lego', 'self_play')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_environment_workspace ON environment(workspace_id);
CREATE INDEX idx_environment_parent ON environment(parent_env_id) WHERE parent_env_id IS NOT NULL;

-- A project references an env (its sandbox state). A state env (scratch/branch)
-- is referenced by exactly one project — a branch always forks, so env_id is
-- never shared. The partial UNIQUE index enforces this 1:1 invariant and lets
-- the service resolve a source env_id to its single project (GetProjectByEnvID).
-- Base envs have no project. ON DELETE RESTRICT: an env cannot be deleted while
-- a project still references it — the caller must delete the project first via
-- DELETE /api/v1/env-dispatch/{projectID}. This prevents silently orphaning a
-- live project (which would leave its agent runs pointing at a dead sandbox).
ALTER TABLE project
    ADD COLUMN env_id UUID REFERENCES environment(id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX idx_project_env_unique ON project(env_id) WHERE env_id IS NOT NULL;

-- env_dispatch_request: idempotency ledger (spec §7.7). Stores the response
-- for a given (workspace_id, idempotency_key) so a retried POST /env-dispatch
-- replays the original rollouts[] instead of forking/creating again. The
-- response row is written in the reset transaction of the first rollout.
CREATE TABLE env_dispatch_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    idempotency_key UUID NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, idempotency_key)
);

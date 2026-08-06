-- training_dispatch records the training intent for an env-dispatch rollout
-- project (spec sub-project D §4.1). When a dispatch is issued with a
-- train_agent_id, one row is written per rollout project so the later
-- session-open hook can resolve the training target + default reward by
-- project_id. project_id is the PRIMARY KEY (one training target per rollout
-- project) and cascades away with its project.
CREATE TABLE training_dispatch (
    project_id UUID PRIMARY KEY REFERENCES project(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL,
    train_agent_id UUID NOT NULL,
    default_reward DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

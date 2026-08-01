-- LRM-961 / LRM-954: Skill grant ladder agent(L1) → channel(L2) → workspace(L3).
-- Import/create default remains agent; promotions are explicit and audited.

ALTER TABLE skill
  ADD COLUMN grant_level TEXT NOT NULL DEFAULT 'agent',
  ADD COLUMN channel_id UUID REFERENCES channel(id) ON DELETE SET NULL;

ALTER TABLE skill
  ADD CONSTRAINT skill_grant_level_check
  CHECK (grant_level IN ('agent', 'channel', 'workspace'));

ALTER TABLE skill
  ADD CONSTRAINT skill_grant_channel_consistency_check
  CHECK (
    (grant_level = 'channel' AND channel_id IS NOT NULL)
    OR (grant_level <> 'channel' AND channel_id IS NULL)
  );

CREATE INDEX idx_skill_channel_id ON skill(channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX idx_skill_workspace_grant_level ON skill(workspace_id, grant_level);

CREATE TABLE skill_promotion (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id UUID NOT NULL REFERENCES skill(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    from_level TEXT NOT NULL,
    to_level TEXT NOT NULL,
    channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
    actor_type TEXT NOT NULL,
    actor_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT skill_promotion_from_level_check
      CHECK (from_level IN ('agent', 'channel', 'workspace')),
    CONSTRAINT skill_promotion_to_level_check
      CHECK (to_level IN ('agent', 'channel', 'workspace')),
    CONSTRAINT skill_promotion_actor_type_check
      CHECK (actor_type IN ('member', 'agent'))
);

CREATE INDEX idx_skill_promotion_skill_created
  ON skill_promotion(skill_id, created_at DESC);
CREATE INDEX idx_skill_promotion_workspace_created
  ON skill_promotion(workspace_id, created_at DESC);

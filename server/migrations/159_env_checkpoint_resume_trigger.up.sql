-- Adds the resume-trigger descriptor captured at checkpoint-create time and
-- executed by ResumeFromCheckpoint to re-engage the agent runtime (reset the
-- in-flight task + wake the resumed daemon). Nullable so pre-change checkpoints
-- (NULL) degrade to sandbox-only resume (legacy behavior).
ALTER TABLE env_checkpoint
    ADD COLUMN IF NOT EXISTS resume_trigger jsonb;

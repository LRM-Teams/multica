-- Beckham escalation ladder: per (channel, agent) count of how many times the
-- group manager has nudged that agent WITHOUT the agent making progress. Reset
-- to 0 when the agent makes real progress (completes an issue task). Beckham
-- escalates by this count: 1=start, 2=ask blocker, 3=reassign/@PM, 4+=flag human.
CREATE TABLE IF NOT EXISTS wendy_nudge_ladder (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  nudge_count INTEGER NOT NULL DEFAULT 0,
  last_nudged_at TIMESTAMPTZ,
  last_progress_seen_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, agent_id)
);

CREATE INDEX IF NOT EXISTS wendy_nudge_ladder_agent_idx
  ON wendy_nudge_ladder (workspace_id, agent_id);

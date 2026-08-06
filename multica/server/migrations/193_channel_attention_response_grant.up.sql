-- PR-3: resolve completed Attention Rounds into internal offers,
-- one-shot convergence turns, or a public response grant.

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_response_mode_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_response_mode_check
    CHECK (response_mode IN ('no_public_output', 'contribution_offer', 'convergence_vote', 'public_response'));

CREATE TABLE channel_attention_contribution_offer (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  participant_id UUID REFERENCES channel_attention_participant(id) ON DELETE SET NULL,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  offer_source TEXT NOT NULL DEFAULT 'attention_decision'
    CHECK (offer_source IN ('attention_decision', 'convergence_merge', 'late_contribution')),
  value_type TEXT NOT NULL
    CHECK (value_type IN ('unique_evidence', 'correction', 'task_claim', 'direct_answer', 'needs_protocol')),
  summary TEXT NOT NULL DEFAULT '',
  evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(evidence_refs) = 'array'),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'merged', 'ignored', 'escalated')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id, agent_id, offer_source)
);

CREATE INDEX idx_channel_attention_contribution_offer_round
  ON channel_attention_contribution_offer(round_id, status, created_at);

CREATE TABLE channel_attention_convergence_vote (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  participant_id UUID REFERENCES channel_attention_participant(id) ON DELETE SET NULL,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  inbox_event_id UUID UNIQUE REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'completed', 'failed')),
  vote TEXT
    CHECK (vote IS NULL OR vote IN ('YIELD', 'KEEP', 'MERGE', 'REQUEST_MANAGER')),
  target_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  summary TEXT NOT NULL DEFAULT '',
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  model_version TEXT,
  last_error TEXT,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id, agent_id),
  CHECK ((status = 'completed') = (vote IS NOT NULL)),
  CHECK (input_tokens >= 0),
  CHECK (output_tokens >= 0)
);

CREATE INDEX idx_channel_attention_convergence_vote_round
  ON channel_attention_convergence_vote(round_id, status, agent_id);

CREATE TABLE channel_attention_response_grant (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  round_id UUID NOT NULL REFERENCES channel_attention_round(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  inbox_event_id UUID UNIQUE REFERENCES agent_inbox_event(id) ON DELETE SET NULL,
  grant_type TEXT NOT NULL
    CHECK (grant_type IN ('unique_answer', 'converged', 'manager_fallback')),
  status TEXT NOT NULL DEFAULT 'granted'
    CHECK (status IN ('granted', 'consumed', 'expired', 'revoked')),
  reason TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '10 minutes',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (round_id),
  CHECK (expires_at > created_at)
);

CREATE INDEX idx_channel_attention_response_grant_agent
  ON channel_attention_response_grant(agent_id, status, expires_at);

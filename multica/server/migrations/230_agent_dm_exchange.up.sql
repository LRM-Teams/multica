CREATE TABLE agent_dm_pair_control (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_low_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  agent_high_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  state TEXT NOT NULL DEFAULT 'active'
    CHECK (state IN ('active', 'paused_pair', 'paused_frequency')),
  pause_reason TEXT,
  window_started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  window_message_count INT NOT NULL DEFAULT 0 CHECK (window_message_count >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_low_id, agent_high_id),
  CHECK (agent_low_id::text < agent_high_id::text)
);

CREATE TABLE agent_dm_owner_control (
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  owner_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  paused BOOLEAN NOT NULL DEFAULT false,
  pause_reason TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, owner_id)
);

CREATE TABLE agent_dm_exchange (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  agent_low_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  agent_high_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  next_sender_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
  matter_id UUID NOT NULL,
  source_channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  source_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  latest_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  turn_count INT NOT NULL DEFAULT 0 CHECK (turn_count >= 0),
  round_limit INT NOT NULL DEFAULT 3 CHECK (round_limit > 0),
  granted_rounds INT NOT NULL DEFAULT 0 CHECK (granted_rounds >= 0),
  state TEXT NOT NULL DEFAULT 'active'
    CHECK (state IN (
      'active',
      'paused_budget',
      'paused_frequency',
      'paused_pair',
      'paused_global'
    )),
  pause_reason TEXT,
  notified_at TIMESTAMPTZ,
  notification_sent_at TIMESTAMPTZ,
  notification_epoch INT NOT NULL DEFAULT 0 CHECK (notification_epoch >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, agent_low_id, agent_high_id, matter_id),
  CHECK (agent_low_id::text < agent_high_id::text)
);

CREATE INDEX idx_agent_dm_exchange_channel_updated
  ON agent_dm_exchange(channel_id, updated_at DESC);

CREATE INDEX idx_agent_dm_exchange_pair_updated
  ON agent_dm_exchange(workspace_id, agent_low_id, agent_high_id, updated_at DESC);

-- Durable idempotency receipt for one pause occurrence. The DM system row,
-- every owner inbox item, this receipt, and notification_sent_at are committed
-- together; a failed attempt leaves none of them behind.
CREATE TABLE agent_dm_pause_notification (
  exchange_id UUID NOT NULL REFERENCES agent_dm_exchange(id) ON DELETE CASCADE,
  notification_epoch INT NOT NULL CHECK (notification_epoch > 0),
  channel_message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (exchange_id, notification_epoch)
);

CREATE INDEX idx_agent_dm_pause_notification_message
  ON agent_dm_pause_notification(channel_message_id);

-- Agent hard-delete must be able to enforce every new FK without scanning the
-- whole A2A control/exchange tables. These single-column prefixes are required
-- even though the pair indexes above begin with workspace_id.
CREATE INDEX idx_agent_dm_pair_control_agent_low
  ON agent_dm_pair_control(agent_low_id);

CREATE INDEX idx_agent_dm_pair_control_agent_high
  ON agent_dm_pair_control(agent_high_id);

CREATE INDEX idx_agent_dm_exchange_agent_low
  ON agent_dm_exchange(agent_low_id);

CREATE INDEX idx_agent_dm_exchange_agent_high
  ON agent_dm_exchange(agent_high_id);

CREATE INDEX idx_agent_dm_exchange_next_sender
  ON agent_dm_exchange(next_sender_agent_id)
  WHERE next_sender_agent_id IS NOT NULL;

ALTER TABLE agent_inbox_event
  ADD COLUMN agent_dm_exchange_id UUID REFERENCES agent_dm_exchange(id) ON DELETE SET NULL,
  ADD COLUMN agent_dm_turn INT CHECK (agent_dm_turn IS NULL OR agent_dm_turn > 0);

CREATE INDEX idx_agent_inbox_event_agent_dm_exchange
  ON agent_inbox_event(agent_dm_exchange_id, created_at)
  WHERE agent_dm_exchange_id IS NOT NULL;

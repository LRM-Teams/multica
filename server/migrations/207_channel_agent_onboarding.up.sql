BEGIN;

-- A channel_member row is one membership generation. Existing rows receive a
-- baseline identity, but the trigger is installed only after the backfill so
-- deploying this migration never wakes the current roster.
ALTER TABLE channel_member
  ADD COLUMN generation_id UUID NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN added_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
  ADD COLUMN join_source TEXT NOT NULL DEFAULT 'system';

ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_join_source_check
  CHECK (join_source IN ('manual', 'system', 'system_general'));

CREATE UNIQUE INDEX channel_member_generation_unique
  ON channel_member(generation_id);

ALTER TABLE channel_message
  ADD COLUMN membership_generation_id UUID;

ALTER TABLE channel_message
  ADD CONSTRAINT channel_message_membership_generation_shape_check
  CHECK (membership_generation_id IS NULL OR author_type = 'system');

CREATE UNIQUE INDEX channel_message_membership_generation_unique
  ON channel_message(membership_generation_id)
  WHERE membership_generation_id IS NOT NULL;

CREATE TABLE channel_agent_onboarding (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  membership_generation_id UUID NOT NULL,
  system_message_id UUID NOT NULL UNIQUE REFERENCES channel_message(id) ON DELETE CASCADE,
  source_type TEXT NOT NULL
    CHECK (source_type IN ('manual', 'system', 'system_invariant')),
  source_actor_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'claimed', 'sent', 'skipped', 'expired')),
  publication_status TEXT NOT NULL DEFAULT 'pending'
    CHECK (publication_status IN ('pending', 'publishing', 'published')),
  publication_attempt INT NOT NULL DEFAULT 0 CHECK (publication_attempt >= 0),
  publication_lease_expires_at TIMESTAMPTZ,
  published_at TIMESTAMPTZ,
  terminal_evidence JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(terminal_evidence) = 'object'),
  claimed_at TIMESTAMPTZ,
  terminal_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (channel_id, agent_id, membership_generation_id)
);

CREATE INDEX channel_agent_onboarding_agent_pending
  ON channel_agent_onboarding(agent_id, created_at, id)
  WHERE status IN ('pending', 'claimed');

ALTER TABLE agent_inbox_event
  ADD COLUMN channel_onboarding_id UUID UNIQUE
    REFERENCES channel_agent_onboarding(id) ON DELETE CASCADE;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention',
    'dm',
    'ambient',
    'thread_reply',
    'channel_message',
    'collaboration_turn',
    'collaboration_manager_fallback',
    'channel_onboarding'
  ));

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_terminal_outcome_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_terminal_outcome_check
  CHECK (terminal_outcome IN (
    'replied',
    'no_reply',
    'held',
    'failed',
    'sent',
    'skipped',
    'expired'
  ));

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_channel_onboarding_shape_check
  CHECK (
    (reason = 'channel_onboarding' AND channel_onboarding_id IS NOT NULL)
    OR
    (reason <> 'channel_onboarding' AND channel_onboarding_id IS NULL)
  );

CREATE OR REPLACE FUNCTION maintain_channel_agent_onboarding()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  target_channel channel%ROWTYPE;
  system_message_id UUID;
  onboarding_source TEXT;
  actor_handle TEXT;
  actor_display_name TEXT;
  actor_type TEXT;
  target_handle TEXT;
  target_display_name TEXT;
  canonical_content TEXT;
  event_params JSONB;
BEGIN
  IF TG_OP = 'INSERT' AND NEW.member_type = 'agent' THEN
    SELECT * INTO target_channel
    FROM channel
    WHERE id = NEW.channel_id;

    IF target_channel.id IS NOT NULL
       AND target_channel.kind = 'group'
       AND target_channel.archived_at IS NULL THEN
      onboarding_source := CASE
        WHEN target_channel.system_key = 'general' THEN 'system_invariant'
        ELSE NEW.join_source
      END;

      SELECT COALESCE(NULLIF(name, ''), 'agent'),
             COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), 'Agent')
      INTO target_handle, target_display_name
      FROM agent
      WHERE id = NEW.member_id;

      IF onboarding_source = 'system_invariant' THEN
        actor_type := 'system';
        actor_handle := 'system';
        actor_display_name := 'Workspace';
        canonical_content := target_display_name || ' joined this channel';
      ELSIF NEW.added_by IS NOT NULL THEN
        SELECT COALESCE(NULLIF(name, ''), NULLIF(email, ''), 'user'),
               COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), NULLIF(email, ''), 'User')
        INTO actor_handle, actor_display_name
        FROM "user"
        WHERE id = NEW.added_by;
        actor_type := 'human';
        canonical_content := actor_display_name || ' added ' || target_display_name || ' to the channel';
      ELSE
        actor_type := 'system';
        actor_handle := 'system';
        actor_display_name := 'System';
        canonical_content := target_display_name || ' joined this channel';
      END IF;

      event_params := jsonb_build_object(
        'actor_id', COALESCE(NEW.added_by::text, ''),
        'actor_type', actor_type,
        'actor_handle', actor_handle,
        'actor_display_name', actor_display_name,
        'actor_name', actor_display_name,
        'target_id', NEW.member_id::text,
        'target_type', 'agent',
        'target_handle', target_handle,
        'target_display_name', target_display_name,
        'target_name', target_display_name,
        'source', onboarding_source,
        'membership_generation_id', NEW.generation_id::text
      );

      INSERT INTO channel_message (
        channel_id,
        workspace_id,
        author_type,
        author_name,
        content,
        parts,
        source,
        membership_generation_id
      )
      VALUES (
        NEW.channel_id,
        NEW.workspace_id,
        'system',
        'system',
        canonical_content,
        jsonb_build_array(jsonb_build_object(
          'type', 'system_event',
          'event', 'channel_member_added',
          'event_params', event_params
        )),
        'multica',
        NEW.generation_id
      )
      ON CONFLICT (membership_generation_id) WHERE membership_generation_id IS NOT NULL
      DO NOTHING
      RETURNING id INTO system_message_id;

      IF system_message_id IS NULL THEN
        SELECT id INTO system_message_id
        FROM channel_message
        WHERE membership_generation_id = NEW.generation_id;
      END IF;

      INSERT INTO channel_agent_onboarding (
        workspace_id,
        channel_id,
        agent_id,
        membership_generation_id,
        system_message_id,
        source_type,
        source_actor_id
      )
      VALUES (
        NEW.workspace_id,
        NEW.channel_id,
        NEW.member_id,
        NEW.generation_id,
        system_message_id,
        onboarding_source,
        NEW.added_by
      )
      ON CONFLICT (channel_id, agent_id, membership_generation_id) DO NOTHING;
    END IF;
    RETURN NEW;
  END IF;

  IF TG_OP = 'DELETE' AND OLD.member_type = 'agent' THEN
    UPDATE agent_event_delivery delivery
    SET status = 'expired',
        last_error = 'channel onboarding membership generation removed',
        updated_at = now()
    FROM agent_inbox_event inbox,
         channel_agent_onboarding onboarding
    WHERE onboarding.channel_id = OLD.channel_id
      AND onboarding.agent_id = OLD.member_id
      AND onboarding.membership_generation_id = OLD.generation_id
      AND onboarding.status IN ('pending', 'claimed')
      AND inbox.channel_onboarding_id = onboarding.id
      AND delivery.inbox_event_id = inbox.id
      AND delivery.status IN ('leased', 'processing');

    UPDATE agent_inbox_event inbox
    SET status = 'suppressed',
        terminal_outcome = 'expired',
        retryable = FALSE,
        terminal_at = now(),
        last_error = 'channel onboarding membership generation removed',
        updated_at = now()
    FROM channel_agent_onboarding onboarding
    WHERE onboarding.channel_id = OLD.channel_id
      AND onboarding.agent_id = OLD.member_id
      AND onboarding.membership_generation_id = OLD.generation_id
      AND onboarding.status IN ('pending', 'claimed')
      AND inbox.channel_onboarding_id = onboarding.id
      AND inbox.status IN ('pending', 'draining', 'failed');

    UPDATE channel_agent_onboarding
    SET status = 'expired',
        terminal_evidence = jsonb_build_object('reason', 'membership_generation_removed'),
        terminal_at = now(),
        updated_at = now()
    WHERE channel_id = OLD.channel_id
      AND agent_id = OLD.member_id
      AND membership_generation_id = OLD.generation_id
      AND status IN ('pending', 'claimed');
    RETURN OLD;
  END IF;

  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

CREATE TRIGGER trg_maintain_channel_agent_onboarding
AFTER INSERT OR DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION maintain_channel_agent_onboarding();

COMMIT;

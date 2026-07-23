BEGIN;

CREATE TABLE voice_call_session (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id),
  agent_id UUID NOT NULL REFERENCES agent(id),
  user_id UUID NOT NULL REFERENCES "user"(id),
  provider TEXT NOT NULL CHECK (btrim(provider) <> ''),
  provider_task_id TEXT CHECK (provider_task_id IS NULL OR btrim(provider_task_id) <> ''),
  room_id TEXT CHECK (room_id IS NULL OR btrim(room_id) <> ''),
  status TEXT NOT NULL DEFAULT 'starting'
    CHECK (status IN (
      'starting', 'connecting', 'active', 'reconnecting',
      'ending', 'ended', 'failed'
    )),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  connected_at TIMESTAMPTZ,
  ended_at TIMESTAMPTZ,
  end_reason TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  input_audio_ms BIGINT NOT NULL DEFAULT 0 CHECK (input_audio_ms >= 0),
  output_audio_ms BIGINT NOT NULL DEFAULT 0 CHECK (output_audio_ms >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((status IN ('ended', 'failed')) = (ended_at IS NOT NULL)),
  CHECK (connected_at IS NULL OR connected_at >= started_at),
  CHECK (ended_at IS NULL OR ended_at >= started_at),
  CHECK (ended_at IS NULL OR connected_at IS NULL OR ended_at >= connected_at),
  CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX voice_call_session_active_pair_idx
  ON voice_call_session (workspace_id, user_id, agent_id)
  WHERE status NOT IN ('ended', 'failed');

CREATE UNIQUE INDEX voice_call_session_provider_task_idx
  ON voice_call_session (provider, provider_task_id)
  WHERE provider_task_id IS NOT NULL;

CREATE UNIQUE INDEX voice_call_session_provider_room_idx
  ON voice_call_session (provider, room_id)
  WHERE room_id IS NOT NULL;

CREATE FUNCTION enforce_voice_call_session_status_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.status = OLD.status THEN
    RETURN NEW;
  END IF;

  IF NOT (
    (OLD.status = 'starting' AND NEW.status IN ('connecting', 'ending', 'failed'))
    OR (OLD.status = 'connecting' AND NEW.status IN ('active', 'ending', 'failed'))
    OR (OLD.status = 'active' AND NEW.status IN ('reconnecting', 'ending', 'failed'))
    OR (OLD.status = 'reconnecting' AND NEW.status IN ('active', 'ending', 'failed'))
    OR (OLD.status = 'ending' AND NEW.status IN ('ended', 'failed'))
  ) THEN
    RAISE EXCEPTION 'invalid voice call status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER voice_call_session_status_transition
BEFORE UPDATE OF status ON voice_call_session
FOR EACH ROW
EXECUTE FUNCTION enforce_voice_call_session_status_transition();

CREATE TABLE voice_call_turn (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  call_session_id UUID NOT NULL REFERENCES voice_call_session(id) ON DELETE CASCADE,
  sequence BIGINT NOT NULL CHECK (sequence > 0),
  speaker TEXT NOT NULL CHECK (speaker IN ('member', 'agent')),
  transcript TEXT NOT NULL CHECK (btrim(transcript) <> ''),
  started_at TIMESTAMPTZ NOT NULL,
  ended_at TIMESTAMPTZ NOT NULL,
  is_interrupted BOOLEAN NOT NULL DEFAULT false,
  spoken_duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (spoken_duration_ms >= 0),
  provider_turn_id TEXT CHECK (provider_turn_id IS NULL OR btrim(provider_turn_id) <> ''),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (ended_at >= started_at),
  UNIQUE (call_session_id, sequence)
);

CREATE UNIQUE INDEX voice_call_turn_provider_identity_idx
  ON voice_call_turn (call_session_id, provider_turn_id)
  WHERE provider_turn_id IS NOT NULL;

COMMIT;

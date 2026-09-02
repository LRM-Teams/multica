-- Migrate the legacy interaction DAG to the workspace-scoped canonical schema.
-- The entire script is expected to run in one transaction.

SELECT pg_advisory_xact_lock(hashtext('migration-454-universal-interaction-dag')::bigint);

LOCK TABLE interaction_dag_segment, interaction_dag_edge,
  project, channel, agent_inbox_event, task_message,
  env_dispatch_run, env_dispatch_run_agent, pi_provider_call
IN ACCESS EXCLUSIVE MODE;

-- Re-run the Task 0 classification while writes are fenced. Error messages are
-- deliberately aggregate-only and never include trajectory, tensor, or message data.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM task_message
    GROUP BY task_id, seq
    HAVING count(*) > 1
  ) THEN
    RAISE EXCEPTION 'migration 454 refused duplicate task message identity';
  END IF;

  IF EXISTS (
    WITH legacy_rows AS (
      SELECT
        segment.segment_id,
        segment.project_id,
        segment.agent_run_id,
        segment.start_seq,
        segment.end_seq,
        segment.trajectory_source,
        segment.trajectory <> '[]'::jsonb AS has_readable_trajectory,
        dispatch.run_id,
        message.message_count,
        message.distinct_message_seq_count,
        message.min_message_seq,
        message.max_message_seq,
        count(*) OVER (
          PARTITION BY dispatch.run_id, segment.agent_run_id,
                       segment.start_seq, segment.end_seq
        ) AS duplicate_range_count
      FROM interaction_dag_segment AS segment
      LEFT JOIN env_dispatch_run AS dispatch
        ON dispatch.project_id::text = segment.project_id
      LEFT JOIN LATERAL (
        SELECT
          count(*)::bigint AS message_count,
          count(DISTINCT task_message.seq)::bigint AS distinct_message_seq_count,
          min(task_message.seq)::integer AS min_message_seq,
          max(task_message.seq)::integer AS max_message_seq
        FROM task_message
        WHERE task_message.task_id::text = segment.agent_run_id
          AND task_message.seq BETWEEN segment.start_seq AND segment.end_seq
      ) AS message ON true
    )
    SELECT 1
    FROM legacy_rows
    WHERE segment_id IS NULL
       OR segment_id = ''
       OR project_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
       OR agent_run_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
       OR run_id IS NULL
       OR trajectory_source NOT IN ('areal_tensor', 'task_messages')
       OR start_seq <= 0
       OR end_seq < start_seq
       OR message_count <> (end_seq::bigint - start_seq::bigint + 1)
       OR distinct_message_seq_count <> (end_seq::bigint - start_seq::bigint + 1)
       OR min_message_seq IS NULL
       OR max_message_seq IS NULL
       OR min_message_seq <> start_seq
       OR max_message_seq <> end_seq
       OR duplicate_range_count > 1
       OR has_readable_trajectory
  ) THEN
    RAISE EXCEPTION 'migration 454 refused unsafe legacy interaction DAG rows';
  END IF;

  -- UUID casts are safe only after the syntax classification above succeeds.
  IF EXISTS (
    SELECT 1
    FROM interaction_dag_segment AS segment
    LEFT JOIN project AS project_owner
      ON project_owner.id = segment.project_id::uuid
    LEFT JOIN agent_inbox_event AS task_owner
      ON task_owner.id = segment.agent_run_id::uuid
    LEFT JOIN channel AS channel_owner
      ON channel_owner.id = task_owner.channel_id
    LEFT JOIN env_dispatch_run AS dispatch
      ON dispatch.project_id = project_owner.id
    WHERE project_owner.id IS NULL
       OR task_owner.id IS NULL
       OR dispatch.run_id IS NULL
       OR project_owner.workspace_id <> task_owner.workspace_id
       OR dispatch.workspace_id <> project_owner.workspace_id
       OR (task_owner.channel_id IS NOT NULL AND channel_owner.id IS NULL)
       OR (task_owner.channel_id IS NOT NULL
           AND channel_owner.workspace_id <> project_owner.workspace_id)
       OR (task_owner.channel_id IS NOT NULL
           AND channel_owner.project_id IS DISTINCT FROM project_owner.id)
  ) THEN
    RAISE EXCEPTION 'migration 454 refused unverifiable legacy ownership';
  END IF;

  IF EXISTS (
    WITH ordered_ranges AS (
      SELECT
        agent_run_id,
        start_seq,
        lag(end_seq) OVER (
          PARTITION BY agent_run_id
          ORDER BY start_seq, end_seq, segment_id
        ) AS previous_end_seq
      FROM interaction_dag_segment
    )
    SELECT 1
    FROM ordered_ranges
    WHERE previous_end_seq IS NOT NULL
      AND start_seq <= previous_end_seq
  ) THEN
    RAISE EXCEPTION 'migration 454 refused overlapping legacy ranges';
  END IF;

  -- Legacy edge semantics do not prove canonical event linkage or edge order.
  IF EXISTS (SELECT 1 FROM interaction_dag_edge) THEN
    RAISE EXCEPTION 'migration 454 refused unmappable legacy edges';
  END IF;
END;
$$;

CREATE UNIQUE INDEX task_message_task_id_seq_454_uidx
  ON task_message (task_id, seq);

ALTER TABLE interaction_dag_segment
  ADD COLUMN workspace_id uuid,
  ADD COLUMN generation bigint,
  ADD COLUMN project_id_at_event uuid,
  ADD COLUMN channel_id_at_event uuid,
  ADD COLUMN route_generation_at_event bigint,
  ADD COLUMN memory_type_at_event text,
  ADD COLUMN graph_projection_eligible_at_event boolean,
  ADD COLUMN close_action_kind text,
  ADD COLUMN canonical_action_id uuid,
  ADD COLUMN visible_action_key text,
  ADD COLUMN derivative boolean,
  ADD COLUMN trainable_eligible boolean,
  ADD COLUMN publish_status text,
  ADD COLUMN content_status text,
  ADD COLUMN publish_seq bigint,
  ADD COLUMN sanitizer_version text,
  ADD COLUMN policy_version text,
  ADD COLUMN provider_capture_status text,
  ADD COLUMN provider_capture_id text,
  ADD COLUMN provider_capture_version bigint,
  ADD COLUMN provider_capture_correlation_key text,
  ADD COLUMN run_id uuid,
  ADD COLUMN run_agent_id uuid,
  ADD COLUMN published_at timestamptz,
  ADD COLUMN retracted_at timestamptz,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

WITH ranked AS (
  SELECT
    segment.segment_id,
    project_owner.workspace_id,
    project_owner.id AS project_id_at_event,
    task_owner.channel_id AS channel_id_at_event,
    row_number() OVER (
      PARTITION BY segment.agent_run_id
      ORDER BY segment.start_seq, segment.end_seq, segment.segment_id
    )::bigint AS generation
  FROM interaction_dag_segment AS segment
  JOIN project AS project_owner
    ON project_owner.id = segment.project_id::uuid
  JOIN agent_inbox_event AS task_owner
    ON task_owner.id = segment.agent_run_id::uuid
)
UPDATE interaction_dag_segment AS segment
SET workspace_id = ranked.workspace_id,
    generation = ranked.generation,
    project_id_at_event = ranked.project_id_at_event,
    channel_id_at_event = ranked.channel_id_at_event,
    route_generation_at_event = NULL,
    memory_type_at_event = 'legacy',
    graph_projection_eligible_at_event = false,
    close_action_kind = NULL,
    canonical_action_id = NULL,
    visible_action_key = NULL,
    derivative = false,
    trainable_eligible = false,
    publish_status = NULL,
    content_status = 'legacy_unverified',
    publish_seq = NULL,
    sanitizer_version = NULL,
    policy_version = NULL,
    provider_capture_status = 'not_expected',
    provider_capture_id = NULL,
    provider_capture_version = NULL,
    provider_capture_correlation_key = NULL,
    run_id = NULL,
    run_agent_id = NULL,
    published_at = NULL,
    retracted_at = NULL
FROM ranked
WHERE ranked.segment_id = segment.segment_id;

ALTER TABLE interaction_dag_segment
  ALTER COLUMN project_id DROP NOT NULL,
  ALTER COLUMN agent_run_id TYPE uuid USING agent_run_id::uuid,
  ALTER COLUMN workspace_id SET NOT NULL,
  ALTER COLUMN generation SET NOT NULL,
  ALTER COLUMN memory_type_at_event SET NOT NULL,
  ALTER COLUMN graph_projection_eligible_at_event SET NOT NULL,
  ALTER COLUMN derivative SET NOT NULL,
  ALTER COLUMN trainable_eligible SET NOT NULL,
  ALTER COLUMN content_status SET NOT NULL,
  ALTER COLUMN provider_capture_status SET NOT NULL;

-- Preserve migration 205 source validation for legacy rows. Canonical
-- task-message Segments may acquire a tensor during the publish pipeline while
-- retaining their committed source provenance and NULL legacy trajectory ID.
ALTER TABLE interaction_dag_segment
  DROP CONSTRAINT ck_segment_source_valid,
  ADD CONSTRAINT ck_segment_source_valid CHECK (
    (content_status = 'legacy_unverified' AND (
      (trajectory_source = 'areal_tensor'
       AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
      OR
      (trajectory_source = 'task_messages'
       AND trajectory_id IS NULL AND tensor_ref IS NULL)
    ))
    OR
    (content_status <> 'legacy_unverified' AND (
      (trajectory_source = 'areal_tensor'
       AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
      OR
      (trajectory_source = 'task_messages' AND trajectory_id IS NULL)
    ))
  );

ALTER TABLE interaction_dag_segment
  ADD CONSTRAINT interaction_dag_segment_workspace_fk
    FOREIGN KEY (workspace_id) REFERENCES workspace(id),
  ADD CONSTRAINT interaction_dag_segment_agent_run_fk
    FOREIGN KEY (agent_run_id) REFERENCES agent_inbox_event(id),
  ADD CONSTRAINT interaction_dag_segment_project_event_fk
    FOREIGN KEY (project_id_at_event) REFERENCES project(id),
  ADD CONSTRAINT interaction_dag_segment_channel_event_fk
    FOREIGN KEY (channel_id_at_event) REFERENCES channel(id),
  ADD CONSTRAINT interaction_dag_segment_run_fk
    FOREIGN KEY (run_id) REFERENCES env_dispatch_run(run_id),
  ADD CONSTRAINT interaction_dag_segment_run_agent_fk
    FOREIGN KEY (run_id, run_agent_id)
    REFERENCES env_dispatch_run_agent(run_id, run_agent_id),
  ADD CONSTRAINT interaction_dag_segment_generation_check
    CHECK (generation > 0),
  ADD CONSTRAINT interaction_dag_segment_range_check
    CHECK (
      (content_status = 'legacy_unverified' AND start_seq >= 0 AND end_seq >= start_seq)
      OR
      (content_status <> 'legacy_unverified' AND close_action_kind = 'metadata_only'
       AND start_seq = 0 AND end_seq = 0)
      OR
      (content_status <> 'legacy_unverified' AND close_action_kind <> 'metadata_only'
       AND start_seq > 0 AND end_seq >= start_seq)
    ),
  ADD CONSTRAINT interaction_dag_segment_close_action_check
    CHECK (
      (content_status = 'legacy_unverified'
       AND close_action_kind IS NULL
       AND canonical_action_id IS NULL
       AND visible_action_key IS NULL
       AND publish_status IS NULL
       AND publish_seq IS NULL
       AND graph_projection_eligible_at_event = false
       AND trainable_eligible = false)
      OR
      (content_status <> 'legacy_unverified'
       AND close_action_kind IN ('message', 'reaction', 'terminal', 'metadata_only')
       AND (
         (close_action_kind IN ('message', 'reaction') AND canonical_action_id IS NOT NULL)
         OR
         (close_action_kind IN ('terminal', 'metadata_only') AND canonical_action_id IS NULL)
       )
       AND (
         (close_action_kind = 'metadata_only' AND visible_action_key IS NULL)
         OR
         (close_action_kind <> 'metadata_only'
          AND visible_action_key IS NOT NULL
          AND length(btrim(visible_action_key)) > 0)
       )
       AND publish_status IS NOT NULL)
    ),
  ADD CONSTRAINT interaction_dag_segment_content_status_check
    CHECK (content_status IN (
      'legacy_unverified', 'pending', 'published', 'empty',
      'redaction_failed', 'rejected_scope', 'dead_letter', 'retracted'
    )),
  ADD CONSTRAINT interaction_dag_segment_publish_status_check
    CHECK (publish_status IS NULL OR publish_status IN (
      'pending', 'processing', 'retry', 'published',
      'redaction_failed', 'rejected_scope', 'dead_letter', 'retracted'
    )),
  ADD CONSTRAINT interaction_dag_segment_publish_seq_check
    CHECK (publish_seq IS NULL OR publish_seq > 0),
  ADD CONSTRAINT interaction_dag_segment_published_metadata_check
    CHECK (
      (publish_status <> 'published' AND content_status <> 'published')
      OR (publish_seq IS NOT NULL AND publish_seq > 0 AND published_at IS NOT NULL)
    ),
  ADD CONSTRAINT interaction_dag_segment_retracted_metadata_check
    CHECK (
      (publish_status <> 'retracted' AND content_status <> 'retracted')
      OR retracted_at IS NOT NULL
    ),
  ADD CONSTRAINT interaction_dag_segment_provider_status_check
    CHECK (provider_capture_status IN ('not_expected', 'pending', 'finalized', 'conflict')),
  ADD CONSTRAINT interaction_dag_segment_provider_identity_check
    CHECK (
      (provider_capture_status IN ('not_expected', 'pending')
       AND provider_capture_id IS NULL
       AND provider_capture_version IS NULL)
      OR
      (provider_capture_status IN ('finalized', 'conflict')
       AND provider_capture_id IS NOT NULL
       AND length(btrim(provider_capture_id)) > 0
       AND provider_capture_version IS NOT NULL
       AND provider_capture_version > 0)
    ),
  ADD CONSTRAINT interaction_dag_segment_provider_version_check
    CHECK (provider_capture_version IS NULL OR provider_capture_version > 0),
  ADD CONSTRAINT interaction_dag_segment_provider_correlation_check
    CHECK (
      (provider_capture_status = 'not_expected'
       AND provider_capture_correlation_key IS NULL)
      OR
      (provider_capture_status IN ('pending', 'finalized', 'conflict')
       AND provider_capture_correlation_key IS NOT NULL
       AND length(btrim(provider_capture_correlation_key)) > 0)
    ),
  ADD CONSTRAINT interaction_dag_segment_run_identity_check
    CHECK ((run_id IS NULL) = (run_agent_id IS NULL));

CREATE UNIQUE INDEX interaction_dag_segment_workspace_generation_uidx
  ON interaction_dag_segment (workspace_id, agent_run_id, generation);

CREATE UNIQUE INDEX interaction_dag_segment_workspace_visible_action_uidx
  ON interaction_dag_segment (workspace_id, visible_action_key)
  WHERE visible_action_key IS NOT NULL;

CREATE UNIQUE INDEX interaction_dag_segment_workspace_segment_uidx
  ON interaction_dag_segment (workspace_id, segment_id);

CREATE INDEX interaction_dag_segment_canonical_range_guard_idx
  ON interaction_dag_segment (agent_run_id, start_seq, end_seq)
  WHERE content_status NOT IN ('legacy_unverified', 'empty');

CREATE OR REPLACE FUNCTION validate_universal_dag_segment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  task_workspace_id uuid;
  project_workspace_id uuid;
  channel_workspace_id uuid;
  channel_project_id uuid;
  run_workspace_id uuid;
  message_count bigint;
  distinct_message_count bigint;
  minimum_message_seq integer;
  maximum_message_seq integer;
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.content_status <> 'legacy_unverified' THEN
      RAISE EXCEPTION 'canonical universal DAG segment deletion is forbidden';
    END IF;
    RETURN OLD;
  END IF;

  IF TG_OP = 'INSERT' AND NEW.content_status <> 'legacy_unverified' THEN
    IF NEW.publish_status <> 'pending'
       OR NEW.content_status NOT IN ('pending', 'empty')
       OR (NEW.content_status = 'empty' AND NEW.close_action_kind <> 'metadata_only')
       OR (NEW.content_status = 'pending' AND NEW.close_action_kind = 'metadata_only')
       OR NEW.publish_seq IS NOT NULL
       OR NEW.published_at IS NOT NULL
       OR NEW.retracted_at IS NOT NULL
       OR NEW.provider_capture_status NOT IN ('not_expected', 'pending') THEN
      RAISE EXCEPTION 'canonical universal DAG segment initial lifecycle state is invalid';
    END IF;
  END IF;

  IF TG_OP = 'UPDATE'
     AND OLD.content_status <> 'legacy_unverified'
     AND NEW.content_status = 'legacy_unverified' THEN
    RAISE EXCEPTION 'canonical universal DAG segment cannot become legacy unverified';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF NEW.segment_id IS DISTINCT FROM OLD.segment_id
       OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.agent_run_id IS DISTINCT FROM OLD.agent_run_id
       OR NEW.generation IS DISTINCT FROM OLD.generation
       OR NEW.start_seq IS DISTINCT FROM OLD.start_seq
       OR NEW.end_seq IS DISTINCT FROM OLD.end_seq
       OR NEW.project_id_at_event IS DISTINCT FROM OLD.project_id_at_event
       OR NEW.channel_id_at_event IS DISTINCT FROM OLD.channel_id_at_event
       OR NEW.route_generation_at_event IS DISTINCT FROM OLD.route_generation_at_event
       OR NEW.issue_id IS DISTINCT FROM OLD.issue_id
       OR NEW.trajectory_source IS DISTINCT FROM OLD.trajectory_source
       OR NEW.close_action_kind IS DISTINCT FROM OLD.close_action_kind
       OR NEW.canonical_action_id IS DISTINCT FROM OLD.canonical_action_id
       OR NEW.visible_action_key IS DISTINCT FROM OLD.visible_action_key
       OR NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.run_agent_id IS DISTINCT FROM OLD.run_agent_id
       OR NEW.memory_type_at_event IS DISTINCT FROM OLD.memory_type_at_event
       OR NEW.graph_projection_eligible_at_event IS DISTINCT FROM OLD.graph_projection_eligible_at_event
       OR NEW.trainable_eligible IS DISTINCT FROM OLD.trainable_eligible
       OR NEW.derivative IS DISTINCT FROM OLD.derivative THEN
      RAISE EXCEPTION 'universal DAG segment provenance is immutable';
    END IF;

    IF NEW.content_status IS DISTINCT FROM OLD.content_status
       AND NOT (
         (OLD.content_status = 'pending'
          AND NEW.content_status IN (
            'published', 'redaction_failed', 'rejected_scope', 'dead_letter'
          ))
         OR (OLD.content_status IN ('published', 'empty')
             AND NEW.content_status = 'retracted')
       ) THEN
      RAISE EXCEPTION 'universal DAG segment content lifecycle transition is invalid';
    END IF;

    IF NEW.publish_status IS DISTINCT FROM OLD.publish_status
       AND NOT (
         (OLD.publish_status = 'pending' AND NEW.publish_status = 'processing')
         OR (OLD.publish_status = 'processing'
             AND NEW.publish_status IN (
               'retry', 'published', 'redaction_failed',
               'rejected_scope', 'dead_letter'
             ))
         OR (OLD.publish_status = 'retry' AND NEW.publish_status = 'processing')
         OR (OLD.publish_status = 'published' AND NEW.publish_status = 'retracted')
       ) THEN
      RAISE EXCEPTION 'universal DAG segment publish lifecycle transition is invalid';
    END IF;

    IF NEW.publish_seq IS DISTINCT FROM OLD.publish_seq
       AND NOT (
         OLD.publish_seq IS NULL
         AND NEW.publish_seq IS NOT NULL
         AND NEW.publish_status = 'published'
       ) THEN
      RAISE EXCEPTION 'universal DAG segment publish sequence is immutable';
    END IF;

    IF NEW.provider_capture_status IS DISTINCT FROM OLD.provider_capture_status
       AND NOT (
         (OLD.provider_capture_status = 'not_expected'
          AND NEW.provider_capture_status = 'pending')
         OR (OLD.provider_capture_status = 'pending'
             AND NEW.provider_capture_status IN ('finalized', 'conflict'))
         OR (OLD.provider_capture_status = 'finalized'
             AND NEW.provider_capture_status = 'conflict')
       ) THEN
      RAISE EXCEPTION 'universal DAG segment provider capture transition is invalid';
    END IF;

    IF NEW.provider_capture_correlation_key IS DISTINCT FROM OLD.provider_capture_correlation_key
       AND NOT (
         OLD.provider_capture_status = 'not_expected'
         AND NEW.provider_capture_status = 'pending'
         AND OLD.provider_capture_correlation_key IS NULL
         AND NEW.provider_capture_correlation_key IS NOT NULL
       ) THEN
      RAISE EXCEPTION 'universal DAG segment provider correlation is immutable';
    END IF;

    IF (NEW.provider_capture_id IS DISTINCT FROM OLD.provider_capture_id
        OR NEW.provider_capture_version IS DISTINCT FROM OLD.provider_capture_version)
       AND NOT (
         OLD.provider_capture_status = 'pending'
         AND NEW.provider_capture_status IN ('finalized', 'conflict')
         AND OLD.provider_capture_id IS NULL
         AND OLD.provider_capture_version IS NULL
         AND NEW.provider_capture_id IS NOT NULL
         AND NEW.provider_capture_version IS NOT NULL
       ) THEN
      RAISE EXCEPTION 'universal DAG segment provider capture identity is immutable';
    END IF;
  END IF;

  SELECT workspace_id INTO task_workspace_id
  FROM agent_inbox_event
  WHERE id = NEW.agent_run_id;
  IF NOT FOUND OR task_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
    RAISE EXCEPTION 'universal DAG segment task ownership is invalid';
  END IF;

  IF NEW.project_id_at_event IS NOT NULL THEN
    SELECT workspace_id INTO project_workspace_id
    FROM project
    WHERE id = NEW.project_id_at_event;
    IF NOT FOUND OR project_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
      RAISE EXCEPTION 'universal DAG segment project ownership is invalid';
    END IF;
  END IF;

  IF NEW.channel_id_at_event IS NOT NULL THEN
    SELECT workspace_id, project_id
      INTO channel_workspace_id, channel_project_id
    FROM channel
    WHERE id = NEW.channel_id_at_event;
    IF NOT FOUND OR channel_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
      RAISE EXCEPTION 'universal DAG segment channel ownership is invalid';
    END IF;
    IF NEW.project_id_at_event IS NOT NULL
       AND channel_project_id IS DISTINCT FROM NEW.project_id_at_event THEN
      RAISE EXCEPTION 'universal DAG segment channel scope is invalid';
    END IF;
  END IF;

  IF NEW.run_id IS NOT NULL THEN
    SELECT dispatch.workspace_id INTO run_workspace_id
    FROM env_dispatch_run AS dispatch
    JOIN env_dispatch_run_agent AS run_agent
      ON run_agent.run_id = dispatch.run_id
    WHERE dispatch.run_id = NEW.run_id
      AND run_agent.run_agent_id = NEW.run_agent_id;
    IF NOT FOUND OR run_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
      RAISE EXCEPTION 'universal DAG segment run ownership is invalid';
    END IF;
  END IF;

  IF NEW.content_status <> 'legacy_unverified'
     AND NEW.close_action_kind <> 'metadata_only' THEN
    -- Serialize range creation with task-message identity mutation. The reverse
    -- trigger below protects the range after this Segment becomes visible.
    PERFORM 1
    FROM task_message
    WHERE task_id = NEW.agent_run_id
      AND seq BETWEEN NEW.start_seq AND NEW.end_seq
    FOR SHARE;

    SELECT count(*)::bigint,
           count(DISTINCT seq)::bigint,
           min(seq)::integer,
           max(seq)::integer
      INTO message_count, distinct_message_count,
           minimum_message_seq, maximum_message_seq
    FROM task_message
    WHERE task_id = NEW.agent_run_id
      AND seq BETWEEN NEW.start_seq AND NEW.end_seq;

    IF message_count <> (NEW.end_seq::bigint - NEW.start_seq::bigint + 1)
       OR distinct_message_count <> (NEW.end_seq::bigint - NEW.start_seq::bigint + 1)
       OR minimum_message_seq IS DISTINCT FROM NEW.start_seq
       OR maximum_message_seq IS DISTINCT FROM NEW.end_seq THEN
      RAISE EXCEPTION 'universal DAG segment canonical range is invalid';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_dag_segment_validate
BEFORE INSERT OR UPDATE OR DELETE ON interaction_dag_segment
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_segment();

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_workspace_id_id_454_key
  UNIQUE (workspace_id, id);

CREATE TABLE interaction_dag_segment_generation_sequence (
  workspace_id uuid NOT NULL,
  agent_run_id uuid NOT NULL,
  next_generation bigint NOT NULL DEFAULT 1 CHECK (next_generation > 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_run_id),
  CONSTRAINT interaction_dag_segment_generation_sequence_task_fk
    FOREIGN KEY (workspace_id, agent_run_id)
    REFERENCES agent_inbox_event(workspace_id, id)
    ON DELETE CASCADE
);

CREATE TABLE interaction_dag_task_cursor (
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  agent_run_id uuid NOT NULL REFERENCES agent_inbox_event(id),
  next_generation bigint NOT NULL DEFAULT 1 CHECK (next_generation > 0),
  open_start_seq integer CHECK (open_start_seq IS NULL OR open_start_seq > 0),
  last_closed_seq integer NOT NULL DEFAULT 0 CHECK (last_closed_seq >= 0),
  open_generation bigint CHECK (open_generation IS NULL OR open_generation > 0),
  open_end_seq integer CHECK (open_end_seq IS NULL OR open_end_seq >= open_start_seq),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, agent_run_id),
  CONSTRAINT interaction_dag_task_cursor_open_state_check CHECK (
    (open_start_seq IS NULL AND open_generation IS NULL AND open_end_seq IS NULL)
    OR
    (open_start_seq IS NOT NULL
     AND open_generation = next_generation
     AND open_end_seq IS NOT NULL
     AND open_start_seq > last_closed_seq
     AND open_end_seq >= open_start_seq)
  )
);

CREATE OR REPLACE FUNCTION validate_universal_dag_task_cursor()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  task_workspace_id uuid;
BEGIN
  SELECT workspace_id INTO task_workspace_id
  FROM agent_inbox_event
  WHERE id = NEW.agent_run_id;
  IF NOT FOUND OR task_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
    RAISE EXCEPTION 'universal DAG cursor task ownership is invalid';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF NEW.next_generation < OLD.next_generation
       OR NEW.last_closed_seq < OLD.last_closed_seq THEN
      RAISE EXCEPTION 'universal DAG cursor state cannot regress';
    END IF;

    IF OLD.open_generation IS NOT NULL AND NEW.open_generation IS NOT NULL THEN
      IF NEW.open_generation <> OLD.open_generation
         OR NEW.open_start_seq IS DISTINCT FROM OLD.open_start_seq
         OR NEW.open_end_seq < OLD.open_end_seq THEN
        RAISE EXCEPTION 'universal DAG open cursor range is incoherent';
      END IF;
    ELSIF OLD.open_generation IS NOT NULL AND NEW.open_generation IS NULL THEN
      IF NEW.last_closed_seq < OLD.open_end_seq
         OR NEW.next_generation <= OLD.open_generation THEN
        RAISE EXCEPTION 'universal DAG cursor close transition is incoherent';
      END IF;
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_dag_task_cursor_validate
BEFORE INSERT OR UPDATE ON interaction_dag_task_cursor
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_task_cursor();

CREATE TABLE interaction_dag_publish_outbox (
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  segment_id text NOT NULL,
  request_hash text NOT NULL CHECK (length(btrim(request_hash)) > 0),
  status text NOT NULL CHECK (status IN (
    'pending', 'processing', 'retry', 'published',
    'redaction_failed', 'rejected_scope', 'dead_letter', 'retracted'
  )),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz,
  lease_owner text,
  lease_expires_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  PRIMARY KEY (workspace_id, segment_id),
  UNIQUE (segment_id),
  CONSTRAINT interaction_dag_publish_outbox_segment_fk
    FOREIGN KEY (segment_id) REFERENCES interaction_dag_segment(segment_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT interaction_dag_publish_outbox_lease_check CHECK (
    (lease_owner IS NULL AND lease_expires_at IS NULL)
    OR
    (lease_owner IS NOT NULL AND length(btrim(lease_owner)) > 0
     AND lease_expires_at IS NOT NULL)
  )
);

CREATE OR REPLACE FUNCTION validate_universal_dag_publish_outbox_lifecycle()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'pending'
       OR NEW.attempts <> 0
       OR NEW.lease_owner IS NOT NULL
       OR NEW.lease_expires_at IS NOT NULL
       OR NEW.completed_at IS NOT NULL THEN
      RAISE EXCEPTION 'universal DAG publish outbox initial lifecycle state is invalid';
    END IF;
  ELSE
    IF NEW.workspace_id IS DISTINCT FROM OLD.workspace_id
       OR NEW.segment_id IS DISTINCT FROM OLD.segment_id
       OR NEW.request_hash IS DISTINCT FROM OLD.request_hash THEN
      RAISE EXCEPTION 'universal DAG publish outbox creation identity is immutable';
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status
       AND NOT (
         (OLD.status = 'pending' AND NEW.status = 'processing')
         OR (OLD.status = 'processing' AND NEW.status IN (
           'retry', 'published', 'redaction_failed',
           'rejected_scope', 'dead_letter'
         ))
         OR (OLD.status = 'retry' AND NEW.status = 'processing')
         OR (OLD.status = 'published' AND NEW.status = 'retracted')
       ) THEN
      RAISE EXCEPTION 'universal DAG publish outbox lifecycle transition is invalid';
    END IF;

    IF NEW.attempts IS DISTINCT FROM OLD.attempts
       AND NOT (
         OLD.status = 'processing'
         AND NEW.status = 'retry'
         AND NEW.attempts = OLD.attempts + 1
       ) THEN
      RAISE EXCEPTION 'universal DAG publish outbox attempt count is invalid';
    END IF;

    IF OLD.status = 'processing' AND NEW.status = 'retry'
       AND NEW.attempts <> OLD.attempts + 1 THEN
      RAISE EXCEPTION 'universal DAG publish outbox retry must increment attempts';
    END IF;

    IF OLD.status = 'published' AND NEW.status = 'retracted'
       AND NEW.completed_at IS NOT DISTINCT FROM OLD.completed_at THEN
      RAISE EXCEPTION 'universal DAG publish outbox retraction must record completion';
    END IF;
  END IF;

  IF NEW.status = 'processing' THEN
    IF NEW.lease_owner IS NULL
       OR NEW.lease_expires_at IS NULL
       OR NEW.completed_at IS NOT NULL THEN
      RAISE EXCEPTION 'universal DAG publish outbox processing metadata is invalid';
    END IF;
  ELSIF NEW.status = 'retry' THEN
    IF NEW.lease_owner IS NOT NULL
       OR NEW.lease_expires_at IS NOT NULL
       OR NEW.next_attempt_at IS NULL
       OR NEW.completed_at IS NOT NULL THEN
      RAISE EXCEPTION 'universal DAG publish outbox retry metadata is invalid';
    END IF;
  ELSIF NEW.status IN (
    'published', 'redaction_failed', 'rejected_scope', 'dead_letter', 'retracted'
  ) THEN
    IF NEW.lease_owner IS NOT NULL
       OR NEW.lease_expires_at IS NOT NULL
       OR NEW.completed_at IS NULL THEN
      RAISE EXCEPTION 'universal DAG publish outbox completion metadata is invalid';
    END IF;
  ELSE
    IF NEW.lease_owner IS NOT NULL
       OR NEW.lease_expires_at IS NOT NULL
       OR NEW.completed_at IS NOT NULL THEN
      RAISE EXCEPTION 'universal DAG publish outbox pending metadata is invalid';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_dag_publish_outbox_lifecycle_validate
BEFORE INSERT OR UPDATE ON interaction_dag_publish_outbox
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_publish_outbox_lifecycle();

CREATE OR REPLACE FUNCTION validate_universal_dag_segment_outbox()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.content_status <> 'legacy_unverified'
     AND NOT EXISTS (
       SELECT 1
       FROM interaction_dag_publish_outbox AS outbox
       WHERE outbox.workspace_id = NEW.workspace_id
         AND outbox.segment_id = NEW.segment_id
     ) THEN
    RAISE EXCEPTION 'canonical universal DAG segment requires an outbox row';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER interaction_dag_segment_outbox_guard
AFTER INSERT OR UPDATE ON interaction_dag_segment
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_segment_outbox();

CREATE OR REPLACE FUNCTION validate_universal_dag_outbox_segment()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_workspace_id uuid;
  scoped_segment_id text;
  segment_workspace_id uuid;
  segment_content_status text;
BEGIN
  IF TG_OP = 'DELETE' THEN
    scoped_workspace_id := OLD.workspace_id;
    scoped_segment_id := OLD.segment_id;
  ELSE
    scoped_workspace_id := NEW.workspace_id;
    scoped_segment_id := NEW.segment_id;
  END IF;

  SELECT workspace_id, content_status
    INTO segment_workspace_id, segment_content_status
  FROM interaction_dag_segment
  WHERE segment_id = scoped_segment_id;

  IF TG_OP = 'DELETE' THEN
    IF FOUND AND segment_content_status <> 'legacy_unverified' THEN
      RAISE EXCEPTION 'canonical universal DAG segment requires an outbox row';
    END IF;
    RETURN OLD;
  END IF;

  IF NOT FOUND
     OR segment_workspace_id IS DISTINCT FROM scoped_workspace_id
     OR segment_content_status = 'legacy_unverified' THEN
    RAISE EXCEPTION 'universal DAG outbox segment identity is invalid';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER interaction_dag_outbox_segment_guard
AFTER INSERT OR UPDATE OR DELETE ON interaction_dag_publish_outbox
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_outbox_segment();

CREATE TABLE interaction_dag_edge_sequence (
  workspace_id uuid PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
  next_edge_seq bigint NOT NULL DEFAULT 1 CHECK (next_edge_seq > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE interaction_dag_edge
  ADD COLUMN workspace_id uuid,
  ADD COLUMN edge_seq bigint,
  ADD COLUMN trigger_message_id uuid,
  ALTER COLUMN project_id DROP NOT NULL;

ALTER TABLE interaction_dag_edge
  ALTER COLUMN workspace_id SET NOT NULL,
  ALTER COLUMN edge_seq SET NOT NULL,
  DROP CONSTRAINT interaction_dag_edge_type_check,
  ADD CONSTRAINT interaction_dag_edge_workspace_fk
    FOREIGN KEY (workspace_id) REFERENCES workspace(id),
  ADD CONSTRAINT interaction_dag_edge_seq_check
    CHECK (edge_seq > 0),
  ADD CONSTRAINT interaction_dag_edge_type_check
    CHECK (type IN ('continues', 'responds_to', 'delegates_to', 'mentions')),
  ADD CONSTRAINT interaction_dag_edge_trigger_shape_check
    CHECK (
      (type = 'continues' AND trigger_message_id IS NULL)
      OR
      (type IN ('responds_to', 'delegates_to', 'mentions')
       AND trigger_message_id IS NOT NULL)
    ),
  ADD CONSTRAINT interaction_dag_edge_workspace_seq_unique
    UNIQUE (workspace_id, edge_seq),
  ADD CONSTRAINT interaction_dag_edge_source_fk
    FOREIGN KEY (workspace_id, src_segment_id)
    REFERENCES interaction_dag_segment(workspace_id, segment_id)
    ON DELETE RESTRICT,
  ADD CONSTRAINT interaction_dag_edge_target_fk
    FOREIGN KEY (workspace_id, dst_segment_id)
    REFERENCES interaction_dag_segment(workspace_id, segment_id)
    ON DELETE RESTRICT,
  ADD CONSTRAINT interaction_dag_edge_trigger_message_fk
    FOREIGN KEY (trigger_message_id)
    REFERENCES task_message(id)
    ON DELETE RESTRICT;

CREATE INDEX interaction_dag_edge_source_fk_idx
  ON interaction_dag_edge (workspace_id, src_segment_id);
CREATE INDEX interaction_dag_edge_target_fk_idx
  ON interaction_dag_edge (workspace_id, dst_segment_id);
CREATE INDEX interaction_dag_edge_trigger_message_fk_idx
  ON interaction_dag_edge (trigger_message_id)
  WHERE trigger_message_id IS NOT NULL;

CREATE OR REPLACE FUNCTION validate_universal_dag_edge()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  source_workspace_id uuid;
  source_agent_run_id uuid;
  source_start_seq integer;
  source_end_seq integer;
  target_workspace_id uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'canonical universal DAG edge deletion is forbidden';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION 'canonical universal DAG edge provenance is immutable';
  END IF;

  SELECT workspace_id, agent_run_id, start_seq, end_seq
    INTO source_workspace_id, source_agent_run_id, source_start_seq, source_end_seq
  FROM interaction_dag_segment
  WHERE segment_id = NEW.src_segment_id;
  IF NOT FOUND OR source_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
    RAISE EXCEPTION 'universal DAG edge source identity is invalid';
  END IF;

  SELECT workspace_id INTO target_workspace_id
  FROM interaction_dag_segment
  WHERE segment_id = NEW.dst_segment_id;
  IF NOT FOUND OR target_workspace_id IS DISTINCT FROM NEW.workspace_id THEN
    RAISE EXCEPTION 'universal DAG edge target identity is invalid';
  END IF;

  IF NEW.type <> 'continues' AND NOT EXISTS (
    SELECT 1
    FROM task_message AS message
    WHERE message.id = NEW.trigger_message_id
      AND message.task_id = source_agent_run_id
      AND message.seq BETWEEN source_start_seq AND source_end_seq
  ) THEN
    RAISE EXCEPTION 'universal DAG edge trigger provenance is invalid';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_dag_edge_validate
BEFORE INSERT OR UPDATE OR DELETE ON interaction_dag_edge
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_edge();

CREATE OR REPLACE FUNCTION guard_universal_dag_trigger_message_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE'
     OR NEW.task_id IS DISTINCT FROM OLD.task_id
     OR NEW.seq IS DISTINCT FROM OLD.seq THEN
    IF EXISTS (
      SELECT 1
      FROM interaction_dag_edge AS edge
      WHERE edge.trigger_message_id = OLD.id
    ) THEN
      RAISE EXCEPTION 'universal DAG edge trigger message provenance is immutable';
    END IF;

    IF EXISTS (
      SELECT 1
      FROM interaction_dag_segment AS segment
      WHERE segment.agent_run_id = OLD.task_id
        AND OLD.seq BETWEEN segment.start_seq AND segment.end_seq
        AND segment.content_status NOT IN ('legacy_unverified', 'empty')
    ) THEN
      RAISE EXCEPTION 'universal DAG canonical range task message provenance is immutable';
    END IF;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER task_message_universal_dag_trigger_guard
BEFORE UPDATE OR DELETE ON task_message
FOR EACH ROW EXECUTE FUNCTION guard_universal_dag_trigger_message_mutation();

CREATE TABLE interaction_dag_universal_provider_call (
  segment_id text NOT NULL REFERENCES interaction_dag_segment(segment_id) ON DELETE CASCADE,
  provider_call_id text NOT NULL REFERENCES pi_provider_call(call_id) ON DELETE CASCADE,
  role text NOT NULL CHECK (role IN ('owned', 'shared_producer', 'audit')),
  ordinal bigint NOT NULL CHECK (ordinal > 0),
  run_id uuid NOT NULL,
  run_agent_id uuid NOT NULL,
  capture_id text NOT NULL CHECK (length(btrim(capture_id)) > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (segment_id, provider_call_id),
  CONSTRAINT interaction_dag_provider_call_identity_fk
    FOREIGN KEY (run_id, run_agent_id, provider_call_id)
    REFERENCES pi_provider_call(run_id, run_agent_id, call_id)
    ON DELETE CASCADE
);

CREATE UNIQUE INDEX interaction_dag_universal_provider_one_owner_uidx
  ON interaction_dag_universal_provider_call (provider_call_id)
  WHERE role = 'owned';

CREATE OR REPLACE FUNCTION validate_universal_dag_provider_call()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  segment_run_id uuid;
  segment_run_agent_id uuid;
  segment_capture_id text;
  segment_capture_status text;
  call_ordinal bigint;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'universal DAG provider association deletion is forbidden';
  END IF;

  IF TG_OP = 'UPDATE' AND (
    NEW.segment_id IS DISTINCT FROM OLD.segment_id
    OR NEW.provider_call_id IS DISTINCT FROM OLD.provider_call_id
    OR NEW.role IS DISTINCT FROM OLD.role
    OR NEW.ordinal IS DISTINCT FROM OLD.ordinal
    OR NEW.run_id IS DISTINCT FROM OLD.run_id
    OR NEW.run_agent_id IS DISTINCT FROM OLD.run_agent_id
    OR NEW.capture_id IS DISTINCT FROM OLD.capture_id
    OR NEW.created_at IS DISTINCT FROM OLD.created_at
  ) THEN
    RAISE EXCEPTION 'universal DAG provider association provenance is immutable';
  END IF;

  SELECT run_id, run_agent_id, provider_capture_id, provider_capture_status
    INTO segment_run_id, segment_run_agent_id,
         segment_capture_id, segment_capture_status
  FROM interaction_dag_segment
  WHERE segment_id = NEW.segment_id;

  IF NOT FOUND
     OR segment_capture_status NOT IN ('finalized', 'conflict')
     OR segment_run_id IS DISTINCT FROM NEW.run_id
     OR segment_run_agent_id IS DISTINCT FROM NEW.run_agent_id
     OR segment_capture_id IS DISTINCT FROM NEW.capture_id THEN
    RAISE EXCEPTION 'universal DAG provider segment identity is invalid';
  END IF;

  SELECT provider.call_ordinal INTO call_ordinal
  FROM pi_provider_call AS provider
  WHERE provider.call_id = NEW.provider_call_id
    AND provider.run_id = NEW.run_id
    AND provider.run_agent_id = NEW.run_agent_id;

  IF NOT FOUND OR call_ordinal IS DISTINCT FROM NEW.ordinal THEN
    RAISE EXCEPTION 'universal DAG provider call identity is invalid';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER interaction_dag_universal_provider_call_validate
BEFORE INSERT OR UPDATE OR DELETE ON interaction_dag_universal_provider_call
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_provider_call();

CREATE OR REPLACE FUNCTION validate_universal_dag_shared_provider_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  scoped_provider_call_id text;
  scoped_run_id uuid;
BEGIN
  IF TG_OP = 'DELETE' THEN
    scoped_provider_call_id := OLD.provider_call_id;
    scoped_run_id := OLD.run_id;
  ELSE
    scoped_provider_call_id := NEW.provider_call_id;
    scoped_run_id := NEW.run_id;
  END IF;

  IF EXISTS (
    SELECT 1
    FROM interaction_dag_universal_provider_call AS association
    WHERE association.provider_call_id = scoped_provider_call_id
      AND association.run_id = scoped_run_id
      AND association.role = 'shared_producer'
  ) AND NOT EXISTS (
    SELECT 1
    FROM interaction_dag_universal_provider_call AS association
    WHERE association.provider_call_id = scoped_provider_call_id
      AND association.run_id = scoped_run_id
      AND association.role = 'owned'
  ) THEN
    RAISE EXCEPTION 'shared provider association requires a same-run owner';
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER interaction_dag_shared_provider_owner_guard
AFTER INSERT OR UPDATE OR DELETE ON interaction_dag_universal_provider_call
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_universal_dag_shared_provider_owner();

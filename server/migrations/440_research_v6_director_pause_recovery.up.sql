WITH recovered AS (
  UPDATE research_session session
  SET status = 'running',
      stop_reason = '',
      next_reconcile_at = now(),
      last_progress_at = now(),
      updated_at = now()
  WHERE session.orchestrator_version = 'research-run-v6'
    AND session.status = 'paused'
    AND session.stop_reason = ''
    AND COALESCE((
      SELECT control.create_enabled
      FROM research_v6_release_control control
      WHERE control.workspace_id = session.workspace_id
    ), true)
    AND EXISTS (
      SELECT 1
      FROM research_run_event event
      WHERE event.workspace_id = session.workspace_id
        AND event.session_id = session.id
        AND event.event_type = 'v6_pause_run'
        AND event.actor_type = 'director'
        AND NOT EXISTS (
          SELECT 1
          FROM research_run_event later
          WHERE later.workspace_id = event.workspace_id
            AND later.session_id = event.session_id
            AND later.sequence > event.sequence
            AND later.event_type IN ('run_paused', 'run_resumed', 'v6_resume_run')
        )
    )
  RETURNING session.workspace_id, session.id
), sequenced AS (
  SELECT recovered.workspace_id,
         recovered.id AS session_id,
         COALESCE((
           SELECT max(event.sequence)
           FROM research_run_event event
           WHERE event.session_id = recovered.id
         ), 0) + 1 AS sequence
  FROM recovered
)
INSERT INTO research_run_event (
  workspace_id,
  session_id,
  sequence,
  event_type,
  idempotency_key,
  actor_type,
  payload
)
SELECT workspace_id,
       session_id,
       sequence,
       'v6_director_pause_recovered',
       'v6-director-pause-recovered:migration-440',
       'system',
       '{"status":"running","reason":"autonomous Director pause removed"}'::jsonb
FROM sequenced
ON CONFLICT (session_id, idempotency_key) DO NOTHING;

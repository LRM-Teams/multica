-- Replace the former "text containing JSON" event convention with a typed
-- system_event part. This preserves every event fact while letting clients
-- select an event without parsing visible text.
WITH converted AS (
  SELECT
    m.id,
    jsonb_agg(
      CASE
        WHEN jsonb_typeof(payload.value) = 'object'
          AND jsonb_typeof(payload.value->'event') = 'string'
          AND jsonb_typeof(payload.value->'params') = 'object'
        THEN jsonb_build_object(
          'type', 'system_event',
          'event', payload.value->>'event',
          'event_params', payload.value->'params'
        )
        ELSE part.value
      END
      ORDER BY part.ordinality
    ) AS parts
  FROM channel_message m
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) WITH ORDINALITY AS part(value, ordinality)
  CROSS JOIN LATERAL (
    SELECT CASE
      WHEN part.value->>'text' LIKE '{"event":%'
      THEN (part.value->>'text')::jsonb
      ELSE NULL
    END AS value
  ) AS payload
  WHERE m.author_type = 'system'
  GROUP BY m.id
  HAVING bool_or(
    jsonb_typeof(payload.value) = 'object'
    AND jsonb_typeof(payload.value->'event') = 'string'
    AND jsonb_typeof(payload.value->'params') = 'object'
  )
)
UPDATE channel_message m
SET parts = converted.parts
FROM converted
WHERE m.id = converted.id;

-- Remove the legacy mention:// fallback from historical thread-unfollow rows
-- and add the same typed @handle reference written by new events. Agent rows
-- are retained after archival, so the event remains resolvable for history.
UPDATE channel_message m
SET
  content = '@' || COALESCE(NULLIF(a.name, ''), 'agent') || ' unfollowed this thread',
  parts = COALESCE(m.parts, '[]'::jsonb) || jsonb_build_array(jsonb_build_object(
    'type', 'reference',
    'ref_type', 'mention',
    'ref_subtype', 'agent',
    'ref_id', a.id::text,
    'label', '@' || COALESCE(NULLIF(a.name, ''), 'agent')
  ))
FROM agent a
WHERE m.author_type = 'system'
  AND m.workspace_id = a.workspace_id
  AND m.content LIKE '%unfollowed this thread'
  AND strpos(m.content, 'mention://agent/' || a.id::text || ')') > 0
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) AS part(value)
    WHERE part.value->>'type' = 'reference'
      AND part.value->>'ref_type' = 'mention'
      AND part.value->>'ref_subtype' = 'agent'
      AND part.value->>'ref_id' = a.id::text
  );

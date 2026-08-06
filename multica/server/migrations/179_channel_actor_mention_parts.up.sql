-- Channel messages used to persist actor mentions as mention:// markdown.
-- Convert that source syntax once, so readers only consume typed reference
-- parts and visible content stays normal readable text.
CREATE FUNCTION channel_message_utf16_length(value text)
RETURNS integer
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT COALESCE(SUM(CASE WHEN ascii(character) > 65535 THEN 2 ELSE 1 END), 0)::integer
  FROM regexp_split_to_table(value, '') AS character;
$$;

CREATE FUNCTION migrate_channel_actor_mention_content(value text)
RETURNS TABLE(content text, reference_parts jsonb)
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
  pattern constant text := '\[@?((\\.|[^]])+)\]\(mention://(member|agent|squad|all)/([0-9a-fA-F-]+|all)\)';
  scan_from integer := 1;
  cursor integer := 1;
  match_start integer;
  match_end integer;
  label text;
  mention_type text;
  mention_id text;
  visible_label text;
  start_utf16 integer;
BEGIN
  content := '';
  reference_parts := '[]'::jsonb;

  LOOP
    match_start := regexp_instr(value, pattern, scan_from, 1, 0, '', 0);
    EXIT WHEN match_start = 0;
    match_end := regexp_instr(value, pattern, scan_from, 1, 1, '', 0);
    label := regexp_substr(value, pattern, scan_from, 1, '', 1);
    mention_type := regexp_substr(value, pattern, scan_from, 1, '', 3);
    mention_id := regexp_substr(value, pattern, scan_from, 1, '', 4);

    visible_label := replace(replace(label, E'\\[', '['), E'\\]', ']');
    IF left(visible_label, 1) <> '@' THEN
      visible_label := '@' || visible_label;
    END IF;

    content := content || substring(value FROM cursor FOR match_start - cursor) || visible_label;
    IF mention_type <> 'all' THEN
      start_utf16 := channel_message_utf16_length(content) - channel_message_utf16_length(visible_label);
      reference_parts := reference_parts || jsonb_build_array(jsonb_build_object(
        'type', 'reference',
        'ref_type', 'mention',
        'ref_subtype', mention_type,
        'ref_id', mention_id,
        'label', visible_label,
        'content_start_utf16', start_utf16,
        'content_end_utf16', start_utf16 + channel_message_utf16_length(visible_label)
      ));
    END IF;

    cursor := match_end;
    scan_from := match_end;
  END LOOP;

  content := content || substring(value FROM cursor);
  RETURN NEXT;
END;
$$;

WITH migrated AS (
  SELECT
    m.id,
    converted.content,
    converted.reference_parts,
    COALESCE(rebuilt.parts, '[]'::jsonb) AS retained_parts
  FROM channel_message m
  CROSS JOIN LATERAL migrate_channel_actor_mention_content(m.content) AS converted
  LEFT JOIN LATERAL (
    SELECT jsonb_agg(
      CASE
        WHEN text_converted.content IS NOT NULL
        THEN jsonb_set(
          part.value,
          '{text}',
          to_jsonb(text_converted.content)
        )
        ELSE part.value
      END
      ORDER BY part.ordinality
    ) AS parts
    FROM jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) WITH ORDINALITY AS part(value, ordinality)
    LEFT JOIN LATERAL migrate_channel_actor_mention_content(part.value->>'text') AS text_converted
      ON part.value->>'type' = 'text'
      AND part.value->>'text' ~ 'mention://(member|agent|squad|all)/'
    -- Existing actor reference sidecars did not carry source spans. Replace
    -- them with the per-occurrence anchors rebuilt from canonical content.
    WHERE NOT (
      part.value->>'type' = 'reference'
      AND part.value->>'ref_type' = 'mention'
    )
  ) AS rebuilt ON true
  WHERE m.content ~ 'mention://(member|agent|squad|all)/'
     OR EXISTS (
       SELECT 1
       FROM jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) AS part(value)
       WHERE part.value->>'type' = 'text'
         AND part.value->>'text' ~ 'mention://(member|agent|squad|all)/'
     )
)
UPDATE channel_message m
SET
  content = migrated.content,
  parts = migrated.retained_parts || migrated.reference_parts
FROM migrated
WHERE m.id = migrated.id;

-- @all is no longer a channel mention type. Keep its visible text, but remove
-- any previously persisted broadcast sidecar even when the row did not use the
-- old markdown spelling (for example, an early structured write).
WITH rebuilt AS (
  SELECT
    m.id,
    COALESCE(
      jsonb_agg(part.value ORDER BY part.ordinality) FILTER (
        WHERE NOT (
          part.value->>'type' = 'reference'
          AND part.value->>'ref_type' = 'mention'
          AND part.value->>'ref_subtype' = 'all'
        )
      ),
      '[]'::jsonb
    ) AS parts
  FROM channel_message m
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(m.parts, '[]'::jsonb)) WITH ORDINALITY AS part(value, ordinality)
  GROUP BY m.id
  HAVING bool_or(
    part.value->>'type' = 'reference'
    AND part.value->>'ref_type' = 'mention'
    AND part.value->>'ref_subtype' = 'all'
  )
)
UPDATE channel_message m
SET parts = rebuilt.parts
FROM rebuilt
WHERE m.id = rebuilt.id;

DROP FUNCTION migrate_channel_actor_mention_content(text);
DROP FUNCTION channel_message_utf16_length(text);

-- Codex reports quota resets as English timestamps such as
-- "try again at Aug 20th, 2026 3:30 AM". Before this format was parsed,
-- these rows were stored with provider_blocked_until = NULL, whose meaning is
-- an unknown-end lock. Clear only those malformed Codex locks once so the
-- pending task can probe the provider again; if quota is still exhausted, the
-- next failure is persisted with the parsed reset timestamp.
UPDATE agent AS a
SET provider_blocked_until = NULL,
    provider_block_detail = '',
    status = CASE
      WHEN a.status <> 'blocked' THEN a.status
      WHEN EXISTS (
        SELECT 1
        FROM agent_inbox_event AS event
        WHERE event.agent_id = a.id
          AND event.status = 'draining'
      ) THEN 'working'
      ELSE 'idle'
    END,
    updated_at = now()
WHERE a.provider_blocked_until IS NULL
  AND a.provider_block_detail ~* 'try again at[[:space:]]+[[:alpha:]]+[[:space:]]+[0-9]{1,2}(st|nd|rd|th)?,[[:space:]]+20[0-9]{2}[[:space:]]+[0-9]{1,2}:[0-9]{2}';

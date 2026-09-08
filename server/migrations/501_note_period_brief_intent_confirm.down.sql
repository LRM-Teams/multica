-- Soft-confirm rows must leave awaiting_intent before the status check narrows.

UPDATE note_period_brief_prompt
SET status = 'cancelled'
WHERE status = 'awaiting_intent';

DROP INDEX IF EXISTS note_period_brief_prompt_active_session_idx;

CREATE UNIQUE INDEX note_period_brief_prompt_active_session_idx
    ON note_period_brief_prompt (chat_session_id)
    WHERE status = 'clarifying';

ALTER TABLE note_period_brief_prompt
    DROP COLUMN IF EXISTS source_ask;

ALTER TABLE note_period_brief_prompt
    DROP CONSTRAINT IF EXISTS note_period_brief_prompt_status_check;

ALTER TABLE note_period_brief_prompt
    ADD CONSTRAINT note_period_brief_prompt_status_check
    CHECK (status IN ('clarifying', 'consumed', 'cancelled'));

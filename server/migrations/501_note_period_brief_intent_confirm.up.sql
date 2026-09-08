-- Soft-confirm 写汇报 intent before opening the plan card.
-- awaiting_intent holds the original ask; clarifying remains the visible plan.

ALTER TABLE note_period_brief_prompt
    DROP CONSTRAINT IF EXISTS note_period_brief_prompt_status_check;

ALTER TABLE note_period_brief_prompt
    ADD CONSTRAINT note_period_brief_prompt_status_check
    CHECK (status IN ('clarifying', 'awaiting_intent', 'consumed', 'cancelled'));

ALTER TABLE note_period_brief_prompt
    ADD COLUMN IF NOT EXISTS source_ask TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS note_period_brief_prompt_active_session_idx;

CREATE UNIQUE INDEX note_period_brief_prompt_active_session_idx
    ON note_period_brief_prompt (chat_session_id)
    WHERE status IN ('clarifying', 'awaiting_intent');

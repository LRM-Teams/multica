DROP INDEX IF EXISTS idx_chat_session_context_note_page;
ALTER TABLE chat_session DROP COLUMN IF EXISTS context_note_page_id;

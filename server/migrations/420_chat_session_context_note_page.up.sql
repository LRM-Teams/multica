-- Notes assistant bubble: bind a standalone chat_session to a product note
-- page so the agent may read that page and its descendants under the
-- creator's note ACL (see resolveAgentNoteViewer).
ALTER TABLE chat_session
  ADD COLUMN context_note_page_id UUID REFERENCES note_page(id) ON DELETE SET NULL;

CREATE INDEX idx_chat_session_context_note_page
  ON chat_session (context_note_page_id)
  WHERE context_note_page_id IS NOT NULL;

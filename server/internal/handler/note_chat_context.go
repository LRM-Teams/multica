package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// notePageIsUnderRoot reports whether pageID is rootID or a descendant of
// rootID within the same workspace (deleted pages excluded).
func (h *Handler) notePageIsUnderRoot(ctx context.Context, pageID, rootID, workspaceID pgtype.UUID) (bool, error) {
	if !pageID.Valid || !rootID.Valid {
		return false, nil
	}
	if uuidToString(pageID) == uuidToString(rootID) {
		return true, nil
	}
	var ok bool
	err := h.DB.QueryRow(ctx, `
WITH RECURSIVE ancestors AS (
  SELECT id, parent_id, workspace_id
  FROM note_page
  WHERE id = $1 AND workspace_id = $3 AND deleted_at IS NULL
  UNION ALL
  SELECT p.id, p.parent_id, p.workspace_id
  FROM note_page p
  JOIN ancestors a ON p.id = a.parent_id
  WHERE p.workspace_id = $3 AND p.deleted_at IS NULL
)
SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $2)`, pageID, rootID, workspaceID).Scan(&ok)
	return ok, err
}

// resolveNoteChatSessionViewer finds an active chat_session for this agent
// whose context_note_page_id covers pageID (root or descendant). Returns the
// session creator as the note ACL viewer.
func (h *Handler) resolveNoteChatSessionViewer(
	ctx context.Context,
	agentID, workspaceID, pageID pgtype.UUID,
) (pgtype.UUID, bool, error) {
	rows, err := h.DB.Query(ctx, `
SELECT creator_id, context_note_page_id
FROM chat_session
WHERE agent_id = $1
  AND workspace_id = $2
  AND status = 'active'
  AND context_note_page_id IS NOT NULL
ORDER BY updated_at DESC
LIMIT 32`, agentID, workspaceID)
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	type sessionRoot struct {
		creatorID pgtype.UUID
		rootID    pgtype.UUID
	}
	sessions := make([]sessionRoot, 0)
	for rows.Next() {
		var session sessionRoot
		if err := rows.Scan(&session.creatorID, &session.rootID); err != nil {
			rows.Close()
			return pgtype.UUID{}, false, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return pgtype.UUID{}, false, err
	}
	// Drain the list cursor before ancestry lookups so nested QueryRow
	// cannot hold two pool connections (cursordeadlock / #1803).
	rows.Close()
	for _, session := range sessions {
		under, underErr := h.notePageIsUnderRoot(ctx, pageID, session.rootID, workspaceID)
		if underErr != nil {
			return pgtype.UUID{}, false, underErr
		}
		if under {
			return session.creatorID, true, nil
		}
	}
	return pgtype.UUID{}, false, nil
}

type agentNoteTreeNode struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id,omitempty"`
	Title    string  `json:"title"`
	Depth    int     `json:"depth"`
}

// listNoteSubtreeNodes returns the root page and all descendants (BFS by
// sort_key) as a flat outline. Caller must already authorize root access.
func (h *Handler) listNoteSubtreeNodes(ctx context.Context, rootID, workspaceID pgtype.UUID) ([]agentNoteTreeNode, error) {
	rows, err := h.DB.Query(ctx, `
WITH RECURSIVE subtree AS (
  SELECT id, parent_id, title, sort_key, 0 AS depth
  FROM note_page
  WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
  UNION ALL
  SELECT c.id, c.parent_id, c.title, c.sort_key, s.depth + 1
  FROM note_page c
  JOIN subtree s ON c.parent_id = s.id
  WHERE c.workspace_id = $2 AND c.deleted_at IS NULL
)
SELECT id, parent_id, title, depth
FROM subtree
ORDER BY depth ASC, sort_key ASC, title ASC`, rootID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentNoteTreeNode, 0)
	for rows.Next() {
		var id pgtype.UUID
		var parentID pgtype.UUID
		var title string
		var depth int
		if err := rows.Scan(&id, &parentID, &title, &depth); err != nil {
			return nil, err
		}
		node := agentNoteTreeNode{
			ID:    uuidToString(id),
			Title: title,
			Depth: depth,
		}
		if parentID.Valid {
			s := uuidToString(parentID)
			node.ParentID = &s
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func formatNoteTreeOutline(nodes []agentNoteTreeNode) string {
	if len(nodes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range nodes {
		indent := strings.Repeat("  ", n.Depth)
		fmt.Fprintf(&b, "%s- %s (%s)\n", indent, n.Title, n.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildNoteChatWakePrefix prepends machine-readable note context for a
// Notes assistant bubble delivery. Empty when the session has no note bind.
//
// Intentionally omits the full subtree outline and page bodies — the Notes
// Assistant must call `notes tree` / `notes get` for the pages it needs
// (see skill multica-notes-assistant / Selective note reads).
func (h *Handler) buildNoteChatWakePrefix(ctx context.Context, sessionID pgtype.UUID) string {
	var rootID pgtype.UUID
	var title string
	err := h.DB.QueryRow(ctx, `
SELECT cs.context_note_page_id, COALESCE(np.title, '')
FROM chat_session cs
LEFT JOIN note_page np ON np.id = cs.context_note_page_id AND np.deleted_at IS NULL
WHERE cs.id = $1`, sessionID).Scan(&rootID, &title)
	if err != nil || !rootID.Valid {
		return ""
	}
	var b strings.Builder
	b.WriteString("<note_chat_context>\n")
	b.WriteString("You are the Notes Assistant chatting about one product note page and its subtree.\n")
	fmt.Fprintf(&b, "context_note_page_id: %s\n", uuidToString(rootID))
	fmt.Fprintf(&b, "context_note_title: %s\n", title)
	b.WriteString("Selective note reads: do not assume child bodies are in context.\n")
	b.WriteString("Use `multica notes tree <page-id>` for ids+titles, then `multica notes get <page-id>` only for pages you need this turn.\n")
	b.WriteString("Read skill `multica-notes-assistant`. Stay within this subtree unless the human names another authorized page.\n")
	b.WriteString("</note_chat_context>\n\n")
	return b.String()
}

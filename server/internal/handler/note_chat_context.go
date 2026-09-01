package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
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

// periodBriefChatResidue is the compact 写汇报 leftover injected into a later
// Notes-bubble wake. It names the artifact; it does not replay collect/synth.
type periodBriefChatResidue struct {
	RunID         string
	Status        string
	WindowLabel   string
	DraftPageID   string
	DraftTitle    string
	ResultPageID  string
	ResultTitle   string
	ResultMode    string
	BriefMarkdown string
}

func formatPeriodBriefChatResidue(res periodBriefChatResidue) string {
	if strings.TrimSpace(res.RunID) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<period_brief_residue>\n")
	b.WriteString("This bubble session already ran 写汇报 (Period Work Brief) as a separate Worker wake. You did not live through collect or synthesis.\n")
	fmt.Fprintf(&b, "run_id: %s\n", res.RunID)
	fmt.Fprintf(&b, "status: %s\n", res.Status)
	if label := strings.TrimSpace(res.WindowLabel); label != "" {
		fmt.Fprintf(&b, "window: %s\n", label)
	}
	inserted := strings.TrimSpace(res.ResultMode)
	hasResult := strings.TrimSpace(res.ResultPageID) != ""
	switch {
	case (inserted == "append" || inserted == "child") && !hasResult:
		inserted = "deleted"
	case inserted != "append" && inserted != "child":
		inserted = "no"
	}
	fmt.Fprintf(&b, "inserted: %s\n", inserted)
	if hasResult {
		fmt.Fprintf(&b, "result_page_id: %s\n", res.ResultPageID)
		if title := strings.TrimSpace(res.ResultTitle); title != "" {
			fmt.Fprintf(&b, "result_page_title: %s\n", title)
		}
	}
	if strings.TrimSpace(res.DraftPageID) != "" {
		fmt.Fprintf(&b, "draft_page_id: %s\n", res.DraftPageID)
		if title := strings.TrimSpace(res.DraftTitle); title != "" {
			fmt.Fprintf(&b, "draft_page_title: %s\n", title)
		}
	}
	switch inserted {
	case "no":
		b.WriteString("The human has not inserted the brief into Notes yet. When they refer to this report, use <period_brief> below, or notes get draft_page_id.\n")
	case "deleted":
		b.WriteString("The inserted result page was deleted. When they refer to this report, use <period_brief> below, or notes get draft_page_id. Do not ask them to paste it.\n")
	default:
		b.WriteString("When the human refers to \"这个汇报\" / this report / the write result, use <period_brief> below. notes get result_page_id for a live copy if needed. Do not claim you collected or synthesized it yourself.\n")
	}
	if brief := strings.TrimSpace(res.BriefMarkdown); brief != "" {
		b.WriteString("<period_brief>\n")
		b.WriteString(brief)
		if !strings.HasSuffix(brief, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("</period_brief>\n")
	}
	b.WriteString("</period_brief_residue>\n\n")
	return b.String()
}

func (h *Handler) loadPeriodBriefChatResidue(ctx context.Context, sessionID pgtype.UUID) (periodBriefChatResidue, bool) {
	var res periodBriefChatResidue
	var liveResultID pgtype.UUID
	var resultMode string
	err := h.DB.QueryRow(ctx, `
SELECT r.id::text, r.status, r.window_label,
       r.draft_page_id::text, COALESCE(d.title, ''),
       rp.id, COALESCE(rp.title, ''), COALESCE(r.result_mode, ''),
       COALESCE(r.result_markdown, '')
FROM note_period_brief_run r
LEFT JOIN note_page d ON d.id = r.draft_page_id AND d.deleted_at IS NULL
LEFT JOIN note_page rp ON rp.id = r.result_page_id AND rp.deleted_at IS NULL
WHERE r.chat_session_id = $1
  AND r.status IN ('awaiting_confirm', 'done')
ORDER BY r.created_at DESC
LIMIT 1`, sessionID).Scan(
		&res.RunID, &res.Status, &res.WindowLabel,
		&res.DraftPageID, &res.DraftTitle,
		&liveResultID, &res.ResultTitle, &resultMode,
		&res.BriefMarkdown,
	)
	if err != nil {
		return periodBriefChatResidue{}, false
	}
	if liveResultID.Valid {
		res.ResultPageID = uuidToString(liveResultID)
	}
	res.ResultMode = strings.TrimSpace(resultMode)
	if strings.TrimSpace(res.BriefMarkdown) == "" {
		res.BriefMarkdown = h.loadPeriodBriefCardMarkdown(ctx, sessionID, res.DraftPageID)
	}
	return res, true
}

func (h *Handler) loadPeriodBriefCardMarkdown(ctx context.Context, sessionID pgtype.UUID, draftPageID string) string {
	rows, err := h.DB.Query(ctx, `
SELECT parts
FROM chat_message
WHERE chat_session_id = $1 AND role = 'assistant'
ORDER BY created_at DESC
LIMIT 16`, sessionID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	draftPageID = strings.TrimSpace(draftPageID)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return ""
		}
		for _, part := range messageparts.Decode(raw) {
			if part.Type != protocol.MessagePartTypeNoteBrief {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(part.Label), "采集包") {
				continue
			}
			if draftPageID != "" && strings.TrimSpace(part.RefID) != "" && strings.TrimSpace(part.RefID) != draftPageID {
				continue
			}
			if text := strings.TrimSpace(part.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

func (h *Handler) persistPeriodBriefResultMarkdown(ctx context.Context, runID pgtype.UUID, markdown string) error {
	_, err := h.DB.Exec(ctx, `
UPDATE note_period_brief_run
SET result_markdown = $2, updated_at = now()
WHERE id = $1`, runID, strings.TrimSpace(markdown))
	return err
}

// resolvePeriodBriefResidueViewer grants the exact draft or result page of an
// active bubble 写汇报 run — not that page's siblings or the 工作介绍/ tree.
func (h *Handler) resolvePeriodBriefResidueViewer(
	ctx context.Context,
	agentID, workspaceID, pageID pgtype.UUID,
) (pgtype.UUID, bool, error) {
	var ownerID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT r.owner_user_id
FROM note_period_brief_run r
JOIN chat_session cs ON cs.id = r.chat_session_id
WHERE cs.agent_id = $1
  AND cs.workspace_id = $2
  AND cs.status = 'active'
  AND r.status IN ('awaiting_confirm', 'done')
  AND (
    r.draft_page_id = $3
    OR r.result_page_id = $3
  )
ORDER BY r.created_at DESC
LIMIT 1`, agentID, workspaceID, pageID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, nil
	}
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	return ownerID, true, nil
}

// buildNoteChatWakePrefix prepends machine-readable note context for a
// Notes assistant bubble delivery. Empty when the session has no note bind.
//
// Intentionally omits the full subtree outline and page bodies — the Notes
// Assistant must call `notes tree` / `notes get` for the pages it needs
// (see skill multica-notes-assistant / Selective note reads).
// After a 写汇报 on the same session, appends <period_brief_residue> so
// follow-up Q&A can name the artifact without resuming collect/synthesis.
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
	if res, ok := h.loadPeriodBriefChatResidue(ctx, sessionID); ok {
		b.WriteString(formatPeriodBriefChatResidue(res))
	}
	return b.String()
}

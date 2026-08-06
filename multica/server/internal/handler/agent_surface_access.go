package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
)

// rejectAgentOnHumanRoute fails closed when an AgentPrincipal hits a human
// data-plane URL (not under /api/agent/). Shared helpers invoked from dedicated
// agent routes keep working so behavior can stay principal-native without
// duplicating every handler. Records metric for alias-zero observation.
// Returns true if the request was rejected (caller must return).
func rejectAgentOnHumanRoute(w http.ResponseWriter, r *http.Request, site string) bool {
	if _, ok := middleware.AgentPrincipalFromContext(r.Context()); !ok {
		return false
	}
	// Dedicated agent surface may call shared loaders/handlers.
	if strings.HasPrefix(r.URL.Path, "/api/agent/") {
		return false
	}
	middleware.RecordAgentHumanRouteHit(site)
	writeError(w, http.StatusForbidden, "agent must use dedicated /api/agent/* route")
	return true
}

// agentHasSurfaceAccess is the unified hard gate for agent data-plane access
// to a channel. ONLY current direct channel_member(agent) membership.
// Never owner human membership. Never source_agent_id env-dispatch fallback
// (that exception stays on explicit #channel output resolution only).
func (h *Handler) agentHasSurfaceAccess(ctx context.Context, workspaceID, agentID, channelID pgtype.UUID) bool {
	return agentHasDirectChannelMembership(ctx, h.DB, workspaceID, agentID, channelID)
}

func agentHasDirectChannelMembership(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, workspaceID, agentID, channelID pgtype.UUID) bool {
	if !workspaceID.Valid || !agentID.Valid || !channelID.Valid {
		return false
	}
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_member
			WHERE workspace_id = $1
			  AND channel_id = $2
			  AND member_type = 'agent'
			  AND member_id = $3
		)`, workspaceID, channelID, agentID).Scan(&exists)
	return err == nil && exists
}

// requireAgentPrincipal returns the AgentPrincipal or writes 403.
func (h *Handler) requireAgentPrincipal(w http.ResponseWriter, r *http.Request) (middleware.AgentPrincipal, bool) {
	p, ok := middleware.AgentPrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "agent principal required")
		return middleware.AgentPrincipal{}, false
	}
	return p, true
}

// requireAgentSurfaceAccessHTTP gates a channel for the current agent principal.
func (h *Handler) requireAgentSurfaceAccessHTTP(w http.ResponseWriter, r *http.Request, p middleware.AgentPrincipal, channelID pgtype.UUID) bool {
	ws, ok := p.WorkspaceUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return false
	}
	agentID, ok := p.AgentUUID()
	if !ok {
		writeError(w, http.StatusForbidden, "access denied")
		return false
	}
	if !h.channelExists(r.Context(), p.WorkspaceID, channelID) {
		writeError(w, http.StatusNotFound, "channel not found")
		return false
	}
	if !h.agentHasSurfaceAccess(r.Context(), ws, agentID, channelID) {
		writeError(w, http.StatusForbidden, "access denied")
		return false
	}
	return true
}

// agentAttachmentVisible decides whether an agent may read attachment
// metadata/content/download (#801). Contract (Barry/Parker):
//
//   - Visibility is the OR of *current* references — not historical membership.
//   - Channel-message references count only when the agent is a *direct*
//     channel_member(agent) of that channel (no source_agent / env-dispatch
//     borrow on this gate).
//   - Chat-session references count when the attachment is on a chat_message
//     in a session owned by this agent_id.
//   - Issue/comment attachments are workspace-visible under the current issue
//     product model (workspace-scoped, not channel-ACL).
//   - Orphan (no qualifying reference) → deny.
//   - If the agent's only qualifying channel reference is removed (leave/
//     remove membership or unlink), the next content/download re-check must
//     deny — callers must re-invoke this helper on every download, not cache
//     a prior metadata allow.
//
// Metadata GET and download paths both call this; never short-circuit download
// on a prior metadata pass.
func (h *Handler) agentAttachmentVisible(ctx context.Context, workspaceID, agentID, attachmentID pgtype.UUID) bool {
	if !workspaceID.Valid || !agentID.Valid || !attachmentID.Valid {
		return false
	}
	var ok bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message_attachment cma
			JOIN channel_message m ON m.id = cma.channel_message_id AND m.workspace_id = cma.workspace_id
			JOIN channel ch ON ch.id = m.channel_id AND ch.workspace_id = cma.workspace_id
			WHERE cma.attachment_id = $3
			  AND cma.workspace_id = $1
			  AND EXISTS (
			    SELECT 1 FROM channel_member cm
			    WHERE cm.channel_id = ch.id
			      AND cm.workspace_id = $1
			      AND cm.member_type = 'agent'
			      AND cm.member_id = $2
			  )
		)`, workspaceID, agentID, attachmentID).Scan(&ok)
	if err == nil && ok {
		return true
	}
	err = h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM attachment a
			JOIN chat_message m ON m.id = a.chat_message_id
			JOIN chat_session s ON s.id = m.session_id
			WHERE a.id = $3
			  AND a.workspace_id = $1
			  AND s.workspace_id = $1
			  AND s.agent_id = $2
		)`, workspaceID, agentID, attachmentID).Scan(&ok)
	if err == nil && ok {
		return true
	}
	err = h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM attachment a
			WHERE a.id = $2
			  AND a.workspace_id = $1
			  AND (a.issue_id IS NOT NULL OR a.comment_id IS NOT NULL)
		)`, workspaceID, attachmentID).Scan(&ok)
	if err == nil && ok {
		return true
	}
	// Upload provenance: attachment.channel_id set at upload time, agent is a
	// current direct member of that channel (Barry #801 counterfactual ②).
	err = h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM attachment a
			JOIN channel_member cm
			  ON cm.channel_id = a.channel_id
			 AND cm.workspace_id = a.workspace_id
			 AND cm.member_type = 'agent'
			 AND cm.member_id = $2
			WHERE a.id = $3
			  AND a.workspace_id = $1
			  AND a.channel_id IS NOT NULL
		)`, workspaceID, agentID, attachmentID).Scan(&ok)
	if err == nil && ok {
		return true
	}
	// Uploader-owned secure staging (Parker product a): ONLY truly unbound
	// orphans — no issue/comment/chat_session/channel FK and no channel_message
	// attachment row. After bind, visibility is reference rules only (remove
	// membership → DENY; no permanent uploader privilege).
	err = h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM attachment a
			WHERE a.id = $3
			  AND a.workspace_id = $1
			  AND a.uploader_type = 'agent'
			  AND a.uploader_id = $2
			  AND a.issue_id IS NULL
			  AND a.comment_id IS NULL
			  AND a.chat_session_id IS NULL
			  AND a.chat_message_id IS NULL
			  AND a.channel_id IS NULL
			  AND NOT EXISTS (
			    SELECT 1 FROM channel_message_attachment cma
			    WHERE cma.attachment_id = a.id
			      AND cma.workspace_id = a.workspace_id
			  )
		)`, workspaceID, agentID, attachmentID).Scan(&ok)
	return err == nil && ok
}

func revokeAgentChannelAccessTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID, agentID pgtype.UUID) error {
	// Order matters vs lease (Barry #801): terminalize events first so a concurrent
	// lease cannot INSERT a leased delivery against a still-pending head, then
	// fail deliveries and running executions.
	if _, err := tx.Exec(ctx, `
		UPDATE agent_inbox_event e
		SET status = 'acked',
		    completed_at = COALESCE(completed_at, now()),
		    terminal_at = COALESCE(terminal_at, now()),
		    acked_at = COALESCE(acked_at, now()),
		    terminal_outcome = COALESCE(terminal_outcome, 'failed'),
		    error = COALESCE(NULLIF(error, ''), 'agent removed from channel'),
		    failure_reason = COALESCE(NULLIF(failure_reason, ''), 'membership_revoked'),
		    retryable = false,
		    updated_at = now()
		WHERE e.workspace_id = $1
		  AND e.channel_id = $2
		  AND e.agent_id = $3
		  AND e.status IN ('pending', 'failed', 'draining')
		  AND e.terminal_outcome IS NULL`, workspaceID, channelID, agentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_event_delivery d
		SET status = 'failed',
		    last_error = 'agent removed from channel',
		    updated_at = now()
		FROM agent_inbox_event e
		WHERE e.id = d.inbox_event_id
		  AND e.workspace_id = $1
		  AND e.channel_id = $2
		  AND e.agent_id = $3
		  AND d.status IN ('leased', 'processing')`, workspaceID, channelID, agentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_execution x
		SET status = 'failed',
		    completed_at = COALESCE(x.completed_at, now())
		FROM agent_inbox_event e
		WHERE x.source_kind = 'inbox'
		  AND x.source_event_id = e.id
		  AND e.workspace_id = $1
		  AND e.channel_id = $2
		  AND e.agent_id = $3
		  AND x.status = 'running'`, workspaceID, channelID, agentID); err != nil {
		return err
	}
	return nil
}

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	issueThreadAssignedEvent      = "issue_assigned"
	issueThreadStatusChangedEvent = "issue_status_changed"
	issueThreadCompletedEvent     = "issue_completed"
)

// issueThreadSystemEventParams is deliberately factual: the event identifies
// the issue and transition, while the optional target is the only agent that
// may be woken. Clients can render it without parsing the fallback content.
type issueThreadSystemEventParams struct {
	IssueID         string `json:"issue_id"`
	IssueIdentifier string `json:"issue_identifier"`
	IssueStatus     string `json:"issue_status"`
	PreviousStatus  string `json:"previous_status,omitempty"`
	ActorID         string `json:"actor_id,omitempty"`
	ActorType       string `json:"actor_type,omitempty"`
	TargetID        string `json:"target_id,omitempty"`
	TargetType      string `json:"target_type,omitempty"`
	TargetHandle    string `json:"target_handle,omitempty"`
	TargetName      string `json:"target_name,omitempty"`
}

type issueThreadBackflowTarget struct {
	ID   pgtype.UUID
	Type string
}

// emitIssueThreadBackflow records the narrow set of issue facts that belong in
// the originating discussion. Source anchors are always thread roots, but the
// query defensively normalizes older data as well. An unanchored issue is a
// normal no-op: it has no conversation to update.
func (h *Handler) emitIssueThreadBackflow(ctx context.Context, issue db.Issue, actorType, actorID string, event string, previousStatus string, target issueThreadBackflowTarget) {
	var channelID, rootID, initiatorUserID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT src.channel_id, COALESCE(message.thread_root_message_id, message.id), channel_row.created_by
		FROM issue_source_message src
		JOIN channel_message message
		  ON message.id = src.message_id
		 AND message.channel_id = src.channel_id
		 AND message.workspace_id = src.workspace_id
		JOIN channel channel_row
		  ON channel_row.id = src.channel_id
		 AND channel_row.workspace_id = src.workspace_id
		WHERE src.issue_id = $1
		  AND src.workspace_id = $2
		  AND message.deleted_at IS NULL
		  AND channel_row.archived_at IS NULL`, issue.ID, issue.WorkspaceID).Scan(&channelID, &rootID, &initiatorUserID)
	if err != nil {
		return
	}
	ch, found := h.getChannel(ctx, uuidToString(issue.WorkspaceID), channelID)
	if !found {
		return
	}

	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	params := issueThreadSystemEventParams{
		IssueID:         uuidToString(issue.ID),
		IssueIdentifier: identifier,
		IssueStatus:     issue.Status,
		PreviousStatus:  previousStatus,
		ActorID:         actorID,
		ActorType:       channelMemberSystemEventPublicType(actorType),
	}

	var targetAgent db.Agent
	if target.Type == "agent" && target.ID.Valid {
		// A target only becomes a directed mention after membership is confirmed.
		// Otherwise this remains a factual source-thread event: persisting an
		// unresolvable reference would falsely advertise a targeted notification.
		targetID := uuidToString(target.ID)
		for _, agent := range h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, "", []protocol.MessagePart{{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "mention",
			RefSubType: "agent",
			RefID:      targetID,
		}}) {
			if agent.ID == target.ID {
				targetAgent = agent
				break
			}
		}
		if targetAgent.ID.Valid {
			ref := h.channelMemberSystemEventActorRef(ctx, uuidToString(issue.WorkspaceID), "agent", target.ID)
			params.TargetID = targetID
			params.TargetType = "agent"
			params.TargetHandle = ref.Handle
			params.TargetName = ref.DisplayName
		}
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		slog.Warn("issue thread backflow: marshal event params", "issue", identifier, "event", event, "error", err)
		return
	}
	content := issueThreadBackflowContent(event, identifier, issue.Status, previousStatus, params.TargetHandle)
	// Every backflow sentence starts with the canonical issue identifier (see
	// issueThreadBackflowContent). Persist the issue entity as an anchored
	// reference alongside the system event so the channel projector can render
	// the same inline issue token/hover contract as an authored group message.
	// The system event remains the factual transition source; the reference only
	// decorates the exact visible identifier span and never changes wake routing.
	issueRefStart, issueRefEnd := contentUTF16Span(content, 0, len(identifier))
	parts := []protocol.MessagePart{{
		Type:        protocol.MessagePartTypeSystemEvent,
		Event:       event,
		EventParams: paramsJSON,
	}, {
		Type:              protocol.MessagePartTypeReference,
		RefType:           "issue-ref",
		RefSubType:        "issue",
		RefID:             uuidToString(issue.ID),
		Label:             identifier,
		ContentStartUTF16: &issueRefStart,
		ContentEndUTF16:   &issueRefEnd,
	}}
	if params.TargetID != "" {
		parts = append(parts, protocol.MessagePart{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "mention",
			RefSubType: "agent",
			RefID:      params.TargetID,
			Label:      "@" + firstNonEmpty(params.TargetHandle, params.TargetName),
		})
	}
	msg, err := h.insertChannelMessageWithParts(ctx, channelID, issue.WorkspaceID, "system", pgtype.UUID{}, "system", content, parts, "multica", nil, pgtype.UUID{}, rootID, nil, 0)
	if err != nil {
		slog.Warn("issue thread backflow: insert system message", "issue", identifier, "event", event, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "system", "", channelID, msg)

	// System rows are observable but never ambiently wake agents. A structured
	// agent reference is the explicit exception: dispatch only that member.
	if targetAgent.ID.Valid && initiatorUserID.Valid {
		// The persisted reference is an explicit personal mention, so retain the
		// established inbox reason instead of adding a parallel event reason.
		if _, err := h.dispatchChannelAgentReplyWithReason(ctx, ch, targetAgent, msg, initiatorUserID, "mention"); err != nil {
			slog.Warn("issue thread backflow: dispatch target agent", "issue", identifier, "event", event, "agent", uuidToString(targetAgent.ID), "error", err)
		}
	}
}

func issueThreadBackflowContent(event, identifier, status, previousStatus, targetHandle string) string {
	target := strings.TrimPrefix(strings.TrimSpace(targetHandle), "@")
	switch event {
	case issueThreadAssignedEvent:
		if target != "" {
			return fmt.Sprintf("%s assigned to @%s", identifier, target)
		}
		return fmt.Sprintf("%s assignment changed", identifier)
	case issueThreadCompletedEvent:
		if target != "" {
			return fmt.Sprintf("%s completed — @%s", identifier, target)
		}
		return fmt.Sprintf("%s completed", identifier)
	default:
		if previousStatus != "" {
			return fmt.Sprintf("%s moved from %s to %s", identifier, previousStatus, status)
		}
		return fmt.Sprintf("%s moved to %s", identifier, status)
	}
}

func issueThreadStatusTarget(issue db.Issue) issueThreadBackflowTarget {
	if issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		return issueThreadBackflowTarget{ID: issue.AssigneeID, Type: "agent"}
	}
	return issueThreadBackflowTarget{}
}

func issueThreadCompletionTarget(issue db.Issue) issueThreadBackflowTarget {
	if issue.CreatorType == "agent" && issue.CreatorID.Valid {
		return issueThreadBackflowTarget{ID: issue.CreatorID, Type: "agent"}
	}
	return issueThreadBackflowTarget{}
}

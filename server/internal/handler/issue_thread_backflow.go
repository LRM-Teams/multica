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
	issueThreadCreatedEvent       = "issue_created"
	issueThreadAssignedEvent      = "issue_assigned"
	issueThreadStatusChangedEvent = "issue_status_changed"
	issueThreadCompletedEvent     = "issue_completed"
)

// issueThreadSystemEventParams is deliberately factual: the event identifies
// the issue and transition, while the optional target is the only agent that
// may be woken. Clients can render it without parsing the fallback content.
// Actor/target names are denormalized so the FE does not depend on the
// workspace agent directory (group managers are hidden from ListAgents — LRM-233).
type issueThreadSystemEventParams struct {
	IssueID         string `json:"issue_id"`
	IssueIdentifier string `json:"issue_identifier"`
	IssueStatus     string `json:"issue_status"`
	PreviousStatus  string `json:"previous_status,omitempty"`
	ActorID         string `json:"actor_id,omitempty"`
	ActorType       string `json:"actor_type,omitempty"`
	ActorHandle     string `json:"actor_handle,omitempty"`
	ActorName       string `json:"actor_name,omitempty"`
	TargetID        string `json:"target_id,omitempty"`
	TargetType      string `json:"target_type,omitempty"`
	TargetHandle    string `json:"target_handle,omitempty"`
	TargetName      string `json:"target_name,omitempty"`
}

type issueThreadBackflowTarget struct {
	ID   pgtype.UUID
	Type string
}

// issueThreadBackflowScope identifies one canonical conversation projection for
// an issue. A direct source anchor wins when it overlaps a project-bound group:
// it keeps the event in the originating thread instead of duplicating it in the
// channel timeline. Project-only scopes are still useful for issues that were
// created outside chat but belong to that project's group.
type issueThreadBackflowScope struct {
	ChannelID       pgtype.UUID
	RootID          pgtype.UUID
	InitiatorUserID pgtype.UUID
	DirectSource    bool
}

// emitIssueThreadBackflow records issue facts in their canonical conversation
// contexts. A message-backed anchor writes into that source thread; a
// channel-only anchor writes one low-noise row in the channel timeline. An
// unanchored issue is a normal no-op: it has no conversation to update.
func (h *Handler) emitIssueThreadBackflow(ctx context.Context, issue db.Issue, actorType, actorID string, event string, previousStatus string, target issueThreadBackflowTarget) {
	for _, scope := range h.issueThreadBackflowScopes(ctx, issue) {
		h.emitIssueThreadBackflowToScope(ctx, issue, actorType, actorID, event, previousStatus, target, scope)
	}
}

func (h *Handler) issueThreadBackflowScopes(ctx context.Context, issue db.Issue) []issueThreadBackflowScope {
	var scopes []issueThreadBackflowScope
	seenChannelIDs := make(map[string]struct{})

	directRows, err := h.DB.Query(ctx, `
		SELECT src.channel_id, COALESCE(message.thread_root_message_id, message.id), channel_row.created_by
		FROM issue_source_message src
		LEFT JOIN channel_message message
		  ON message.id = src.message_id
		 AND message.channel_id = src.channel_id
		 AND message.workspace_id = src.workspace_id
		JOIN channel channel_row
		  ON channel_row.id = src.channel_id
		 AND channel_row.workspace_id = src.workspace_id
		WHERE src.issue_id = $1
		  AND src.workspace_id = $2
		  AND (src.message_id IS NULL OR (message.id IS NOT NULL AND message.deleted_at IS NULL))
		  AND channel_row.archived_at IS NULL`, issue.ID, issue.WorkspaceID)
	if err != nil {
		return scopes
	}
	defer directRows.Close()
	for directRows.Next() {
		var scope issueThreadBackflowScope
		if err := directRows.Scan(&scope.ChannelID, &scope.RootID, &scope.InitiatorUserID); err != nil {
			continue
		}
		scope.DirectSource = true
		scopes = append(scopes, scope)
		seenChannelIDs[uuidToString(scope.ChannelID)] = struct{}{}
	}
	if directRows.Err() != nil {
		return scopes
	}

	if !issue.ProjectID.Valid {
		return scopes
	}
	projectRows, err := h.DB.Query(ctx, `
		SELECT id, created_by
		FROM channel
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND kind = 'group'
		  AND archived_at IS NULL`, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return scopes
	}
	defer projectRows.Close()
	for projectRows.Next() {
		var scope issueThreadBackflowScope
		if err := projectRows.Scan(&scope.ChannelID, &scope.InitiatorUserID); err != nil {
			continue
		}
		if _, alreadyProjected := seenChannelIDs[uuidToString(scope.ChannelID)]; alreadyProjected {
			continue
		}
		scopes = append(scopes, scope)
		seenChannelIDs[uuidToString(scope.ChannelID)] = struct{}{}
	}
	if projectRows.Err() != nil {
		return scopes
	}
	return scopes
}

func (h *Handler) emitIssueThreadBackflowToScope(ctx context.Context, issue db.Issue, actorType, actorID string, event string, previousStatus string, target issueThreadBackflowTarget, scope issueThreadBackflowScope) {
	ch, found := h.getChannel(ctx, uuidToString(issue.WorkspaceID), scope.ChannelID)
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
	if actorID != "" {
		// Resolve display facts at emit time. Group managers (贝克汉姆) are
		// omitted from ListAgents, so the FE cannot look them up live.
		refMemberType := "user"
		if actorType == "agent" {
			refMemberType = "agent"
		}
		ref := h.channelMemberSystemEventActorRef(ctx, uuidToString(issue.WorkspaceID), refMemberType, parseUUID(actorID))
		params.ActorHandle = ref.Handle
		params.ActorName = ref.DisplayName
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
	msg, err := h.insertChannelMessageWithParts(ctx, scope.ChannelID, issue.WorkspaceID, "system", pgtype.UUID{}, "system", content, parts, "multica", nil, pgtype.UUID{}, scope.RootID, nil, 0)
	if err != nil {
		slog.Warn("issue thread backflow: insert system message", "issue", identifier, "event", event, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "system", "", scope.ChannelID, msg)

	// System rows are observable but never ambiently wake agents. A structured
	// agent reference is the explicit exception: dispatch only that member.
	if scope.DirectSource && targetAgent.ID.Valid && scope.InitiatorUserID.Valid {
		// The persisted reference is an explicit personal mention, so retain the
		// established inbox reason instead of adding a parallel event reason.
		if _, err := h.dispatchChannelAgentReplyWithReason(ctx, ch, targetAgent, msg, scope.InitiatorUserID, "mention"); err != nil {
			slog.Warn("issue thread backflow: dispatch target agent", "issue", identifier, "event", event, "agent", uuidToString(targetAgent.ID), "error", err)
		}
	}
}

func issueThreadBackflowContent(event, identifier, status, previousStatus, targetHandle string) string {
	target := strings.TrimPrefix(strings.TrimSpace(targetHandle), "@")
	switch event {
	case issueThreadCreatedEvent:
		return fmt.Sprintf("%s created", identifier)
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

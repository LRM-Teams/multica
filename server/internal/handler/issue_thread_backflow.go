package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	issueThreadCreatedEvent       = "issue_created"
	issueThreadAssignedEvent      = "issue_assigned"
	issueThreadStatusChangedEvent = "issue_status_changed"
	issueThreadCompletedEvent     = "issue_completed"

	// Same actor + same aggregatable event type merge into one system row for
	// this window (LRM-418 / LRM-422). Window is anchored on the open bubble's
	// created_at — later same-key events update that row instead of inserting.
	issueThreadAggregationWindow = 5 * time.Minute
)

// issueThreadSystemEventItem is one retained transition inside an aggregated
// system row. FE expand (LRM-423) renders this list; never drop an item to
// shrink the bubble (LRM-238).
type issueThreadSystemEventItem struct {
	IssueID         string `json:"issue_id"`
	IssueIdentifier string `json:"issue_identifier"`
	IssueTitle      string `json:"issue_title"`
	IssueStatus     string `json:"issue_status,omitempty"`
	PreviousStatus  string `json:"previous_status,omitempty"`
	OccurredAt      string `json:"occurred_at,omitempty"`
	TargetID        string `json:"target_id,omitempty"`
	TargetType      string `json:"target_type,omitempty"`
	TargetHandle    string `json:"target_handle,omitempty"`
	TargetName      string `json:"target_name,omitempty"`
}

// issueThreadSystemEventParams is deliberately factual: the event identifies
// the issue and transition, while the optional target is the only agent that
// may be woken. Clients can render it without parsing the fallback content.
// Actor/target ids are the resolvable identity (GET /api/member-profiles/…).
// Optional handle/name fields may still be emitted for diagnostics, but the FE
// must not use them as a silent display fallback (LRM-281 / LRM-238) — group
// managers are hidden from ListAgents (LRM-233) and must be looked up via the
// profile API instead.
//
// For aggregatable issue events, `items` always carries every merged transition
// (N>=1). Top-level issue_* / target_* mirror the latest item so older
// single-issue readers keep working.
type issueThreadSystemEventParams struct {
	IssueID         string                       `json:"issue_id"`
	IssueIdentifier string                       `json:"issue_identifier"`
	IssueTitle      string                       `json:"issue_title,omitempty"`
	IssueStatus     string                       `json:"issue_status"`
	PreviousStatus  string                       `json:"previous_status,omitempty"`
	ActorID         string                       `json:"actor_id,omitempty"`
	ActorType       string                       `json:"actor_type,omitempty"`
	ActorHandle     string                       `json:"actor_handle,omitempty"`
	ActorName       string                       `json:"actor_name,omitempty"`
	TargetID        string                       `json:"target_id,omitempty"`
	TargetType      string                       `json:"target_type,omitempty"`
	TargetHandle    string                       `json:"target_handle,omitempty"`
	TargetName      string                       `json:"target_name,omitempty"`
	Items           []issueThreadSystemEventItem `json:"items,omitempty"`
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
	}
	if directRows.Err() != nil {
		return scopes
	}
	// LRM-638: issue system-event backflow must be channel-scoped. We no longer
	// fan out to every group channel bound to the issue's project — that leaked
	// created_at / issue metadata into channels that never anchored the issue.
	// Events remain queryable via Activity / issue detail / search; only the
	// in-feed echo is restricted to the direct-source channels above.
	return scopes
}

func (h *Handler) emitIssueThreadBackflowToScope(ctx context.Context, issue db.Issue, actorType, actorID string, event string, previousStatus string, target issueThreadBackflowTarget, scope issueThreadBackflowScope) {
	ch, found := h.getChannel(ctx, uuidToString(issue.WorkspaceID), scope.ChannelID)
	if !found {
		return
	}

	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	issueTitle := strings.TrimSpace(issue.Title)
	params := issueThreadSystemEventParams{
		IssueID:         uuidToString(issue.ID),
		IssueIdentifier: identifier,
		IssueTitle:      issueTitle,
		IssueStatus:     issue.Status,
		PreviousStatus:  previousStatus,
		ActorID:         actorID,
		ActorType:       channelMemberSystemEventPublicType(actorType),
	}
	if actorID != "" {
		// Still stamp handle/name for diagnostics / older clients, but display
		// must resolve via member-profile (LRM-281) because group managers are
		// omitted from ListAgents (LRM-233).
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

	if issueThreadEventAggregatable(event) {
		if params.IssueTitle == "" {
			slog.Warn("issue thread backflow: skip aggregation without issue_title", "issue", identifier, "event", event)
		} else {
			item := issueThreadSystemEventItem{
				IssueID:         params.IssueID,
				IssueIdentifier: params.IssueIdentifier,
				IssueTitle:      params.IssueTitle,
				IssueStatus:     params.IssueStatus,
				PreviousStatus:  params.PreviousStatus,
				OccurredAt:      time.Now().UTC().Format(time.RFC3339),
				TargetID:        params.TargetID,
				TargetType:      params.TargetType,
				TargetHandle:    params.TargetHandle,
				TargetName:      params.TargetName,
			}
			params.Items = []issueThreadSystemEventItem{item}
			if h.tryMergeIssueThreadBackflow(ctx, ch, issue, event, params, targetAgent, scope) {
				return
			}
		}
	}

	content := issueThreadBackflowContentFromParams(event, params)
	parts, err := issueThreadBackflowParts(event, params, content)
	if err != nil {
		slog.Warn("issue thread backflow: marshal event params", "issue", identifier, "event", event, "error", err)
		return
	}
	msg, err := h.insertChannelMessageWithParts(ctx, scope.ChannelID, issue.WorkspaceID, "system", pgtype.UUID{}, "system", content, parts, "multica", nil, pgtype.UUID{}, scope.RootID, nil, 0)
	if err != nil {
		slog.Warn("issue thread backflow: insert system message", "issue", identifier, "event", event, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "system", "", scope.ChannelID, msg)

	// A structured Agent reference is resolved by the committed channel:message
	// Delivery boundary above. System rows otherwise remain observable only.
}

// tryMergeIssueThreadBackflow folds a new aggregatable transition into an open
// same-key bubble in this scope (channel + thread root + actor + event) when
// that bubble's created_at is still inside the 5-minute window. Returns true
// when the event was recorded on an existing row (insert skipped). On any
// failure it returns false so the caller can insert — never silently drop
// (LRM-238).
func (h *Handler) tryMergeIssueThreadBackflow(ctx context.Context, ch ChannelResponse, issue db.Issue, event string, incoming issueThreadSystemEventParams, targetAgent db.Agent, scope issueThreadBackflowScope) bool {
	if h.TxStarter == nil || len(incoming.Items) == 0 {
		return false
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("issue thread backflow: begin aggregation tx", "issue", incoming.IssueIdentifier, "event", event, "error", err)
		return false
	}
	defer tx.Rollback(ctx)

	lockKey := issueThreadAggregationLockKey(scope, incoming.ActorType, incoming.ActorID, event)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		slog.Warn("issue thread backflow: aggregation lock", "issue", incoming.IssueIdentifier, "event", event, "error", err)
		return false
	}

	var (
		msgID    pgtype.UUID
		rawParts []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT id, parts
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND author_type = 'system'
		  AND deleted_at IS NULL
		  AND created_at > now() - ($3::double precision * interval '1 second')
		  AND (
		        ($4::uuid IS NULL AND thread_root_message_id IS NULL)
		     OR thread_root_message_id = $4
		  )
		  AND EXISTS (
		        SELECT 1
		        FROM jsonb_array_elements(parts) AS part
		        WHERE part->>'type' = 'system_event'
		          AND part->>'event' = $5
		          AND COALESCE(part->'event_params'->>'actor_id', '') = $6
		          AND COALESCE(part->'event_params'->>'actor_type', '') = $7
		  )
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE`,
		scope.ChannelID,
		issue.WorkspaceID,
		issueThreadAggregationWindow.Seconds(),
		nullableUUID(scope.RootID),
		event,
		incoming.ActorID,
		incoming.ActorType,
	).Scan(&msgID, &rawParts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false
		}
		slog.Warn("issue thread backflow: lookup open aggregation", "issue", incoming.IssueIdentifier, "event", event, "error", err)
		return false
	}

	existingParts := messageparts.Decode(rawParts)
	existingParams, ok := issueThreadParamsFromParts(existingParts, event)
	if !ok {
		return false
	}
	merged := mergeIssueThreadAggregationParams(existingParams, incoming)
	content := issueThreadBackflowContentFromParams(event, merged)
	parts, err := issueThreadBackflowParts(event, merged, content)
	if err != nil {
		slog.Warn("issue thread backflow: marshal merged params", "issue", incoming.IssueIdentifier, "event", event, "error", err)
		return false
	}

	msg, err := scanChannelMessage(tx.QueryRow(ctx, `
		UPDATE channel_message
		SET content = $2, parts = $3::jsonb, edited_at = now()
		WHERE id = $1
		RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`,
		msgID, content, messageparts.MustJSON(parts)))
	if err != nil {
		slog.Warn("issue thread backflow: update aggregated message", "issue", incoming.IssueIdentifier, "event", event, "error", err)
		return false
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("issue thread backflow: commit aggregation", "issue", incoming.IssueIdentifier, "event", event, "error", err)
		return false
	}

	h.publishChannelToMembers(ctx, protocol.EventChannelMessageUpdated, ch.WorkspaceID, "system", "", scope.ChannelID, msg)

	return true
}

func issueThreadEventAggregatable(event string) bool {
	switch event {
	case issueThreadCreatedEvent, issueThreadStatusChangedEvent, issueThreadCompletedEvent, issueThreadAssignedEvent:
		return true
	default:
		return false
	}
}

func issueThreadAggregationLockKey(scope issueThreadBackflowScope, actorType, actorID, event string) string {
	root := "timeline"
	if scope.RootID.Valid {
		root = uuidToString(scope.RootID)
	}
	return fmt.Sprintf("issue-thread-backflow:%s:%s:%s:%s:%s", uuidToString(scope.ChannelID), root, actorType, actorID, event)
}

func issueThreadParamsFromParts(parts []protocol.MessagePart, event string) (issueThreadSystemEventParams, bool) {
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeSystemEvent || part.Event != event {
			continue
		}
		var params issueThreadSystemEventParams
		if err := json.Unmarshal(part.EventParams, &params); err != nil {
			return issueThreadSystemEventParams{}, false
		}
		if len(params.Items) == 0 && params.IssueID != "" {
			params.Items = []issueThreadSystemEventItem{{
				IssueID:         params.IssueID,
				IssueIdentifier: params.IssueIdentifier,
				IssueTitle:      params.IssueTitle,
				IssueStatus:     params.IssueStatus,
				PreviousStatus:  params.PreviousStatus,
				TargetID:        params.TargetID,
				TargetType:      params.TargetType,
				TargetHandle:    params.TargetHandle,
				TargetName:      params.TargetName,
			}}
		}
		if issueThreadEventAggregatable(event) && !issueThreadItemsHaveTitles(params.Items) {
			return issueThreadSystemEventParams{}, false
		}
		return params, params.IssueID != "" || len(params.Items) > 0
	}
	return issueThreadSystemEventParams{}, false
}

// mergeIssueThreadAggregationParams appends (or replaces same issue_id) the
// incoming item, keeps actor fields from the open bubble, and mirrors the
// latest item onto top-level issue/target fields.
func mergeIssueThreadAggregationParams(existing, incoming issueThreadSystemEventParams) issueThreadSystemEventParams {
	out := existing
	if out.ActorID == "" {
		out.ActorID = incoming.ActorID
	}
	if out.ActorType == "" {
		out.ActorType = incoming.ActorType
	}
	if out.ActorHandle == "" {
		out.ActorHandle = incoming.ActorHandle
	}
	if out.ActorName == "" {
		out.ActorName = incoming.ActorName
	}
	if len(out.Items) == 0 && out.IssueID != "" {
		out.Items = []issueThreadSystemEventItem{{
			IssueID:         out.IssueID,
			IssueIdentifier: out.IssueIdentifier,
			IssueTitle:      out.IssueTitle,
			IssueStatus:     out.IssueStatus,
			PreviousStatus:  out.PreviousStatus,
			TargetID:        out.TargetID,
			TargetType:      out.TargetType,
			TargetHandle:    out.TargetHandle,
			TargetName:      out.TargetName,
		}}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range out.Items {
		if out.Items[i].OccurredAt == "" {
			out.Items[i].OccurredAt = now
		}
	}
	for _, item := range incoming.Items {
		if item.OccurredAt == "" {
			item.OccurredAt = now
		}
		replaced := false
		for i := range out.Items {
			if out.Items[i].IssueID == item.IssueID {
				out.Items[i] = item
				replaced = true
				break
			}
		}
		if !replaced {
			out.Items = append(out.Items, item)
		}
	}
	if len(out.Items) > 0 {
		latest := out.Items[len(out.Items)-1]
		out.IssueID = latest.IssueID
		out.IssueIdentifier = latest.IssueIdentifier
		out.IssueTitle = latest.IssueTitle
		out.IssueStatus = latest.IssueStatus
		out.PreviousStatus = latest.PreviousStatus
		out.TargetID = latest.TargetID
		out.TargetType = latest.TargetType
		out.TargetHandle = latest.TargetHandle
		out.TargetName = latest.TargetName
	}
	return out
}

func issueThreadBackflowContentFromParams(event string, params issueThreadSystemEventParams) string {
	items := params.Items
	if len(items) == 0 {
		return issueThreadBackflowContent(event, params.IssueIdentifier, params.IssueStatus, params.PreviousStatus, params.TargetHandle)
	}
	if len(items) == 1 {
		return issueThreadBackflowContent(event, issueThreadItemContentLabel(items[0]), items[0].IssueStatus, items[0].PreviousStatus, items[0].TargetHandle)
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		if label := issueThreadItemContentLabel(item); label != "" {
			labels = append(labels, label)
		}
	}
	list := strings.Join(labels, ", ")
	switch event {
	case issueThreadAssignedEvent:
		return fmt.Sprintf("%s assignment changed", list)
	case issueThreadCompletedEvent:
		return fmt.Sprintf("%s completed", list)
	default:
		if params.PreviousStatus != "" {
			return fmt.Sprintf("%s moved from %s to %s", list, params.PreviousStatus, params.IssueStatus)
		}
		return fmt.Sprintf("%s moved to %s", list, params.IssueStatus)
	}
}

func issueThreadItemsHaveTitles(items []issueThreadSystemEventItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item.IssueTitle) == "" {
			return false
		}
	}
	return true
}

func issueThreadItemContentLabel(item issueThreadSystemEventItem) string {
	title := strings.TrimSpace(item.IssueTitle)
	identifier := strings.TrimSpace(item.IssueIdentifier)
	if title == "" {
		return ""
	}
	if identifier == "" {
		return title
	}
	return fmt.Sprintf("%s · %s", title, identifier)
}

func issueThreadBackflowParts(event string, params issueThreadSystemEventParams, content string) ([]protocol.MessagePart, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	parts := []protocol.MessagePart{{
		Type:        protocol.MessagePartTypeSystemEvent,
		Event:       event,
		EventParams: paramsJSON,
	}}

	items := params.Items
	if len(items) == 0 && params.IssueID != "" {
		items = []issueThreadSystemEventItem{{
			IssueID:         params.IssueID,
			IssueIdentifier: params.IssueIdentifier,
			IssueTitle:      params.IssueTitle,
		}}
	}
	searchFrom := 0
	for _, item := range items {
		if item.IssueID == "" || item.IssueIdentifier == "" {
			continue
		}
		rel := strings.Index(content[searchFrom:], item.IssueIdentifier)
		if rel < 0 {
			continue
		}
		byteStart := searchFrom + rel
		start, end := contentUTF16Span(content, byteStart, byteStart+len(item.IssueIdentifier))
		startCopy, endCopy := start, end
		parts = append(parts, protocol.MessagePart{
			Type:              protocol.MessagePartTypeReference,
			RefType:           "issue-ref",
			RefSubType:        "issue",
			RefID:             item.IssueID,
			Label:             item.IssueIdentifier,
			ContentStartUTF16: &startCopy,
			ContentEndUTF16:   &endCopy,
		})
		searchFrom = byteStart + len(item.IssueIdentifier)
	}

	seenTargets := make(map[string]struct{})
	appendMention := func(targetID, targetHandle, targetName string) {
		if targetID == "" {
			return
		}
		if _, ok := seenTargets[targetID]; ok {
			return
		}
		seenTargets[targetID] = struct{}{}
		parts = append(parts, protocol.MessagePart{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "mention",
			RefSubType: "agent",
			RefID:      targetID,
			Label:      "@" + firstNonEmpty(targetHandle, targetName),
		})
	}
	for _, item := range items {
		appendMention(item.TargetID, item.TargetHandle, item.TargetName)
	}
	appendMention(params.TargetID, params.TargetHandle, params.TargetName)
	return parts, nil
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

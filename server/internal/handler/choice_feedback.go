package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReplyFeedbackResponse is the actor's preference on one agent message.
// Distinct from social emoji reactions.
type ReplyFeedbackResponse struct {
	ID          string  `json:"id"`
	MessageKind string  `json:"message_kind"`
	MessageID   string  `json:"message_id"`
	Value       int     `json:"value"` // +1 or -1
	AgentID     *string `json:"agent_id,omitempty"`
	TaskID      *string `json:"task_id,omitempty"`
	UpdatedAt   string  `json:"updated_at"`
}

// ChannelMessageResponse gains optional my_reply_feedback (current member only).
// Field lives on the message struct in channel.go.

type chooseChannelOptionRequest struct {
	ChoiceID string `json:"choice_id"`
	OptionID string `json:"option_id"`
}

type upsertReplyFeedbackRequest struct {
	Value int `json:"value"`
}

// ChooseChannelMessageOption records a choice pick (first select or one reselect)
// and posts a user-visible reply that wakes the agent (DM auto-wake; group via
// @mention of the choice author).
func (h *Handler) ChooseChannelMessageOption(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireChannelWritable(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req chooseChannelOptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	choiceID := strings.TrimSpace(req.ChoiceID)
	optionID := strings.TrimSpace(req.OptionID)
	if choiceID == "" || optionID == "" {
		writeError(w, http.StatusBadRequest, "choice_id and option_id are required")
		return
	}

	ch, found := h.getChannel(r.Context(), workspaceID, channelID)
	if !found {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if !h.requireDMChannelAgentAccess(w, r, workspaceID, userID, ch) {
		return
	}

	result, err := h.applyChannelChoiceSelection(r.Context(), ch, workspaceID, userID, channelID, messageID, choiceID, optionID)
	if err != nil {
		switch {
		case errors.Is(err, errChoiceNotFound):
			writeError(w, http.StatusNotFound, "choice not found")
		case errors.Is(err, errChoiceAlreadySelected):
			writeError(w, http.StatusConflict, "choice already selected")
		case errors.Is(err, errChoiceOptionInvalid):
			writeError(w, http.StatusBadRequest, "invalid option_id")
		case errors.Is(err, errChoiceNotAuthorized):
			writeError(w, http.StatusForbidden, "not allowed to choose")
		default:
			slog.Warn("choose channel option failed", "channel_id", uuidToString(channelID), "message_id", uuidToString(messageID), "error", err)
			writeError(w, http.StatusInternalServerError, "failed to choose option")
		}
		return
	}

	updated := h.attachSingleChannelMessageDetails(r.Context(), workspaceID, parseUUID(userID), result.UpdatedMessage)
	h.publishChannelToMembers(r.Context(), protocol.EventChannelMessageUpdated, workspaceID, "member", userID, channelID, updated)
	out := map[string]any{"message": updated}
	if result.ReplyMessage.ID != "" {
		reply := h.attachSingleChannelMessageDetails(r.Context(), workspaceID, parseUUID(userID), result.ReplyMessage)
		h.publishChannelToMembers(r.Context(), protocol.EventChannelMessage, workspaceID, "member", userID, channelID, reply)
		out["reply"] = reply
		h.runAfterChannelMessageAck(r.Context(), func(ctx context.Context) {
			h.dispatchHumanChannelMessageSideEffects(ctx, ch, reply, parseUUID(userID))
		})
	}
	writeJSON(w, http.StatusCreated, out)
}

var (
	errChoiceNotFound        = errors.New("choice not found")
	errChoiceAlreadySelected = errors.New("choice already selected")
	errChoiceOptionInvalid   = errors.New("invalid option")
	errChoiceNotAuthorized   = errors.New("not authorized")
)

// maxChoiceSelectCount: first pick + one reselect, then locked.
const maxChoiceSelectCount = 2

type channelChoiceApplyResult struct {
	UpdatedMessage ChannelMessageResponse
	ReplyMessage   ChannelMessageResponse
}

func (h *Handler) applyChannelChoiceSelection(
	ctx context.Context,
	ch ChannelResponse,
	workspaceID, userID string,
	channelID, messageID pgtype.UUID,
	choiceID, optionID string,
) (channelChoiceApplyResult, error) {
	if h.TxStarter == nil {
		return channelChoiceApplyResult{}, errors.New("transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return channelChoiceApplyResult{}, err
	}
	defer tx.Rollback(ctx)

	var authorType string
	var authorID pgtype.UUID
	var content string
	var partsRaw []byte
	var deletedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT author_type, author_id, content, parts, deleted_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3
		FOR UPDATE`, messageID, channelID, parseUUID(workspaceID)).Scan(
		&authorType, &authorID, &content, &partsRaw, &deletedAt,
	)
	if err != nil {
		if errorsIsNoRows(err) {
			return channelChoiceApplyResult{}, errChoiceNotFound
		}
		return channelChoiceApplyResult{}, err
	}
	if deletedAt.Valid {
		return channelChoiceApplyResult{}, errChoiceNotFound
	}
	if authorType != "agent" {
		return channelChoiceApplyResult{}, errChoiceNotFound
	}

	parts := messageparts.Decode(partsRaw)
	choiceIdx := -1
	var selectedLabel string
	isReselect := false
	for i := range parts {
		if parts[i].Type != protocol.MessagePartTypeChoice {
			continue
		}
		if parts[i].ChoiceID != choiceID {
			continue
		}
		choiceIdx = i
		prevSelected := strings.TrimSpace(parts[i].SelectedOptionID)
		prevCount := parts[i].SelectCount
		if prevSelected == "" {
			prevCount = 0
		} else if prevCount == 0 {
			prevCount = 1
		}
		if prevSelected == optionID {
			// Idempotent re-tap of the same option: no new reply.
			if err := tx.Commit(ctx); err != nil {
				return channelChoiceApplyResult{}, err
			}
			updatedMsg, found := h.getChannelMessage(ctx, workspaceID, channelID, messageID)
			if !found {
				updatedMsg = ChannelMessageResponse{
					ID:          uuidToString(messageID),
					ChannelID:   uuidToString(channelID),
					WorkspaceID: workspaceID,
					Content:     content,
					Parts:       parts,
					Type:        "agent",
				}
			}
			return channelChoiceApplyResult{UpdatedMessage: updatedMsg}, nil
		}
		if prevCount >= maxChoiceSelectCount {
			return channelChoiceApplyResult{}, errChoiceAlreadySelected
		}
		foundOpt := false
		for _, opt := range parts[i].Options {
			if opt.ID == optionID {
				foundOpt = true
				selectedLabel = opt.Label
				break
			}
		}
		if !foundOpt {
			return channelChoiceApplyResult{}, errChoiceOptionInvalid
		}
		isReselect = prevSelected != ""
		break
	}
	if choiceIdx < 0 {
		return channelChoiceApplyResult{}, errChoiceNotFound
	}

	// Group: only humans may choose; DM same. (Agents cannot choose.)
	// Spec: group — only the triggering human; v1 allows any channel member
	// who can write (same as reacting). Tightened later if needed.
	_ = ch

	nextCount := 1
	if isReselect {
		nextCount = maxChoiceSelectCount
	}
	parts[choiceIdx].SelectedOptionID = optionID
	parts[choiceIdx].SelectCount = nextCount
	normalizedContent, normalizedParts, err := messageparts.Normalize(content, parts)
	if err != nil {
		return channelChoiceApplyResult{}, err
	}
	partsJSON := messageparts.MustJSON(normalizedParts)
	if _, err := tx.Exec(ctx, `
		UPDATE channel_message
		SET parts = $1, content = $2
		WHERE id = $3`, partsJSON, normalizedContent, messageID); err != nil {
		return channelChoiceApplyResult{}, err
	}

	replyPrefix := "选择："
	if isReselect {
		replyPrefix = "改选："
	}
	replyContent := replyPrefix + selectedLabel
	if ch.Kind != "dm" && authorID.Valid {
		agentName := h.channelAgentAuthorName(ctx, authorID)
		if agentName == "" {
			agentName = "agent"
		}
		replyContent = "@" + agentName + " " + replyContent
	}
	replyParts := []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: replyContent},
		{
			Type:     protocol.MessagePartTypeChoiceReply,
			ChoiceID: choiceID,
			OptionID: optionID,
			Label:    selectedLabel,
		},
	}
	replyContent, replyParts, err = messageparts.Normalize(replyContent, replyParts)
	if err != nil {
		return channelChoiceApplyResult{}, err
	}
	if ch.Kind != "dm" && authorID.Valid {
		replyContent, replyParts, err = h.enrichChannelMessageMentions(ctx, ch, replyContent, replyParts)
		if err != nil {
			return channelChoiceApplyResult{}, err
		}
	}

	authorName := h.channelAuthorName(ctx, userID)
	threadID := uuid.NewString()
	clientMessageID := "choice-" + choiceID + "-" + uuid.NewString()
	inserted, err := insertChannelMessageWithPartsExec(
		ctx, tx, channelID, parseUUID(workspaceID), "user", parseUUID(userID),
		authorName, replyContent, replyParts, "multica", nil, &clientMessageID,
		messageID, pgtype.UUID{}, nil, pgtype.UUID{}, &threadID, 0,
	)
	if err != nil {
		return channelChoiceApplyResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return channelChoiceApplyResult{}, err
	}

	updatedMsg, found := h.getChannelMessage(ctx, workspaceID, channelID, messageID)
	if !found {
		updatedMsg = ChannelMessageResponse{
			ID:          uuidToString(messageID),
			ChannelID:   uuidToString(channelID),
			WorkspaceID: workspaceID,
			Content:     normalizedContent,
			Parts:       normalizedParts,
			Type:        "agent",
		}
		if authorID.Valid {
			aid := uuidToString(authorID)
			updatedMsg.AuthorID = &aid
		}
	}
	return channelChoiceApplyResult{
		UpdatedMessage: updatedMsg,
		ReplyMessage:   inserted.Message,
	}, nil
}

func (h *Handler) channelAgentAuthorName(ctx context.Context, agentID pgtype.UUID) string {
	var name string
	_ = h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(display_name, ''), NULLIF(name, ''), '') FROM agent WHERE id = $1`, agentID).Scan(&name)
	return strings.TrimSpace(name)
}

func (h *Handler) getChannelMessage(ctx context.Context, workspaceID string, channelID, messageID pgtype.UUID) (ChannelMessageResponse, bool) {
	var id, chID, wsID, authorID pgtype.UUID
	var seq int64
	var authorType, authorName, content string
	var partsRaw []byte
	var createdAt pgtype.Timestamptz
	err := h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, seq, author_type, author_id, author_name, content, parts, created_at
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND deleted_at IS NULL`,
		messageID, channelID, parseUUID(workspaceID),
	).Scan(&id, &chID, &wsID, &seq, &authorType, &authorID, &authorName, &content, &partsRaw, &createdAt)
	if err != nil {
		return ChannelMessageResponse{}, false
	}
	msg := ChannelMessageResponse{
		ID:          uuidToString(id),
		ChannelID:   uuidToString(chID),
		WorkspaceID: uuidToString(wsID),
		Seq:         seq,
		Type:        authorType,
		AuthorName:  authorName,
		Content:     content,
		Parts:       messageparts.Decode(partsRaw),
		Source:      "multica",
		CreatedAt:   timestampToString(createdAt),
	}
	if authorID.Valid {
		aid := uuidToString(authorID)
		msg.AuthorID = &aid
	}
	return msg, true
}

// UpsertChannelReplyFeedback sets or replaces 👍/👎 on an agent channel message.
func (h *Handler) UpsertChannelReplyFeedback(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	var req upsertReplyFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Value != 1 && req.Value != -1 {
		writeError(w, http.StatusBadRequest, "value must be 1 or -1")
		return
	}

	var authorType string
	var authorID pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT author_type, author_id
		FROM channel_message
		WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND deleted_at IS NULL`,
		messageID, channelID, parseUUID(workspaceID),
	).Scan(&authorType, &authorID)
	if err != nil {
		if errorsIsNoRows(err) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load message")
		return
	}
	if authorType != "agent" || !authorID.Valid {
		writeError(w, http.StatusBadRequest, "reply feedback is only allowed on agent messages")
		return
	}

	var id pgtype.UUID
	var updatedAt pgtype.Timestamptz
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO reply_feedback (workspace_id, message_kind, message_id, agent_id, actor_user_id, value)
		VALUES ($1, 'channel', $2, $3, $4, $5)
		ON CONFLICT (message_kind, message_id, actor_user_id)
		DO UPDATE SET value = EXCLUDED.value, agent_id = EXCLUDED.agent_id, updated_at = now()
		RETURNING id, updated_at`,
		parseUUID(workspaceID), messageID, authorID, parseUUID(userID), req.Value,
	).Scan(&id, &updatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save reply feedback")
		return
	}
	agentID := uuidToString(authorID)
	fb := ReplyFeedbackResponse{
		ID:          uuidToString(id),
		MessageKind: "channel",
		MessageID:   uuidToString(messageID),
		Value:       req.Value,
		AgentID:     &agentID,
		UpdatedAt:   timestampToString(updatedAt),
	}
	h.publishChannelToMembers(r.Context(), protocol.EventChannelReplyFeedback, workspaceID, "member", userID, channelID, map[string]any{
		"channel_id":     uuidToString(channelID),
		"message_id":     uuidToString(messageID),
		"reply_feedback": fb,
		"actor_user_id":  userID,
	})
	writeJSON(w, http.StatusOK, fb)
}

// DeleteChannelReplyFeedback clears the current user's 👍/👎 on a message.
func (h *Handler) DeleteChannelReplyFeedback(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		DELETE FROM reply_feedback
		WHERE workspace_id = $1 AND message_kind = 'channel' AND message_id = $2 AND actor_user_id = $3`,
		parseUUID(workspaceID), messageID, parseUUID(userID),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete reply feedback")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "reply feedback not found")
		return
	}
	h.publishChannelToMembers(r.Context(), protocol.EventChannelReplyFeedback, workspaceID, "member", userID, channelID, map[string]any{
		"channel_id":    uuidToString(channelID),
		"message_id":    uuidToString(messageID),
		"actor_user_id": userID,
		"cleared":       true,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ListChannelReplyFeedback returns feedback rows for a message (queryable by message).
func (h *Handler) ListChannelReplyFeedback(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	messageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "messageId"), "message id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	// Ensure message belongs to channel.
	var exists bool
	if err := h.DB.QueryRow(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM channel_message
			WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND deleted_at IS NULL
		)`, messageID, channelID, parseUUID(workspaceID)).Scan(&exists); err != nil || !exists {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, message_kind, message_id, value, agent_id, task_id, updated_at
		FROM reply_feedback
		WHERE workspace_id = $1 AND message_kind = 'channel' AND message_id = $2
		ORDER BY updated_at DESC`, parseUUID(workspaceID), messageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list reply feedback")
		return
	}
	defer rows.Close()
	out := []ReplyFeedbackResponse{}
	for rows.Next() {
		var id, mid pgtype.UUID
		var agentID, taskID pgtype.UUID
		var kind string
		var value int16
		var updatedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &kind, &mid, &value, &agentID, &taskID, &updatedAt); err != nil {
			continue
		}
		item := ReplyFeedbackResponse{
			ID:          uuidToString(id),
			MessageKind: kind,
			MessageID:   uuidToString(mid),
			Value:       int(value),
			UpdatedAt:   timestampToString(updatedAt),
		}
		if agentID.Valid {
			s := uuidToString(agentID)
			item.AgentID = &s
		}
		if taskID.Valid {
			s := uuidToString(taskID)
			item.TaskID = &s
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": out})
}

func (h *Handler) attachChannelMessageReplyFeedback(ctx context.Context, workspaceID string, userID pgtype.UUID, messages []ChannelMessageResponse) {
	if !userID.Valid || len(messages) == 0 {
		return
	}
	messageIDs := make([]pgtype.UUID, 0, len(messages))
	for _, msg := range messages {
		if msg.DeletedAt != nil || msg.Type != "agent" {
			continue
		}
		messageIDs = append(messageIDs, parseUUID(msg.ID))
	}
	if len(messageIDs) == 0 {
		return
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, message_id, value, agent_id, task_id, updated_at
		FROM reply_feedback
		WHERE workspace_id = $1 AND message_kind = 'channel'
		  AND actor_user_id = $2 AND message_id = ANY($3::uuid[])`,
		parseUUID(workspaceID), userID, messageIDs)
	if err != nil {
		slog.Warn("reply feedback: load failed", "workspace", workspaceID, "error", err)
		return
	}
	defer rows.Close()
	byMessage := map[string]ReplyFeedbackResponse{}
	for rows.Next() {
		var id, mid, agentID, taskID pgtype.UUID
		var value int16
		var updatedAt pgtype.Timestamptz
		if err := rows.Scan(&id, &mid, &value, &agentID, &taskID, &updatedAt); err != nil {
			continue
		}
		key := uuidToString(mid)
		item := ReplyFeedbackResponse{
			ID:          uuidToString(id),
			MessageKind: "channel",
			MessageID:   key,
			Value:       int(value),
			UpdatedAt:   timestampToString(updatedAt),
		}
		if agentID.Valid {
			s := uuidToString(agentID)
			item.AgentID = &s
		}
		if taskID.Valid {
			s := uuidToString(taskID)
			item.TaskID = &s
		}
		byMessage[key] = item
	}
	for i := range messages {
		if fb, ok := byMessage[messages[i].ID]; ok {
			cp := fb
			messages[i].MyReplyFeedback = &cp
		}
	}
}

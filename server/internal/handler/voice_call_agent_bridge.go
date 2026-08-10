package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	voiceCallAgentProvider = "volcengine"
	voiceCallAgentPrompt   = "" +
		"\n\nLive voice call delivery:\n" +
		"- The current message is a speech transcript from an active call with this member.\n" +
		"- Use your normal Multica memory, tools, permissions, and project context before answering.\n" +
		"- Produce a concise natural spoken answer through the normal visible reply path.\n" +
		"- Do not use Markdown, tables, code fences, stickers, reactions, or the voice-message command; RTC synthesizes your reply as speech.\n" +
		"- Do not claim an action succeeded unless you actually performed or verified it."
)

var (
	errVoiceCallAgentTurnUnavailable = errors.New("voice call agent turn is unavailable")
	errVoiceCallAgentTurnConflict    = errors.New("voice call agent turn conflicts with persisted state")
)

type VoiceCallAgentBridge struct {
	handler      *Handler
	waitTimeout  time.Duration
	pollInterval time.Duration
}

type voiceCallAgentDispatchResult struct {
	Scope        voicecall.Scope
	Channel      ChannelResponse
	Agent        db.Agent
	Message      ChannelMessageResponse
	Event        db.AgentInboxEvent
	AgentSession db.AgentSession
	Created      bool
}

func NewVoiceCallAgentBridge(
	handler *Handler,
	waitTimeout time.Duration,
) (*VoiceCallAgentBridge, error) {
	if handler == nil ||
		handler.DB == nil ||
		handler.TxStarter == nil ||
		handler.Queries == nil {
		return nil, errors.New("voice call agent bridge requires a configured handler")
	}
	if waitTimeout <= 0 {
		return nil, errors.New("voice call agent bridge wait timeout must be positive")
	}
	return &VoiceCallAgentBridge{
		handler:      handler,
		waitTimeout:  waitTimeout,
		pollInterval: 200 * time.Millisecond,
	}, nil
}

func (bridge *VoiceCallAgentBridge) dispatch(
	ctx context.Context,
	input VoiceCallLLMInput,
) (voiceCallAgentDispatchResult, error) {
	transcript := strings.TrimSpace(input.Transcript)
	if transcript == "" {
		return voiceCallAgentDispatchResult{}, errors.New("voice call transcript is required")
	}
	if len([]rune(transcript)) > channelMessageMaxLen {
		return voiceCallAgentDispatchResult{}, errors.New("voice call transcript is too long")
	}

	tx, err := bridge.handler.TxStarter.Begin(ctx)
	if err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf("begin voice call agent turn: %w", err)
	}
	defer tx.Rollback(ctx)

	scopedHandler := *bridge.handler
	scopedHandler.DB = tx
	scopedHandler.Queries = bridge.handler.Queries.WithTx(tx)

	scope, err := loadVoiceCallAgentScope(ctx, tx, input.VoiceCallID)
	if err != nil {
		return voiceCallAgentDispatchResult{}, err
	}
	authorizer := &VoiceCallDMAuthorizer{handler: &scopedHandler}
	if err := authorizer.Authorize(ctx, scope); err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf("authorize voice call agent turn: %w", err)
	}

	channel, found := scopedHandler.getChannel(ctx, scope.WorkspaceID, parseUUID(scope.ChannelID))
	if !found {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"%w: direct message disappeared",
			voicecall.ErrScopeNotFound,
		)
	}
	agent, err := scopedHandler.Queries.GetAgentInWorkspace(
		ctx,
		db.GetAgentInWorkspaceParams{
			ID:          parseUUID(scope.AgentID),
			WorkspaceID: parseUUID(scope.WorkspaceID),
		},
	)
	if err != nil || agent.ArchivedAt.Valid {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"%w: agent is unavailable",
			voicecall.ErrScopeUnavailable,
		)
	}

	clientMessageID := voiceCallAgentClientMessageID(input.VoiceCallID, input.RoundID)
	threadID := voiceCallAgentThreadID(input.VoiceCallID, input.RoundID)
	insertInput := channelMessageInsertInput{
		ChannelID:       parseUUID(scope.ChannelID),
		WorkspaceID:     parseUUID(scope.WorkspaceID),
		AuthorID:        parseUUID(scope.UserID),
		AuthorName:      scopedHandler.channelAuthorName(ctx, scope.UserID),
		Content:         transcript,
		Parts:           []protocol.MessagePart{},
		ThreadID:        &threadID,
		ClientMessageID: &clientMessageID,
	}
	inserted, err := insertChannelMessageWithPartsExec(
		ctx,
		tx,
		insertInput.ChannelID,
		insertInput.WorkspaceID,
		"user",
		insertInput.AuthorID,
		insertInput.AuthorName,
		insertInput.Content,
		insertInput.Parts,
		"multica",
		nil,
		insertInput.ClientMessageID,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		pgtype.UUID{},
		insertInput.ThreadID,
		0,
		insertInput.KindHint,
	)
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			return bridge.loadExistingDispatch(ctx, scope, agent, insertInput)
		}
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"persist voice call member turn: %w",
			err,
		)
	}
	message := inserted.Message

	prompt := scopedHandler.buildChannelMentionPrompt(
		ctx,
		channel,
		message,
		channelFacilitatorState{},
	) + voiceCallAgentPrompt
	promptResult, err := scopedHandler.enqueueChannelAgentPromptWithTx(
		ctx,
		scopedHandler.Queries,
		tx,
		channel,
		agent,
		message,
		insertInput.AuthorID,
		prompt,
		protocol.AgentInboxReasonVoiceCall,
		channelDirectedWakePriority,
	)
	if err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"dispatch voice call agent turn: %w",
			err,
		)
	}
	if promptResult.Coalesced {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"%w: directed voice turn was coalesced",
			errVoiceCallAgentTurnConflict,
		)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE channel SET updated_at = now() WHERE id = $1 AND workspace_id = $2`,
		insertInput.ChannelID,
		insertInput.WorkspaceID,
	); err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"touch voice call direct message: %w",
			err,
		)
	}
	if err := tx.Commit(ctx); err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"commit voice call agent turn: %w",
			err,
		)
	}

	postCommitContext := context.WithoutCancel(ctx)
	message = bridge.handler.attachSingleChannelMessageDetails(
		postCommitContext,
		scope.WorkspaceID,
		insertInput.AuthorID,
		message,
	)
	bridge.handler.clearDMHiddenForChannelMembers(
		postCommitContext,
		scope.WorkspaceID,
		insertInput.ChannelID,
	)
	bridge.handler.publishChannelToMembers(
		postCommitContext,
		protocol.EventChannelMessage,
		scope.WorkspaceID,
		"member",
		scope.UserID,
		insertInput.ChannelID,
		message,
	)
	bridge.handler.publishChannelAgentTyping(
		postCommitContext,
		channel,
		agent,
		true,
	)
	bridge.handler.recordChannelAgentPromptWake(
		postCommitContext,
		channel,
		agent,
		message,
		protocol.AgentInboxReasonVoiceCall,
		promptResult,
	)
	if bridge.handler.Metrics != nil {
		bridge.handler.Metrics.RecordChannelFullExecutionWake(protocol.AgentInboxReasonVoiceCall)
	}

	return voiceCallAgentDispatchResult{
		Scope:        scope,
		Channel:      channel,
		Agent:        agent,
		Message:      message,
		Event:        promptResult.Event,
		AgentSession: promptResult.AgentSession,
		Created:      true,
	}, nil
}

func loadVoiceCallAgentScope(
	ctx context.Context,
	tx pgx.Tx,
	callID string,
) (voicecall.Scope, error) {
	var workspaceID, channelID, agentID, userID pgtype.UUID
	var provider, status string
	err := tx.QueryRow(ctx, `
		SELECT workspace_id, channel_id, agent_id, user_id, provider, status
		FROM voice_call_session
		WHERE id = $1
		FOR SHARE`,
		parseUUID(callID),
	).Scan(
		&workspaceID,
		&channelID,
		&agentID,
		&userID,
		&provider,
		&status,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return voicecall.Scope{}, voicecall.ErrCallNotFound
		}
		return voicecall.Scope{}, fmt.Errorf("load voice call agent scope: %w", err)
	}
	if provider != voiceCallAgentProvider {
		return voicecall.Scope{}, fmt.Errorf(
			"%w: unsupported provider",
			errVoiceCallAgentTurnUnavailable,
		)
	}
	switch voicecall.Status(status) {
	case voicecall.StatusConnecting,
		voicecall.StatusActive,
		voicecall.StatusReconnecting:
	default:
		return voicecall.Scope{}, fmt.Errorf(
			"%w: call status %s",
			errVoiceCallAgentTurnUnavailable,
			status,
		)
	}
	return voicecall.Scope{
		WorkspaceID: uuidToString(workspaceID),
		ChannelID:   uuidToString(channelID),
		AgentID:     uuidToString(agentID),
		UserID:      uuidToString(userID),
	}, nil
}

func voiceCallAgentClientMessageID(callID, roundID string) string {
	sum := sha256.Sum256([]byte(roundID))
	return "rtc:" + callID + ":" + hex.EncodeToString(sum[:8])
}

func voiceCallAgentThreadID(callID, roundID string) string {
	return uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte("multica:voice-call:"+callID+":"+roundID),
	).String()
}

func (bridge *VoiceCallAgentBridge) loadExistingDispatch(
	ctx context.Context,
	scope voicecall.Scope,
	agent db.Agent,
	insertInput channelMessageInsertInput,
) (voiceCallAgentDispatchResult, error) {
	messageResult, err := bridge.handler.resolveDuplicateUserChannelMessage(
		ctx,
		insertInput,
		nil,
	)
	if err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"%w: %v",
			errVoiceCallAgentTurnConflict,
			err,
		)
	}

	var eventID pgtype.UUID
	err = bridge.handler.DB.QueryRow(ctx, `
		SELECT id
		FROM agent_inbox_event
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND source_message_id = $3
		  AND reason = $4
		ORDER BY created_at ASC
		LIMIT 1`,
		insertInput.WorkspaceID,
		insertInput.ChannelID,
		parseUUID(messageResult.Message.ID),
		protocol.AgentInboxReasonVoiceCall,
	).Scan(&eventID)
	if err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"%w: persisted turn has no agent event",
			errVoiceCallAgentTurnConflict,
		)
	}
	event, err := bridge.handler.Queries.GetAgentInboxEvent(ctx, eventID)
	if err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"load persisted voice call agent event: %w",
			err,
		)
	}
	agentSession, err := bridge.handler.Queries.GetAgentSession(
		ctx,
		event.AgentSessionID,
	)
	if err != nil {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"load persisted voice call agent session: %w",
			err,
		)
	}
	channel, found := bridge.handler.getChannel(
		ctx,
		scope.WorkspaceID,
		insertInput.ChannelID,
	)
	if !found {
		return voiceCallAgentDispatchResult{}, fmt.Errorf(
			"%w: persisted direct message disappeared",
			errVoiceCallAgentTurnConflict,
		)
	}
	message := bridge.handler.attachSingleChannelMessageDetails(
		ctx,
		scope.WorkspaceID,
		insertInput.AuthorID,
		messageResult.Message,
	)
	return voiceCallAgentDispatchResult{
		Scope:        scope,
		Channel:      channel,
		Agent:        agent,
		Message:      message,
		Event:        event,
		AgentSession: agentSession,
		Created:      false,
	}, nil
}

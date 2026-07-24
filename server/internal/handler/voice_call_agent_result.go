package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	errVoiceCallAgentTurnTimeout = errors.New("voice call agent turn timed out")
	errVoiceCallAgentNoReply     = errors.New("voice call agent produced no reply")
	errVoiceCallAgentHeld        = errors.New("voice call agent reply was held")
	errVoiceCallAgentFailed      = errors.New("voice call agent execution failed")
)

var _ VoiceCallLLMProcessor = (*VoiceCallAgentBridge)(nil)

func (bridge *VoiceCallAgentBridge) Reply(
	ctx context.Context,
	input VoiceCallLLMInput,
) (VoiceCallLLMReply, error) {
	dispatch, err := bridge.dispatch(ctx, input)
	if err != nil {
		return VoiceCallLLMReply{}, err
	}
	content, err := bridge.waitForCompletion(ctx, dispatch)
	if err != nil {
		return VoiceCallLLMReply{}, err
	}
	return VoiceCallLLMReply{Content: content}, nil
}

func (bridge *VoiceCallAgentBridge) waitForCompletion(
	ctx context.Context,
	dispatch voiceCallAgentDispatchResult,
) (string, error) {
	timer := time.NewTimer(bridge.waitTimeout)
	defer timer.Stop()

	for {
		content, done, err := bridge.loadCompletion(ctx, dispatch)
		if err != nil {
			return "", err
		}
		if done {
			return content, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", errVoiceCallAgentTurnTimeout
		case <-time.After(bridge.pollInterval):
		}
	}
}

func (bridge *VoiceCallAgentBridge) loadCompletion(
	ctx context.Context,
	dispatch voiceCallAgentDispatchResult,
) (string, bool, error) {
	var status, terminalOutcome string
	err := bridge.handler.DB.QueryRow(ctx, `
		SELECT status, COALESCE(terminal_outcome, '')
		FROM agent_inbox_event
		WHERE id = $1
		  AND workspace_id = $2
		  AND channel_id = $3`,
		dispatch.Event.ID,
		parseUUID(dispatch.Scope.WorkspaceID),
		parseUUID(dispatch.Scope.ChannelID),
	).Scan(&status, &terminalOutcome)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, fmt.Errorf(
				"%w: inbox event disappeared",
				errVoiceCallAgentTurnConflict,
			)
		}
		return "", false, fmt.Errorf("load voice call agent completion: %w", err)
	}
	if terminalOutcome == "" {
		switch status {
		case "pending", "draining", "failed":
			// A delivery-level failure is retryable in place: the drain query
			// claims both pending and failed events. Only terminal_outcome =
			// failed means the Agent execution has actually ended.
			return "", false, nil
		case "suppressed", "acked":
			return "", false, fmt.Errorf(
				"%w: terminal status %s has no outcome",
				errVoiceCallAgentFailed,
				status,
			)
		default:
			return "", false, fmt.Errorf(
				"%w: unknown inbox status %s",
				errVoiceCallAgentFailed,
				status,
			)
		}
	}

	switch terminalOutcome {
	case "replied":
		content, err := bridge.loadSpokenCompletionContent(ctx, dispatch)
		if err != nil {
			return "", false, err
		}
		if content == "" {
			return "", false, errVoiceCallAgentNoReply
		}
		return content, true, nil
	case "no_reply":
		return "", false, errVoiceCallAgentNoReply
	case "held":
		return "", false, errVoiceCallAgentHeld
	case "failed":
		return "", false, errVoiceCallAgentFailed
	default:
		return "", false, fmt.Errorf(
			"%w: unsupported terminal outcome %s",
			errVoiceCallAgentFailed,
			terminalOutcome,
		)
	}
}

func (bridge *VoiceCallAgentBridge) loadSpokenCompletionContent(
	ctx context.Context,
	dispatch voiceCallAgentDispatchResult,
) (string, error) {
	transportContent, err := bridge.loadTransportCompletionContent(ctx, dispatch)
	if err != nil {
		return "", err
	}
	if transportContent != "" {
		return transportContent, nil
	}

	rows, err := bridge.handler.DB.Query(ctx, `
		SELECT content
		FROM chat_message
		WHERE task_id = $1
		  AND role = 'assistant'
		  AND btrim(content) <> ''
		ORDER BY created_at ASC, id ASC`,
		dispatch.Event.ID,
	)
	if err != nil {
		return "", fmt.Errorf("load voice call assistant completion: %w", err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", fmt.Errorf("scan voice call assistant completion: %w", err)
		}
		if content = strings.TrimSpace(content); content != "" {
			parts = append(parts, content)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate voice call assistant completion: %w", err)
	}
	return strings.Join(parts, "\n"), nil
}

func (bridge *VoiceCallAgentBridge) loadTransportCompletionContent(
	ctx context.Context,
	dispatch voiceCallAgentDispatchResult,
) (string, error) {
	rows, err := bridge.handler.DB.Query(ctx, `
		SELECT message.content
		FROM agent_task_transport_audit audit
		JOIN channel_message message
		  ON message.id = audit.channel_message_id
		WHERE audit.inbox_event_id = $1
		  AND audit.action = 'message_send'
		  AND audit.channel_id = $2
		  AND message.channel_id = $2
		  AND message.deleted_at IS NULL
		  AND btrim(message.content) <> ''
		ORDER BY audit.created_at ASC, audit.id ASC`,
		dispatch.Event.ID,
		parseUUID(dispatch.Scope.ChannelID),
	)
	if err != nil {
		return "", fmt.Errorf("load voice call transport completion: %w", err)
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", fmt.Errorf("scan voice call transport completion: %w", err)
		}
		if content = strings.TrimSpace(content); content != "" {
			parts = append(parts, content)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate voice call transport completion: %w", err)
	}
	return strings.Join(parts, "\n"), nil
}

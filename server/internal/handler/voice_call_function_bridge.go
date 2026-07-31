package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

const (
	voiceCallFunctionUpdateTimeout  = 20 * time.Second
	voiceCallFunctionComfortMessage = "我已经开始处理，请稍等。"
	voiceCallFunctionTimeoutMessage = "任务已交给真实 Agent 继续执行。电话等待已超时，执行过程和结果会保留在当前私聊中。"
	voiceCallFunctionFailureMessage = "任务已交给真实 Agent，但本次执行没有返回可播报结果。请在当前私聊查看执行详情。"
)

type VoiceCallFunctionUpdater interface {
	UpdateVoiceChat(
		ctx context.Context,
		request volcenginertc.UpdateVoiceChatRequest,
	) (volcenginertc.Response, error)
}

type VoiceCallFunctionBridge struct {
	handler     *Handler
	agentBridge *VoiceCallAgentBridge
	updater     VoiceCallFunctionUpdater
	appID       string
	inFlight    sync.Map
}

type voiceCallFunctionIdentity struct {
	CallID string
	TaskID string
}

func NewVoiceCallFunctionBridge(
	handler *Handler,
	agentBridge *VoiceCallAgentBridge,
	updater VoiceCallFunctionUpdater,
	appID string,
) (*VoiceCallFunctionBridge, error) {
	if handler == nil || handler.DB == nil {
		return nil, errors.New("voice call function bridge requires a configured handler")
	}
	if agentBridge == nil {
		return nil, errors.New("voice call function bridge requires an agent bridge")
	}
	if updater == nil {
		return nil, errors.New("voice call function bridge requires an RTC updater")
	}
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, errors.New("voice call function bridge requires an RTC AppId")
	}
	return &VoiceCallFunctionBridge{
		handler:     handler,
		agentBridge: agentBridge,
		updater:     updater,
		appID:       appID,
	}, nil
}

func (bridge *VoiceCallFunctionBridge) HandleFunctionCalls(
	ctx context.Context,
	roomID string,
	message volcenginertc.FunctionCallMessage,
) error {
	roomID = strings.TrimSpace(roomID)
	identity, err := bridge.loadIdentity(ctx, roomID)
	if err != nil {
		return err
	}
	if err := validateFunctionSubscriber(roomID, message.SubscriberUserID); err != nil {
		return err
	}

	for _, tool := range message.ToolCalls {
		request, err := voiceCallAgentRequest(tool)
		if err != nil {
			return err
		}
		key := roomID + ":" + tool.ID
		if _, loaded := bridge.inFlight.LoadOrStore(key, struct{}{}); loaded {
			continue
		}
		dispatch, err := bridge.agentBridge.dispatch(ctx, VoiceCallLLMInput{
			VoiceCallID: identity.CallID,
			RoundID:     tool.ID,
			Transcript:  request,
		})
		if err != nil {
			bridge.inFlight.Delete(key)
			return fmt.Errorf("dispatch voice call function %s: %w", tool.ID, err)
		}
		go bridge.returnResult(
			context.WithoutCancel(ctx),
			key,
			roomID,
			identity.TaskID,
			tool.ID,
			dispatch,
		)
	}
	return nil
}

func (bridge *VoiceCallFunctionBridge) loadIdentity(
	ctx context.Context,
	roomID string,
) (voiceCallFunctionIdentity, error) {
	if roomID == "" {
		return voiceCallFunctionIdentity{}, errors.New(
			"voice call function room ID is required",
		)
	}
	var identity voiceCallFunctionIdentity
	var status string
	err := bridge.handler.DB.QueryRow(ctx, `
		SELECT id, provider_task_id, status
		FROM voice_call_session
		WHERE provider = $1
		  AND room_id = $2`,
		voiceCallAgentProvider,
		roomID,
	).Scan(&identity.CallID, &identity.TaskID, &status)
	if err != nil {
		return voiceCallFunctionIdentity{}, fmt.Errorf(
			"load voice call function scope: %w",
			err,
		)
	}
	switch voicecall.Status(status) {
	case voicecall.StatusConnecting,
		voicecall.StatusActive,
		voicecall.StatusReconnecting:
	default:
		return voiceCallFunctionIdentity{}, fmt.Errorf(
			"%w: call status %s",
			errVoiceCallAgentTurnUnavailable,
			status,
		)
	}
	if strings.TrimSpace(identity.TaskID) == "" {
		return voiceCallFunctionIdentity{}, errors.New(
			"voice call function scope has no provider task ID",
		)
	}
	return identity, nil
}

func validateFunctionSubscriber(roomID, subscriberUserID string) error {
	subscriberUserID = strings.TrimSpace(subscriberUserID)
	if subscriberUserID == "" {
		return nil
	}
	const roomPrefix = "voice-call-"
	if !strings.HasPrefix(roomID, roomPrefix) {
		return errors.New("voice call function room ID has an invalid prefix")
	}
	expected := "voice-member-" + strings.TrimPrefix(roomID, roomPrefix)
	if subscriberUserID != expected {
		return errors.New("voice call function subscriber does not match room scope")
	}
	return nil
}

func voiceCallAgentRequest(tool volcenginertc.FunctionToolCall) (string, error) {
	if tool.Type != "function" ||
		tool.Function.Name != volcenginertc.VoiceAgentToolName {
		return "", fmt.Errorf(
			"voice call function %q is not supported",
			tool.Function.Name,
		)
	}
	var arguments struct {
		Request string `json:"request"`
	}
	decoder := json.NewDecoder(strings.NewReader(tool.Function.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return "", fmt.Errorf("decode voice call agent request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return "", fmt.Errorf("decode voice call agent request: %w", err)
	}
	arguments.Request = strings.TrimSpace(arguments.Request)
	if arguments.Request == "" {
		return "", errors.New("voice call agent request is required")
	}
	if !utf8.ValidString(arguments.Request) ||
		len([]rune(arguments.Request)) > channelMessageMaxLen {
		return "", errors.New("voice call agent request is too long or invalid")
	}
	return arguments.Request, nil
}

func (bridge *VoiceCallFunctionBridge) returnResult(
	ctx context.Context,
	key string,
	roomID string,
	taskID string,
	toolCallID string,
	dispatch voiceCallAgentDispatchResult,
) {
	defer bridge.inFlight.Delete(key)

	if _, err := bridge.updateVoiceChat(
		ctx,
		volcenginertc.UpdateVoiceChatRequest{
			AppID:         bridge.appID,
			RoomID:        roomID,
			TaskID:        taskID,
			Command:       volcenginertc.UpdateCommandExternalTextToSpeech,
			Message:       voiceCallFunctionComfortMessage,
			InterruptMode: 2,
		},
	); err != nil {
		slog.Warn(
			"send voice call function comfort speech",
			"room_id", roomID,
			"tool_call_id", toolCallID,
			"error", err,
		)
	}

	content, err := bridge.agentBridge.waitForCompletion(ctx, dispatch)
	if err != nil {
		if errors.Is(err, errVoiceCallAgentTurnTimeout) {
			content = voiceCallFunctionTimeoutMessage
		} else {
			content = voiceCallFunctionFailureMessage
		}
	}
	message, marshalErr := json.Marshal(struct {
		ToolCallID string `json:"ToolCallID"`
		Content    string `json:"Content"`
	}{
		ToolCallID: toolCallID,
		Content:    content,
	})
	if marshalErr != nil {
		slog.Error(
			"encode voice call function result",
			"room_id", roomID,
			"tool_call_id", toolCallID,
			"error", marshalErr,
		)
		return
	}
	_, updateErr := bridge.updateVoiceChat(
		ctx,
		volcenginertc.UpdateVoiceChatRequest{
			AppID:   bridge.appID,
			RoomID:  roomID,
			TaskID:  taskID,
			Command: volcenginertc.UpdateCommandFunction,
			Message: string(message),
		},
	)
	if updateErr != nil {
		slog.Error(
			"return voice call function result",
			"room_id", roomID,
			"tool_call_id", toolCallID,
			"error", updateErr,
		)
	}
}

func (bridge *VoiceCallFunctionBridge) updateVoiceChat(
	ctx context.Context,
	request volcenginertc.UpdateVoiceChatRequest,
) (volcenginertc.Response, error) {
	updateContext, cancel := context.WithTimeout(ctx, voiceCallFunctionUpdateTimeout)
	defer cancel()
	return bridge.updater.UpdateVoiceChat(updateContext, request)
}

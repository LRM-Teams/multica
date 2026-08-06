package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/service/duplexcall"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

const duplexStopReason = "duplex_client_stop"

var duplexUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type VoiceCallDuplexAPI interface {
	ActivateDuplex(context.Context, voicecall.AnswerInput) (voicecall.Session, error)
	EndWithoutProviderStop(context.Context, voicecall.StopInput) (voicecall.Session, error)
	Get(context.Context, string, string, string) (voicecall.Session, error)
}

// DuplexGatewayAPI is the Doubao Duplex media gateway surface used by handlers.
type DuplexGatewayAPI interface {
	Configured() bool
	Has(callID string) bool
	MarkPending(callID string)
	Close(callID string)
	Start(
		ctx context.Context,
		callID string,
		executor duplexcall.MulticaExecutor,
		emit duplexcall.Emitter,
	) (*duplexcall.Session, error)
}

type duplexStartResponse struct {
	Call voiceCallResponse `json:"call"`
	Mode string            `json:"mode"`
	WSPath string          `json:"ws_path"`
	Audio  duplexAudioHint `json:"audio"`
	Events duplexEventHint `json:"events"`
}

type duplexAudioHint struct {
	InputFormat  string `json:"input_format"`
	InputRate    int    `json:"input_sample_rate"`
	OutputFormat string `json:"output_format"`
	OutputRate   int    `json:"output_sample_rate"`
}

type duplexEventHint struct {
	Client []string `json:"client"`
	Server []string `json:"server"`
}

// StartVoiceCallDuplex activates Duplex media on an existing voice call without
// starting RTC VoiceChat. FE then connects the duplex WebSocket.
func (h *Handler) StartVoiceCallDuplex(w http.ResponseWriter, r *http.Request) {
	if h.DuplexGateway == nil || !h.DuplexGateway.Configured() {
		writeCodedError(
			w,
			http.StatusServiceUnavailable,
			"duplex_not_configured",
			"doubao duplex is not configured (set DOUBAO_DIALOG_API_KEY)",
		)
		return
	}
	service := h.duplexVoiceCallService()
	if service == nil {
		writeCodedError(
			w,
			http.StatusServiceUnavailable,
			"voice_call_not_configured",
			"voice calling is not configured",
		)
		return
	}
	workspaceID, userID, ok := voiceCallRequestScope(w, r)
	if !ok {
		return
	}
	callID, ok := voiceCallIDFromRequest(w, r)
	if !ok {
		return
	}

	session, err := service.ActivateDuplex(r.Context(), voicecall.AnswerInput{
		WorkspaceID: workspaceID,
		UserID:      userID,
		CallID:      callID,
	})
	if err != nil {
		writeVoiceCallServiceError(w, "duplex_start", err)
		return
	}
	h.DuplexGateway.MarkPending(callID)
	h.publishVoiceCallUpdated(session)

	writeJSON(w, http.StatusOK, duplexStartResponse{
		Call:   voiceCallResponseFromSession(session),
		Mode:   "duplex",
		WSPath: "/api/workspaces/" + workspaceID + "/voice-calls/" + callID + "/duplex/ws",
		Audio: duplexAudioHint{
			InputFormat:  "pcm_s16le",
			InputRate:    16000,
			OutputFormat: "pcm_s16le",
			OutputRate:   24000,
		},
		Events: duplexEventHint{
			Client: []string{
				duplexcall.ClientAudioAppend,
				duplexcall.ClientAudioCommit,
				duplexcall.ClientInterrupt,
				duplexcall.ClientClose,
			},
			Server: []string{
				duplexcall.ServerReady,
				duplexcall.ServerASR,
				duplexcall.ServerAudioDelta,
				duplexcall.ServerTextDelta,
				duplexcall.ServerTool,
				duplexcall.ServerError,
				duplexcall.ServerClosed,
			},
		},
	})
}

// VoiceCallDuplexWS proxies browser PCM/events ↔ Doubao Duplex ↔ Multica tools.
func (h *Handler) VoiceCallDuplexWS(w http.ResponseWriter, r *http.Request) {
	if h.DuplexGateway == nil || !h.DuplexGateway.Configured() {
		writeCodedError(
			w,
			http.StatusServiceUnavailable,
			"duplex_not_configured",
			"doubao duplex is not configured (set DOUBAO_DIALOG_API_KEY)",
		)
		return
	}
	service := h.duplexVoiceCallService()
	if service == nil || h.VoiceCallAgentBridge == nil {
		writeCodedError(
			w,
			http.StatusServiceUnavailable,
			"voice_call_not_configured",
			"voice calling agent bridge is not configured",
		)
		return
	}
	workspaceID, userID, ok := voiceCallRequestScope(w, r)
	if !ok {
		return
	}
	callID, ok := voiceCallIDFromRequest(w, r)
	if !ok {
		return
	}

	session, err := service.Get(r.Context(), workspaceID, userID, callID)
	if err != nil {
		writeVoiceCallServiceError(w, "duplex_ws", err)
		return
	}
	switch session.Status {
	case voicecall.StatusActive, voicecall.StatusConnecting, voicecall.StatusReconnecting:
	default:
		writeCodedError(
			w,
			http.StatusConflict,
			"duplex_call_not_active",
			"call must be activated for duplex before opening the websocket",
		)
		return
	}

	conn, err := duplexUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("duplex websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	var writeMu sync.Mutex
	emit := func(event duplexcall.ServerEvent) error {
		payload, err := duplexcall.EncodeServerEvent(event)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, payload)
	}

	executor := &voiceCallDuplexExecutor{
		bridge: h.VoiceCallAgentBridge,
		callID: callID,
	}
	duplexSession, err := h.DuplexGateway.Start(r.Context(), callID, executor, emit)
	if err != nil {
		_ = emit(duplexcall.ServerEvent{
			Type:    duplexcall.ServerError,
			CallID:  callID,
			Code:    "duplex_start_failed",
			Message: err.Error(),
		})
		_ = emit(duplexcall.ServerEvent{Type: duplexcall.ServerClosed, CallID: callID})
		return
	}
	defer func() {
		duplexSession.Close()
		duplexSession.Wait()
		if _, endErr := service.EndWithoutProviderStop(context.WithoutCancel(r.Context()), voicecall.StopInput{
			WorkspaceID: workspaceID,
			UserID:      userID,
			CallID:      callID,
			Reason:      duplexStopReason,
		}); endErr != nil {
			slog.Warn("duplex end without provider stop failed", "call_id", callID, "error", endErr)
		} else if ended, getErr := service.Get(context.WithoutCancel(r.Context()), workspaceID, userID, callID); getErr == nil {
			h.publishVoiceCallUpdated(ended)
		}
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			duplexSession.Close()
			return
		}
		clientEvent, err := duplexcall.ParseClientEvent(data)
		if err != nil {
			_ = emit(duplexcall.ServerEvent{
				Type:    duplexcall.ServerError,
				CallID:  callID,
				Code:    "duplex_invalid_client_event",
				Message: "invalid client event json",
			})
			continue
		}
		if err := duplexSession.HandleClientEvent(r.Context(), clientEvent); err != nil {
			_ = emit(duplexcall.ServerEvent{
				Type:    duplexcall.ServerError,
				CallID:  callID,
				Code:    "duplex_client_event_failed",
				Message: err.Error(),
			})
			if clientEvent.Type == duplexcall.ClientClose {
				return
			}
		}
		if clientEvent.Type == duplexcall.ClientClose {
			return
		}
	}
}

func (h *Handler) duplexVoiceCallService() VoiceCallDuplexAPI {
	if h == nil {
		return nil
	}
	if duplex, ok := h.VoiceCallService.(VoiceCallDuplexAPI); ok {
		return duplex
	}
	return nil
}

type voiceCallDuplexExecutor struct {
	bridge *VoiceCallAgentBridge
	callID string
}

func (e *voiceCallDuplexExecutor) Delegate(ctx context.Context, request string) (string, error) {
	if e == nil || e.bridge == nil {
		return "", errors.New("duplex multica executor is not configured")
	}
	reply, err := e.bridge.Reply(ctx, VoiceCallLLMInput{
		VoiceCallID: e.callID,
		RoundID:     "duplex-" + uuid.NewString(),
		Transcript:  request,
	})
	if err != nil {
		if errors.Is(err, errVoiceCallAgentTurnTimeout) {
			return "任务已交给真实 Agent 继续执行。电话等待已超时，执行过程和结果会保留在当前私聊中。", nil
		}
		return "", err
	}
	content := strings.TrimSpace(reply.Content)
	if content == "" {
		return "任务已交给真实 Agent，请在当前私聊查看执行详情。", nil
	}
	return content, nil
}

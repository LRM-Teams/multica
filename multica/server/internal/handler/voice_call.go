package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	"github.com/multica-ai/multica/server/internal/util"
)

const maxVoiceCallRequestBytes = 8 << 10

type VoiceCallServiceAPI interface {
	Start(context.Context, voicecall.StartInput) (voicecall.StartResult, error)
	Connect(context.Context, voicecall.ConnectInput) (voicecall.Session, error)
	Answer(context.Context, voicecall.AnswerInput) (voicecall.Session, error)
	Get(context.Context, string, string, string) (voicecall.Session, error)
	Stop(context.Context, voicecall.StopInput) (voicecall.Session, error)
}

type createVoiceCallRequest struct {
	ChannelID string `json:"channel_id"`
	AgentID   string `json:"agent_id"`
}

type voiceCallResponse struct {
	ID            string           `json:"id"`
	ChannelID     string           `json:"channel_id"`
	AgentID       string           `json:"agent_id"`
	Status        voicecall.Status `json:"status"`
	StartedAt     time.Time        `json:"started_at"`
	ConnectedAt   *time.Time       `json:"connected_at,omitempty"`
	EndedAt       *time.Time       `json:"ended_at,omitempty"`
	EndReason     string           `json:"end_reason,omitempty"`
	ErrorCode     string           `json:"error_code,omitempty"`
	InputAudioMS  int64            `json:"input_audio_ms"`
	OutputAudioMS int64            `json:"output_audio_ms"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type voiceCallMediaResponse struct {
	AppID     string    `json:"app_id"`
	RoomID    string    `json:"room_id"`
	UserID    string    `json:"user_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type createVoiceCallResponse struct {
	Call  voiceCallResponse      `json:"call"`
	Media voiceCallMediaResponse `json:"media"`
}

type getVoiceCallResponse struct {
	Call voiceCallResponse `json:"call"`
}

func (h *Handler) CreateVoiceCall(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallService == nil {
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

	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceCallRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request createVoiceCallRequest
	if err := decoder.Decode(&request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeCodedError(
				w,
				http.StatusRequestEntityTooLarge,
				"voice_call_request_too_large",
				"voice call request exceeds 8 KiB",
			)
			return
		}
		writeCodedError(
			w,
			http.StatusBadRequest,
			"invalid_voice_call_request",
			"invalid request body",
		)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeCodedError(
			w,
			http.StatusBadRequest,
			"invalid_voice_call_request",
			"request body must contain one JSON object",
		)
		return
	}
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	request.AgentID = strings.TrimSpace(request.AgentID)
	if _, err := util.ParseUUID(request.ChannelID); err != nil {
		writeCodedError(
			w,
			http.StatusBadRequest,
			"invalid_voice_call_request",
			"channel_id must be a UUID",
		)
		return
	}
	if _, err := util.ParseUUID(request.AgentID); err != nil {
		writeCodedError(
			w,
			http.StatusBadRequest,
			"invalid_voice_call_request",
			"agent_id must be a UUID",
		)
		return
	}

	result, err := h.VoiceCallService.Start(r.Context(), voicecall.StartInput{
		WorkspaceID: workspaceID,
		ChannelID:   request.ChannelID,
		AgentID:     request.AgentID,
		UserID:      userID,
	})
	if err != nil {
		writeVoiceCallServiceError(w, "create", err)
		return
	}
	h.publishVoiceCallUpdated(result.Session)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, createVoiceCallResponse{
		Call: voiceCallResponseFromSession(result.Session),
		Media: voiceCallMediaResponse{
			AppID:     result.Media.AppID,
			RoomID:    result.Media.RoomID,
			UserID:    result.Media.UserID,
			Token:     result.Media.Token,
			ExpiresAt: result.Media.ExpiresAt,
		},
	})
}

func (h *Handler) ConnectVoiceCall(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallService == nil {
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
	session, err := h.VoiceCallService.Connect(r.Context(), voicecall.ConnectInput{
		WorkspaceID: workspaceID,
		UserID:      userID,
		CallID:      callID,
	})
	if err != nil {
		writeVoiceCallServiceError(w, "connect", err)
		return
	}
	h.publishVoiceCallUpdated(session)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, getVoiceCallResponse{
		Call: voiceCallResponseFromSession(session),
	})
}

func (h *Handler) AnswerVoiceCall(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallService == nil {
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
	session, err := h.VoiceCallService.Answer(r.Context(), voicecall.AnswerInput{
		WorkspaceID: workspaceID,
		UserID:      userID,
		CallID:      callID,
	})
	if err != nil {
		writeVoiceCallServiceError(w, "answer", err)
		return
	}
	h.publishVoiceCallUpdated(session)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, getVoiceCallResponse{
		Call: voiceCallResponseFromSession(session),
	})
}

func (h *Handler) GetVoiceCall(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallService == nil {
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
	session, err := h.VoiceCallService.Get(
		r.Context(),
		workspaceID,
		userID,
		callID,
	)
	if err != nil {
		writeVoiceCallServiceError(w, "get", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, getVoiceCallResponse{
		Call: voiceCallResponseFromSession(session),
	})
}

func (h *Handler) StopVoiceCall(w http.ResponseWriter, r *http.Request) {
	if h.VoiceCallService == nil {
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
	stopInput := voicecall.StopInput{
		WorkspaceID: workspaceID,
		UserID:      userID,
		CallID:      callID,
		Reason:      "user_hangup",
	}
	var (
		session voicecall.Session
		err     error
	)
	// Duplex media never started RTC VoiceChat; skip provider.Stop.
	if h.DuplexGateway != nil && h.DuplexGateway.Has(callID) {
		h.DuplexGateway.Close(callID)
		if duplex, ok := h.VoiceCallService.(VoiceCallDuplexAPI); ok {
			session, err = duplex.EndWithoutProviderStop(r.Context(), stopInput)
		} else {
			session, err = h.VoiceCallService.Stop(r.Context(), stopInput)
		}
	} else {
		session, err = h.VoiceCallService.Stop(r.Context(), stopInput)
	}
	if err != nil {
		writeVoiceCallServiceError(w, "stop", err)
		return
	}
	h.publishVoiceCallUpdated(session)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, getVoiceCallResponse{
		Call: voiceCallResponseFromSession(session),
	})
}

func voiceCallRequestScope(
	w http.ResponseWriter,
	r *http.Request,
) (string, string, bool) {
	workspaceID := strings.TrimSpace(workspaceIDFromURL(r, "id"))
	if _, err := util.ParseUUID(workspaceID); err != nil {
		writeCodedError(
			w,
			http.StatusBadRequest,
			"invalid_voice_call_request",
			"workspace id must be a UUID",
		)
		return "", "", false
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return "", "", false
	}
	if _, err := util.ParseUUID(userID); err != nil {
		writeCodedError(
			w,
			http.StatusUnauthorized,
			"invalid_authenticated_member",
			"authenticated member is invalid",
		)
		return "", "", false
	}
	return workspaceID, userID, true
}

func voiceCallIDFromRequest(
	w http.ResponseWriter,
	r *http.Request,
) (string, bool) {
	callID := strings.TrimSpace(chi.URLParam(r, "callId"))
	if _, err := util.ParseUUID(callID); err != nil {
		writeCodedError(
			w,
			http.StatusBadRequest,
			"invalid_voice_call_request",
			"call id must be a UUID",
		)
		return "", false
	}
	return callID, true
}

func voiceCallResponseFromSession(session voicecall.Session) voiceCallResponse {
	return voiceCallResponse{
		ID:            session.ID,
		ChannelID:     session.ChannelID,
		AgentID:       session.AgentID,
		Status:        session.Status,
		StartedAt:     session.StartedAt,
		ConnectedAt:   session.ConnectedAt,
		EndedAt:       session.EndedAt,
		EndReason:     session.EndReason,
		ErrorCode:     session.ErrorCode,
		InputAudioMS:  session.InputAudioMS,
		OutputAudioMS: session.OutputAudioMS,
		UpdatedAt:     session.UpdatedAt,
	}
}

func writeVoiceCallServiceError(
	w http.ResponseWriter,
	operation string,
	err error,
) {
	switch {
	case errors.Is(err, voicecall.ErrCallNotFound),
		errors.Is(err, voicecall.ErrScopeNotFound):
		writeCodedError(
			w,
			http.StatusNotFound,
			"voice_call_not_found",
			"voice call not found",
		)
	case errors.Is(err, voicecall.ErrScopeForbidden):
		writeCodedError(
			w,
			http.StatusForbidden,
			"voice_call_forbidden",
			"voice call is not permitted",
		)
	case errors.Is(err, voicecall.ErrScopeUnavailable):
		writeCodedError(
			w,
			http.StatusConflict,
			"voice_call_unavailable",
			"voice call is unavailable for this conversation",
		)
	case errors.Is(err, voicecall.ErrCallAlreadyActive):
		writeCodedError(
			w,
			http.StatusConflict,
			"voice_call_already_active",
			"an active voice call already exists",
		)
	case errors.Is(err, voicecall.ErrProviderFailure):
		slog.Error(
			"voice call provider request failed",
			"operation",
			operation,
			"error",
			err,
		)
		writeCodedError(
			w,
			http.StatusBadGateway,
			"voice_call_provider_failed",
			"voice call provider request failed",
		)
	default:
		slog.Error(
			"voice call request failed",
			"operation",
			operation,
			"error",
			err,
		)
		writeCodedError(
			w,
			http.StatusInternalServerError,
			"voice_call_failed",
			"voice call request failed",
		)
	}
}

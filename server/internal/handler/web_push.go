package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type webPushPublicKeyResponse struct {
	PublicKey string `json:"public_key"`
	Enabled   bool   `json:"enabled"`
}

type webPushSubscriptionKeysRequest struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type webPushSubscriptionRequest struct {
	Endpoint       string                         `json:"endpoint"`
	Keys           webPushSubscriptionKeysRequest `json:"keys"`
	ExpirationTime *int64                         `json:"expiration_time,omitempty"`
	DeviceID       *string                        `json:"device_id,omitempty"`
	UserAgent      *string                        `json:"user_agent,omitempty"`
}

type webPushBindRequest struct {
	Subscription webPushSubscriptionRequest `json:"subscription"`
	DeviceID     *string                    `json:"device_id,omitempty"`
	UserAgent    *string                    `json:"user_agent,omitempty"`
}

type webPushUnbindRequest struct {
	Endpoint string `json:"endpoint"`
}

type webPushSubscriptionResponse struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspace_id"`
	UserID         string  `json:"user_id"`
	Endpoint       string  `json:"endpoint"`
	ExpirationTime *string `json:"expiration_time,omitempty"`
	DeviceID       *string `json:"device_id,omitempty"`
	UserAgent      *string `json:"user_agent,omitempty"`
	LastActiveAt   string  `json:"last_active_at"`
}

func (h *Handler) GetWebPushPublicKey(w http.ResponseWriter, _ *http.Request) {
	key := strings.TrimSpace(h.cfg.WebPushVAPIDPublicKey)
	writeJSON(w, http.StatusOK, webPushPublicKeyResponse{PublicKey: key, Enabled: key != ""})
}

func (h *Handler) UpsertWebPushSubscription(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}

	var req webPushBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sub := req.Subscription
	if strings.TrimSpace(sub.Endpoint) == "" || strings.TrimSpace(sub.Keys.P256dh) == "" || strings.TrimSpace(sub.Keys.Auth) == "" {
		writeError(w, http.StatusBadRequest, "subscription endpoint and keys are required")
		return
	}

	deviceID := firstNonEmptyPtr(sub.DeviceID, req.DeviceID)
	userAgent := firstNonEmptyPtr(sub.UserAgent, req.UserAgent)
	if userAgent == nil {
		ua := strings.TrimSpace(r.UserAgent())
		if ua != "" {
			userAgent = &ua
		}
	}

	row, err := h.Queries.UpsertWebPushSubscription(r.Context(), db.UpsertWebPushSubscriptionParams{
		WorkspaceID:    wsUUID,
		UserID:         userUUID,
		Endpoint:       strings.TrimSpace(sub.Endpoint),
		P256dh:         strings.TrimSpace(sub.Keys.P256dh),
		Auth:           strings.TrimSpace(sub.Keys.Auth),
		ExpirationTime: subscriptionExpirationTime(sub.ExpirationTime),
		DeviceID:       nullableText(deviceID),
		UserAgent:      nullableText(userAgent),
	})
	if err != nil {
		slog.Warn("UpsertWebPushSubscription failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to bind web push subscription")
		return
	}

	writeJSON(w, http.StatusOK, webPushSubscriptionToResponse(row))
}

func (h *Handler) DeleteWebPushSubscription(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}

	var req webPushUnbindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint is required")
		return
	}

	if _, err := h.Queries.DeleteWebPushSubscription(r.Context(), db.DeleteWebPushSubscriptionParams{
		UserID:   userUUID,
		Endpoint: endpoint,
	}); err != nil {
		slog.Warn("DeleteWebPushSubscription failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to unbind web push subscription")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func firstNonEmptyPtr(values ...*string) *string {
	for _, value := range values {
		if value == nil {
			continue
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed != "" {
			return &trimmed
		}
	}
	return nil
}

func nullableText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return strToText(*value)
}

func subscriptionExpirationTime(ms *int64) pgtype.Timestamptz {
	if ms == nil || *ms <= 0 {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(*ms), Valid: true}
}

func webPushSubscriptionToResponse(sub db.WebPushSubscription) webPushSubscriptionResponse {
	return webPushSubscriptionResponse{
		ID:             uuidToString(sub.ID),
		WorkspaceID:    uuidToString(sub.WorkspaceID),
		UserID:         uuidToString(sub.UserID),
		Endpoint:       sub.Endpoint,
		ExpirationTime: timestampToPtr(sub.ExpirationTime),
		DeviceID:       textToPtr(sub.DeviceID),
		UserAgent:      textToPtr(sub.UserAgent),
		LastActiveAt:   timestampToString(sub.LastActiveAt),
	}
}

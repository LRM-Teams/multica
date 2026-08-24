package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service/webpush"
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

// LRM-755 / LRM-679: settings "Send test notification" hits the real push
// path (VAPID → push service → SW showNotification) so Frank can self-verify
// closed-page OS banners without needing a second device to DM him.
type webPushTestResponse struct {
	OK        bool `json:"ok"`
	Delivered int  `json:"delivered"`
	Failed    int  `json:"failed"`
	Gone      int  `json:"gone"`
	Attempted int  `json:"attempted"`
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
		DeviceID:       optionalPgText(deviceID),
		UserAgent:      optionalPgText(userAgent),
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

func (h *Handler) SendTestWebPush(w http.ResponseWriter, r *http.Request) {
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

	sender := webpush.NewSender(webpush.Config{
		PublicKey:  h.cfg.WebPushVAPIDPublicKey,
		PrivateKey: h.cfg.WebPushVAPIDPrivateKey,
		Subject:    h.cfg.WebPushVAPIDSubject,
	})
	if !sender.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "web push is not configured on this server")
		return
	}

	subs, err := h.Queries.ListActiveWebPushSubscriptions(r.Context(), userUUID)
	if err != nil {
		slog.Warn("SendTestWebPush list failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list web push subscriptions")
		return
	}
	if len(subs) == 0 {
		writeError(w, http.StatusNotFound, "no active web push subscription — enable browser notifications on this device first")
		return
	}

	slug := ""
	if ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID); err == nil {
		slug = ws.Slug
	}
	path := "/"
	if slug != "" {
		path = "/" + slug + "/settings?tab=notifications"
	}
	appBase := strings.TrimRight(h.cfg.WebPushAppURL, "/")
	if appBase == "" {
		appBase = strings.TrimRight(h.cfg.PublicURL, "/")
	}
	url := path
	if appBase != "" {
		url = appBase + path
	}
	icon := ""
	if appBase != "" {
		// PNG — WebKit/iOS drops or hides SVG notification icons (LRM-684).
		icon = appBase + "/icon-192.png"
	}
	payload := map[string]any{
		"title":               "Multica",
		"body":                "Test notification — Web Push works on this device.",
		"url":                 url,
		"slug":                slug,
		"item_id":             "web-push-test",
		"require_interaction": true,
		"timestamp":           time.Now().UnixMilli(),
	}
	if icon != "" {
		payload["icon"] = icon
		payload["badge"] = icon
	}

	delivered, failed, gone := 0, 0, 0
	var goneEndpoints []string
	seen := make(map[string]struct{}, len(subs))
	for _, sub := range subs {
		if _, ok := seen[sub.Endpoint]; ok {
			continue
		}
		seen[sub.Endpoint] = struct{}{}
		res, sendErr := sender.Send(r.Context(), webpush.Subscription{
			Endpoint: sub.Endpoint,
			P256DH:   sub.P256dh,
			Auth:     sub.Auth,
		}, payload)
		if res.Gone {
			gone++
			goneEndpoints = append(goneEndpoints, sub.Endpoint)
			slog.Warn("web push test: delivery gone",
				"workspace_id", workspaceID,
				"recipient_id", userID,
				"endpoint_hash", webPushEndpointHashForHandler(sub.Endpoint),
				"status", res.StatusCode,
			)
			continue
		}
		if sendErr != nil {
			failed++
			slog.Warn("web push test: delivery failed",
				"workspace_id", workspaceID,
				"recipient_id", userID,
				"endpoint_hash", webPushEndpointHashForHandler(sub.Endpoint),
				"status", res.StatusCode,
				"error", sendErr,
			)
			continue
		}
		delivered++
	}
	if len(goneEndpoints) > 0 {
		if _, err := h.Queries.DeleteWebPushSubscriptionsByEndpoints(r.Context(), db.DeleteWebPushSubscriptionsByEndpointsParams{
			UserID:    userUUID,
			Column2:   goneEndpoints,
		}); err != nil {
			slog.Warn("web push test: cleanup gone failed", append(logger.RequestAttrs(r), "error", err)...)
		}
	}

	if delivered == 0 {
		if gone > 0 && failed == 0 {
			writeError(w, http.StatusGone, "push subscription expired — re-enable browser notifications on this device")
			return
		}
		writeError(w, http.StatusBadGateway, "push service rejected the test notification")
		return
	}

	writeJSON(w, http.StatusOK, webPushTestResponse{
		OK:        true,
		Delivered: delivered,
		Failed:    failed,
		Gone:      gone,
		Attempted: delivered + failed + gone,
	})
}

func webPushEndpointHashForHandler(endpoint string) string {
	// Match listeners: 12-char sha256 prefix, no full endpoint in logs.
	sum := sha256.Sum256([]byte(strings.TrimSpace(endpoint)))
	return hex.EncodeToString(sum[:])[:12]
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

func optionalPgText(value *string) pgtype.Text {
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

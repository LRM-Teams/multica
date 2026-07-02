package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/service/webpush"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type webPushInboxPayload struct {
	Slug     string `json:"slug"`
	ItemID   string `json:"item_id"`
	IssueKey string `json:"issue_key"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
}

func registerWebPushListeners(bus *events.Bus, queries *db.Queries, cfg handler.Config) {
	sender := webpush.NewSender(webpush.Config{
		PublicKey:  cfg.WebPushVAPIDPublicKey,
		PrivateKey: cfg.WebPushVAPIDPrivateKey,
		Subject:    cfg.WebPushVAPIDSubject,
	})
	if !sender.Enabled() {
		slog.Info("web push disabled: VAPID public/private key or subject missing")
		return
	}

	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		item, ok := extractInboxItem(e.Payload)
		if !ok || item.RecipientType != "member" || item.RecipientID == "" {
			return
		}
		go deliverWebPushInbox(queries, sender, cfg, item)
	})
}

func deliverWebPushInbox(queries *db.Queries, sender *webpush.Sender, cfg handler.Config, item handler.InboxItemResponse) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if isSystemNotificationMuted(ctx, queries, item.WorkspaceID, item.RecipientID) {
		return
	}
	subs, err := queries.ListActiveWebPushSubscriptions(ctx, parseUUID(item.RecipientID))
	if err != nil || len(subs) == 0 {
		if err != nil {
			slog.Error("web push: list subscriptions failed", "error", err)
		}
		return
	}
	payload := buildWebPushInboxPayload(ctx, queries, item, cfg.WebPushAppURL)
	var gone []string
	seen := make(map[string]struct{}, len(subs))
	for _, sub := range subs {
		if _, ok := seen[sub.Endpoint]; ok {
			continue
		}
		seen[sub.Endpoint] = struct{}{}
		res, err := sender.Send(ctx, webpush.Subscription{Endpoint: sub.Endpoint, P256DH: sub.P256dh, Auth: sub.Auth}, payload)
		if res.Gone {
			gone = append(gone, sub.Endpoint)
			continue
		}
		if err != nil {
			slog.Warn("web push: delivery failed", "endpoint", sub.Endpoint, "status", res.StatusCode, "error", err)
		}
	}
	if len(gone) > 0 {
		_, err := queries.DeleteWebPushSubscriptionsByEndpoints(ctx, db.DeleteWebPushSubscriptionsByEndpointsParams{
			UserID:    parseUUID(item.RecipientID),
			Endpoints: gone,
		})
		if err != nil {
			slog.Error("web push: cleanup failed subscriptions", "error", err)
		}
	}
}

func extractInboxItem(payload any) (handler.InboxItemResponse, bool) {
	if wrapper, ok := payload.(map[string]any); ok {
		if item, ok := wrapper["item"]; ok {
			return decodeInboxItem(item)
		}
	}
	return decodeInboxItem(payload)
}

func decodeInboxItem(raw any) (handler.InboxItemResponse, bool) {
	if item, ok := raw.(handler.InboxItemResponse); ok {
		return item, true
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return handler.InboxItemResponse{}, false
	}
	var item handler.InboxItemResponse
	if err := json.Unmarshal(b, &item); err != nil {
		return handler.InboxItemResponse{}, false
	}
	return item, item.ID != ""
}

func isSystemNotificationMuted(ctx context.Context, queries *db.Queries, workspaceID, userID string) bool {
	pref, err := queries.GetNotificationPreference(ctx, db.GetNotificationPreferenceParams{
		WorkspaceID: parseUUID(workspaceID),
		UserID:      parseUUID(userID),
	})
	if err != nil {
		return false
	}
	var prefs map[string]string
	if err := json.Unmarshal(pref.Preferences, &prefs); err != nil {
		return false
	}
	return prefs["system_notifications"] == "muted"
}

func buildWebPushInboxPayload(ctx context.Context, queries *db.Queries, item handler.InboxItemResponse, appURL string) webPushInboxPayload {
	slug := ""
	if ws, err := queries.GetWorkspace(ctx, parseUUID(item.WorkspaceID)); err == nil {
		slug = ws.Slug
	}
	issueKey := item.ID
	if item.IssueID != nil && *item.IssueID != "" {
		issueKey = *item.IssueID
	}
	body := ""
	if item.Body != nil {
		body = strings.TrimSpace(*item.Body)
		if runes := []rune(body); len(runes) > 180 {
			body = string(runes[:180])
		}
	}
	path := ""
	if slug != "" {
		path = "/" + slug + "/inbox?issue=" + urlQueryEscape(issueKey)
	}
	return webPushInboxPayload{
		Slug:     slug,
		ItemID:   item.ID,
		IssueKey: issueKey,
		Title:    item.Title,
		Body:     body,
		URL:      strings.TrimRight(appURL, "/") + path,
	}
}

func urlQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

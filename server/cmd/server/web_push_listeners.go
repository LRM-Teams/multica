package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/service/webpush"
	"github.com/multica-ai/multica/server/internal/util"
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

type webPushChannelInfo struct {
	Name  string
	Kind  string
	Muted bool
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
	bus.Subscribe(protocol.EventChannelMessage, func(e events.Event) {
		msg, ok := extractChannelMessage(e.Payload)
		if !ok {
			return
		}
		recipients := e.RecipientUserIDs
		if len(recipients) == 0 {
			recipients = channelHumanMemberIDsForWebPush(context.Background(), queries, e.WorkspaceID, msg.ChannelID)
		}
		for _, recipientID := range uniqueStringList(recipients) {
			recipientID := recipientID
			go deliverWebPushChannelMessage(queries, sender, cfg, e, msg, recipientID)
		}
	})
}

func deliverWebPushChannelMessage(queries *db.Queries, sender *webpush.Sender, cfg handler.Config, event events.Event, msg handler.ChannelMessageResponse, recipientID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if isSystemNotificationMuted(ctx, queries, msg.WorkspaceID, recipientID) {
		return
	}
	info, err := webPushChannelInfoForRecipient(ctx, queries, msg.WorkspaceID, msg.ChannelID, recipientID)
	if err != nil {
		slog.Warn("web push: channel recipient lookup failed", "workspace_id", msg.WorkspaceID, "channel_id", msg.ChannelID, "recipient_id", recipientID, "error", err)
		return
	}
	if !shouldDeliverChannelMessageWebPush(msg, event.ActorType, event.ActorID, recipientID, info.Muted) {
		return
	}
	subs, err := queries.ListActiveWebPushSubscriptions(ctx, parseUUID(recipientID))
	if err != nil || len(subs) == 0 {
		if err != nil {
			slog.Error("web push: list subscriptions failed", "error", err)
		}
		return
	}
	payload := buildWebPushChannelPayload(ctx, queries, msg, info, cfg)
	deliverWebPushToSubscriptions(ctx, queries, sender, parseUUID(recipientID), subs, payload)
}

func shouldDeliverChannelMessageWebPush(msg handler.ChannelMessageResponse, actorType, actorID, recipientID string, muted bool) bool {
	if msg.Type == "system" || msg.DeletedAt != nil || msg.EditedAt != nil {
		return false
	}
	if msg.Type == "user" && msg.AuthorID != nil && *msg.AuthorID == recipientID {
		return false
	}
	if actorType == "member" && actorID == recipientID {
		return false
	}
	if !muted {
		return true
	}
	return channelMessageMentionsRecipient(msg, recipientID)
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
	payload := buildWebPushInboxPayload(ctx, queries, item, cfg)
	deliverWebPushToSubscriptions(ctx, queries, sender, parseUUID(item.RecipientID), subs, payload)
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

func buildWebPushInboxPayload(ctx context.Context, queries *db.Queries, item handler.InboxItemResponse, cfg handler.Config) webPushInboxPayload {
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
		URL:      webPushAbsoluteURL(cfg, path),
	}
}

func extractChannelMessage(payload any) (handler.ChannelMessageResponse, bool) {
	if msg, ok := payload.(handler.ChannelMessageResponse); ok {
		return msg, true
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return handler.ChannelMessageResponse{}, false
	}
	var msg handler.ChannelMessageResponse
	if err := json.Unmarshal(b, &msg); err != nil {
		return handler.ChannelMessageResponse{}, false
	}
	return msg, msg.ID != "" && msg.ChannelID != "" && msg.WorkspaceID != ""
}

func buildWebPushChannelPayload(ctx context.Context, queries *db.Queries, msg handler.ChannelMessageResponse, info webPushChannelInfo, cfg handler.Config) webPushInboxPayload {
	slug := ""
	if ws, err := queries.GetWorkspace(ctx, parseUUID(msg.WorkspaceID)); err == nil {
		slug = ws.Slug
	}
	path := ""
	if slug != "" {
		path = "/" + slug + "/channels/" + url.PathEscape(msg.ChannelID)
	}
	title := "New group message"
	if info.Kind == "dm" {
		title = msg.AuthorName
		if strings.TrimSpace(title) == "" {
			title = "Direct message"
		}
	} else if strings.TrimSpace(info.Name) != "" {
		title = "#" + info.Name
	}
	return webPushInboxPayload{
		Slug:   slug,
		ItemID: msg.ID,
		Title:  title,
		Body:   notificationBody(msg.AuthorName, msg.Content),
		URL:    webPushAbsoluteURL(cfg, path),
	}
}

func deliverWebPushToSubscriptions(ctx context.Context, queries *db.Queries, sender *webpush.Sender, userID pgtype.UUID, subs []db.WebPushSubscription, payload any) {
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
			UserID:    userID,
			Endpoints: gone,
		})
		if err != nil {
			slog.Error("web push: cleanup failed subscriptions", "error", err)
		}
	}
}

func webPushChannelInfoForRecipient(ctx context.Context, queries *db.Queries, workspaceID, channelID, recipientID string) (webPushChannelInfo, error) {
	row, err := queries.GetWebPushChannelRecipientInfo(ctx, db.GetWebPushChannelRecipientInfoParams{
		WorkspaceID: parseUUID(workspaceID),
		ChannelID:   parseUUID(channelID),
		MemberID:    parseUUID(recipientID),
	})
	if err != nil {
		return webPushChannelInfo{}, err
	}
	return webPushChannelInfo{Name: row.Name, Kind: row.Kind, Muted: row.Muted}, nil
}

func channelHumanMemberIDsForWebPush(ctx context.Context, queries *db.Queries, workspaceID, channelID string) []string {
	ids, err := queries.ListWebPushChannelHumanMemberIDs(ctx, db.ListWebPushChannelHumanMemberIDsParams{
		WorkspaceID: parseUUID(workspaceID),
		ChannelID:   parseUUID(channelID),
	})
	if err != nil {
		slog.Warn("web push: channel member lookup failed", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		return nil
	}
	return ids
}

func channelMessageMentionsRecipient(msg handler.ChannelMessageResponse, recipientID string) bool {
	if util.HasMentionAll(util.ParseMentions(msg.Content)) {
		return true
	}
	for _, mention := range util.ParseMentionsFromContentAndParts(msg.Content, msg.Parts) {
		if mention.Type == "member" && mention.ID == recipientID {
			return true
		}
	}
	return false
}

func notificationBody(author, content string) string {
	normalized := strings.Join(strings.Fields(content), " ")
	if runes := []rune(normalized); len(runes) > 120 {
		normalized = string(runes[:117]) + "..."
	}
	if normalized == "" {
		return strings.TrimSpace(author)
	}
	if strings.TrimSpace(author) == "" {
		return normalized
	}
	return strings.TrimSpace(author) + ": " + normalized
}

func webPushAbsoluteURL(cfg handler.Config, path string) string {
	base := strings.TrimRight(cfg.WebPushAppURL, "/")
	if base == "" {
		base = strings.TrimRight(cfg.PublicURL, "/")
	}
	if base == "" {
		return path
	}
	return base + path
}

func uniqueStringList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func urlQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

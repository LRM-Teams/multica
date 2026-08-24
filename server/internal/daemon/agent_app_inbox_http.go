package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentInboxMessageRow struct {
	Target            string   `json:"target"`
	PendingCount      int      `json:"pendingCount"`
	FirstPendingMsgID string   `json:"firstPendingMsgId,omitempty"`
	LatestMsgID       string   `json:"latestMsgId,omitempty"`
	LatestSeq         int64    `json:"latestSeq,omitempty"`
	LatestSenderName  string   `json:"latestSenderName,omitempty"`
	Flags             []string `json:"flags"`
}

type agentInboxTypedItem struct {
	Source string                `json:"source"`
	Row    *agentInboxMessageRow `json:"row,omitempty"`
	*AgentAppInboxItem
}

type agentInboxSnapshotResponse struct {
	Rows                   []agentInboxMessageRow            `json:"rows"`
	Items                  []agentInboxTypedItem             `json:"items"`
	PendingTargets         int                               `json:"pending_targets"`
	PendingMessages        int                               `json:"pending_messages"`
	PendingAppItems        int                               `json:"pending_app_items"`
	AcknowledgedAppSources []AgentAppInboxAcknowledgedSource `json:"acknowledged_app_sources"`
}

type agentAppSourceAckResponse struct {
	OK                bool                   `json:"ok"`
	ItemID            string                 `json:"itemId"`
	AppID             string                 `json:"appId"`
	NotificationClass string                 `json:"notificationClass"`
	SourceRef         AgentAppInboxSourceRef `json:"sourceRef"`
	SourceEventID     string                 `json:"sourceEventId"`
	AckAttemptID      string                 `json:"ackAttemptId"`
	Replayed          bool                   `json:"replayed"`
}

func (d *WorkspaceDaemonCore) agentAppInboxHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAgentInboxError(w, http.StatusMethodNotAllowed, "method not allowed", "method_not_allowed")
			return
		}
		agentID, workspaceID, runner, ok := d.localAgentInboxIdentity(w, r)
		if !ok {
			return
		}
		pending := runner.agentInboxPendingSnapshot(agentID)
		store, err := d.agentAppInboxes.Store(agentID)
		if err != nil {
			writeAgentInboxError(w, http.StatusConflict, "open app inbox: "+err.Error(), "app_inbox_unavailable")
			return
		}
		rows := projectAgentInboxRows(pending)
		appItems := store.List()
		items := make([]agentInboxTypedItem, 0, len(rows)+len(appItems))
		for i := range rows {
			row := rows[i]
			items = append(items, agentInboxTypedItem{Source: "message_target", Row: &row})
		}
		for i := range appItems {
			item := appItems[i]
			items = append(items, agentInboxTypedItem{Source: "app", AgentAppInboxItem: &item})
		}
		_ = workspaceID
		writeAgentInboxJSON(w, http.StatusOK, agentInboxSnapshotResponse{
			Rows: rows, Items: items, PendingTargets: len(rows), PendingMessages: len(pending),
			PendingAppItems: len(appItems), AcknowledgedAppSources: store.ListAcknowledgedSources(),
		})
	}
}

func (d *WorkspaceDaemonCore) agentAppInboxAckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeAgentInboxError(w, http.StatusMethodNotAllowed, "method not allowed", "method_not_allowed")
			return
		}
		agentID, workspaceID, runner, ok := d.localAgentInboxIdentity(w, r)
		if !ok {
			return
		}
		var request struct {
			ItemID string `json:"itemId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeAgentInboxError(w, http.StatusBadRequest, "invalid JSON body", "invalid_json")
			return
		}
		request.ItemID = strings.TrimSpace(request.ItemID)
		if request.ItemID == "" {
			writeAgentInboxError(w, http.StatusBadRequest, "itemId required", "item_id_required")
			return
		}
		store, err := d.agentAppInboxes.Store(agentID)
		if err != nil {
			writeAgentInboxError(w, http.StatusNotFound, "app inbox not available for this runner", "app_inbox_unavailable")
			return
		}
		item, found := store.Item(request.ItemID)
		if !found {
			writeAgentInboxError(w, http.StatusNotFound, "item not found", "item_not_found")
			return
		}
		intent := store.BeginServerAuthorizedAckIntent(item.ItemID, uuid.NewString())
		if intent == nil {
			writeAgentInboxError(w, http.StatusNotFound, "item not found", "item_not_found")
			return
		}
		status, body, accepted, authoritativeReject := d.authorizeAgentAppSourceAck(r.Context(), workspaceID, agentID, item, *intent)
		if authoritativeReject {
			store.ClearServerAuthorizedAckIntent(item.ItemID, intent.AckAttemptID)
		}
		if !accepted {
			writeAgentInboxJSON(w, status, body)
			return
		}
		if !store.CompleteServerAuthorizedAck(item.ItemID, intent.AckAttemptID) {
			writeAgentInboxError(w, http.StatusConflict, "server accepted source ACK but the local item could not be retired", "local_ack_completion_failed")
			return
		}
		d.canonicalRuntimes.clearAppInboxNoticeMemo(agentID, runner.messageRuntimeID(agentID))
		body["remaining_app_items"] = len(store.List())
		writeAgentInboxJSON(w, status, body)
	}
}

func (d *WorkspaceDaemonCore) authorizeAgentAppSourceAck(ctx context.Context, workspaceID, agentID string, item AgentAppInboxItem, intent AgentAppInboxAckIntent) (int, map[string]any, bool, bool) {
	credential, ok := readCachedAgentCredentialForMessage(d.cfg, workspaceID, agentID, time.Now())
	if !ok {
		return http.StatusConflict, map[string]any{"error": "agent credential is unavailable", "code": "agent_credential_unavailable"}, false, false
	}
	requestBody := map[string]any{
		"itemId": item.ItemID, "appId": item.AppID, "notificationClass": item.NotificationClass,
		"sourceRef": item.SourceRef, "ackAttemptId": intent.AckAttemptID,
	}
	raw, err := json.Marshal(requestBody)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": "encode app source ACK", "code": "source_ack_encode_failed"}, false, false
	}
	requestCtx, cancel := cli.APIContext(ctx)
	defer cancel()
	upstream, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(d.cfg.ServerBaseURL, "/")+"/api/agent/app-sources/ack", bytes.NewReader(raw))
	if err != nil {
		return http.StatusBadGateway, map[string]any{"error": "prepare app source ACK", "code": "source_ack_transport_failed"}, false, false
	}
	upstream.Header.Set("Authorization", "Bearer "+credential.Token)
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Agent-ID", agentID)
	upstream.Header.Set("X-Workspace-ID", workspaceID)
	response, err := (&http.Client{Timeout: cli.APITimeout()}).Do(upstream)
	if err != nil {
		return http.StatusBadGateway, map[string]any{"error": "app source ACK request: " + err.Error(), "code": "source_ack_transport_failed"}, false, false
	}
	defer response.Body.Close()
	responseRaw, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return http.StatusBadGateway, map[string]any{"error": "read app source ACK response", "code": "invalid_app_source_ack_response"}, false, false
	}
	var body map[string]any
	if err := json.Unmarshal(responseRaw, &body); err != nil {
		return http.StatusBadGateway, map[string]any{"error": "invalid app source ACK response", "code": "invalid_app_source_ack_response"}, false, false
	}
	if response.StatusCode >= http.StatusBadRequest {
		return response.StatusCode, body, false, isAuthoritativeAppSourceAckReject(response.StatusCode, body)
	}
	var accepted agentAppSourceAckResponse
	if err := json.Unmarshal(responseRaw, &accepted); err != nil || !exactAgentAppSourceAckResponse(accepted, item, intent) {
		return http.StatusBadGateway, map[string]any{"error": "server app source ACK response did not match the persisted item intent", "code": "invalid_app_source_ack_response"}, false, false
	}
	return response.StatusCode, body, true, false
}

func (d *WorkspaceDaemonCore) retryAgentAppInboxAckIntents(ctx context.Context, workspaceID string) {
	if d == nil || d.agentAppInboxes == nil {
		return
	}
	owners, err := d.agentAppInboxes.OwnerIDs()
	if err != nil {
		if d.logger != nil {
			d.logger.Error("restore App Inbox ACK intents", "workspace_id", workspaceID, "error", err)
		}
		return
	}
	for _, agentID := range owners {
		store, err := d.agentAppInboxes.Store(agentID)
		if err != nil {
			continue
		}
		for _, intent := range store.ListAckIntents() {
			item, ok := store.Item(intent.ItemID)
			if !ok {
				continue
			}
			_, _, accepted, authoritativeReject := d.authorizeAgentAppSourceAck(ctx, workspaceID, agentID, item, intent)
			if authoritativeReject {
				store.ClearServerAuthorizedAckIntent(item.ItemID, intent.AckAttemptID)
				continue
			}
			if accepted {
				if store.CompleteServerAuthorizedAck(item.ItemID, intent.AckAttemptID) {
					if runner := d.currentWorkspaceSession(workspaceID); runner != nil {
						d.canonicalRuntimes.clearAppInboxNoticeMemo(agentID, runner.messageRuntimeID(agentID))
					}
				}
			}
		}
	}
}

func exactAgentAppSourceAckResponse(response agentAppSourceAckResponse, item AgentAppInboxItem, intent AgentAppInboxAckIntent) bool {
	_, sourceEventErr := uuid.Parse(response.SourceEventID)
	return response.OK && sourceEventErr == nil && response.ItemID == item.ItemID &&
		response.AppID == item.AppID && response.NotificationClass == item.NotificationClass &&
		response.SourceRef == item.SourceRef && response.AckAttemptID == intent.AckAttemptID
}

func isAuthoritativeAppSourceAckReject(status int, body map[string]any) bool {
	code, _ := body["code"].(string)
	want := map[string]int{
		"app_source_authority_not_registered": http.StatusNotFound,
		"invalid_source_revision":             http.StatusBadRequest,
		"source_id_ambiguous":                 http.StatusConflict,
		"source_not_found":                    http.StatusNotFound,
		"target_not_fired":                    http.StatusNotFound,
		"stale_source_revision":               http.StatusConflict,
	}
	return want[code] == status
}

func (d *WorkspaceDaemonCore) localAgentInboxIdentity(w http.ResponseWriter, r *http.Request) (string, string, *workspaceSession, bool) {
	agentID := strings.TrimSpace(r.Header.Get("X-Agent-ID"))
	workspaceID := strings.TrimSpace(r.Header.Get("X-Workspace-ID"))
	if agentID == "" || workspaceID == "" {
		writeAgentInboxError(w, http.StatusBadRequest, "agent_id and workspace_id are required", "identity_required")
		return "", "", nil, false
	}
	runner := d.currentWorkspaceSession(workspaceID)
	if runner == nil {
		writeAgentInboxError(w, http.StatusConflict, "WorkspaceDaemon is unavailable", "runner_unavailable")
		return "", "", nil, false
	}
	return agentID, workspaceID, runner, true
}

func projectAgentInboxRows(messages []protocol.AgentMessageProjection) []agentInboxMessageRow {
	byTarget := make(map[string][]protocol.AgentMessageProjection)
	for _, message := range messages {
		if message.Target != "" {
			byTarget[message.Target] = append(byTarget[message.Target], message)
		}
	}
	rows := make([]agentInboxMessageRow, 0, len(byTarget))
	for target, targetMessages := range byTarget {
		sort.Slice(targetMessages, func(i, j int) bool { return targetMessages[i].Seq < targetMessages[j].Seq })
		first, latest := targetMessages[0], targetMessages[len(targetMessages)-1]
		rows = append(rows, agentInboxMessageRow{
			Target: target, PendingCount: len(targetMessages), FirstPendingMsgID: first.ID,
			LatestMsgID: latest.ID, LatestSeq: latest.Seq, LatestSenderName: latest.InitiatorName, Flags: []string{},
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].LatestSeq > rows[j].LatestSeq || rows[i].LatestSeq == rows[j].LatestSeq && rows[i].Target < rows[j].Target
	})
	return rows
}

func writeAgentInboxError(w http.ResponseWriter, status int, message, code string) {
	writeAgentInboxJSON(w, status, map[string]string{"error": message, "code": code})
}

func writeAgentInboxJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

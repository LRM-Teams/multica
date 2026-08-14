package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// credentialProxyMessageSendRequest is deliberately not the Server request
// shape.  In particular, Agent code cannot provide a client identity or a
// seen cursor: both are machine-local Draft state.
//
// Raft-aligned: chat send identity is an independent uuid (or draft reuse for
// the same intent). There is no turn-coordinate batch client_message_id.
type credentialProxyMessageSendRequest struct {
	AgentID       string   `json:"agent_id"`
	WorkspaceID   string   `json:"workspace_id"`
	Target        string   `json:"target"`
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
	SendDraft     bool     `json:"send_draft,omitempty"`
	Anyway        bool     `json:"anyway,omitempty"`
	// Kind is the optional structured agent output kind (LRM-1529).
	Kind string `json:"kind,omitempty"`
	// NoteWrite asks the Server to attach a note_write confirmation part.
	NoteWrite bool `json:"note_write,omitempty"`
	// NotePageID is the optional existing note_page to target. Requires NoteWrite.
	NotePageID string `json:"note_page_id,omitempty"`
}

type credentialProxyMessageTargetResponse struct {
	Target        string `json:"target"`
	ContextTarget string `json:"context_target"`
}

func (d *Daemon) credentialProxyMessageSendHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request credentialProxyMessageSendRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := request.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		credential, ok := readCachedAgentCredentialForMessage(d.cfg, request.WorkspaceID, request.AgentID, time.Now())
		if !ok {
			http.Error(w, "Agent credential is unavailable", http.StatusConflict)
			return
		}

		proxy := d.CredentialProxy()
		runner := d.currentWorkspaceRunner(request.WorkspaceID)
		now := time.Now()
		draft, status, err := d.prepareMessageSendDraft(r.Context(), proxy, credential, request, now)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}

		if !request.Anyway {
			freshness, err := proxy.PreflightMessageSend(request.AgentID, draft.ContextTarget)
			if err != nil {
				if _, holdErr := proxy.RecordMessageDraftHold(request.WorkspaceID, request.AgentID, draft.Target, draft.IdempotencyKey, draft.ContextTarget, draft.SeenUpToSeq, time.Now()); holdErr != nil {
					http.Error(w, "refresh held local Draft: "+holdErr.Error(), http.StatusConflict)
					return
				}
				d.recordAgentMessageResponse(request.WorkspaceID, request.AgentID, draft.IdempotencyKey, draft.ContextTarget, nil, "response_send", "held", "freshness_unknown")
				if runner != nil {
					runner.observeMessageSendHold(request.AgentID, draft.Target, 0, "freshness_unknown")
				}
				writeCredentialProxyMessageJSON(w, localMessageSendHeldResponse(draft.Target, MessageSendFreshness{}, "freshness_unknown"))
				return
			}
			if freshness.Held {
				if _, err := proxy.RecordMessageDraftHold(request.WorkspaceID, request.AgentID, draft.Target, draft.IdempotencyKey, draft.ContextTarget, freshness.LatestSeq, time.Now()); err != nil {
					http.Error(w, "refresh held local Draft: "+err.Error(), http.StatusConflict)
					return
				}
				d.recordAgentMessageResponse(request.WorkspaceID, request.AgentID, draft.IdempotencyKey, draft.ContextTarget, nil, "response_send", "held", "local_pending")
				if runner != nil {
					runner.observeMessageSendHold(request.AgentID, draft.Target, freshness.NewMessageCount, "local_pending")
				}
				writeCredentialProxyMessageJSON(w, localMessageSendHeldResponse(draft.Target, freshness, "newer_messages_available"))
				return
			}
			draft.SeenUpToSeq = freshness.SeenUpToSeq
		}

		endObservation := d.observeOverlappingMessageSend(request.AgentID, draft.Target)
		defer endObservation()
		upstreamRequest := map[string]any{
			"target":            draft.Target,
			"content":           draft.Content,
			"attachment_ids":    draft.AttachmentIDs,
			"client_message_id": draft.IdempotencyKey,
			"seen_up_to_seq":    draft.SeenUpToSeq,
			"context_target":    draft.ContextTarget,
			"bypass_freshness":  request.Anyway,
		}
		if kind := strings.TrimSpace(draft.Kind); kind != "" {
			upstreamRequest["kind"] = kind
		} else if kind := strings.TrimSpace(request.Kind); kind != "" {
			upstreamRequest["kind"] = kind
		}
		if draft.NoteWrite || request.NoteWrite {
			upstreamRequest["note_write"] = true
		}
		if pageID := strings.TrimSpace(draft.NotePageID); pageID != "" {
			upstreamRequest["note_page_id"] = pageID
		} else if pageID := strings.TrimSpace(request.NotePageID); pageID != "" {
			upstreamRequest["note_page_id"] = pageID
		}
		client := d.agentCredentialClient(credential.Token, request)
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		var response map[string]any
		if err := client.PostJSON(ctx, "/api/agent/messages/send", upstreamRequest, &response); err != nil {
			// The outcome is unknown whenever a send request did not return a
			// successful response.  Keep the identity-bearing Draft for an
			// explicit safe replay instead of trying again in the background.
			d.recordAgentMessageResponse(request.WorkspaceID, request.AgentID, draft.IdempotencyKey, draft.ContextTarget, nil, "response_send", "failed", "service_send_failed")
			http.Error(w, "send message through Credential Proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		localCoverageReceipt := ""
		if credentialProxyMessageOutputIsHeld(response) {
			latestSeq, ok := jsonInteger(response["latestSeq"])
			if !ok || latestSeq <= 0 {
				http.Error(w, "invalid held send response from server", http.StatusBadGateway)
				return
			}
			heldMessages, err := parseServerHeldMessageContext(response, draft.ContextTarget)
			if err != nil {
				http.Error(w, "invalid held send response from server", http.StatusBadGateway)
				return
			}
			coverage, err := proxy.PrepareHeldMessageContext(request.AgentID, draft.ContextTarget, latestSeq, heldMessages)
			if err != nil || coverage.ReceiptID == "" {
				http.Error(w, "prepare held message coverage", http.StatusConflict)
				return
			}
			localCoverageReceipt = coverage.ReceiptID
			if _, err := proxy.RecordMessageDraftHold(request.WorkspaceID, request.AgentID, draft.Target, draft.IdempotencyKey, draft.ContextTarget, latestSeq, time.Now()); err != nil {
				http.Error(w, "refresh held local Draft: "+err.Error(), http.StatusConflict)
				return
			}
			count, _ := jsonInteger(response["newMessageCount"])
			d.recordAgentMessageResponse(request.WorkspaceID, request.AgentID, draft.IdempotencyKey, draft.ContextTarget, response, "response_send", "held", "server_race")
			if runner != nil {
				runner.observeMessageSendHold(request.AgentID, draft.Target, count, "server_race")
			}
		}
		if !credentialProxyMessageOutputIsHeld(response) {
			if err := proxy.ClearMessageDraft(request.WorkspaceID, request.AgentID, draft.Target, draft.IdempotencyKey); err != nil {
				// A canonical Message may already exist, so do not claim an unknown
				// send.  The stable server identity makes a later explicit replay
				// safe if the local cleanup needs manual recovery.
				d.recordAgentMessageResponse(request.WorkspaceID, request.AgentID, draft.IdempotencyKey, draft.ContextTarget, response, "response_accepted", "degraded", "draft_cleanup_failed")
				http.Error(w, "clear sent local Draft: "+err.Error(), http.StatusConflict)
				return
			}
			d.recordAgentMessageResponse(request.WorkspaceID, request.AgentID, draft.IdempotencyKey, draft.ContextTarget, response, "response_accepted", "accepted", "")
			if request.SendDraft && runner != nil {
				runner.observeMessageSendDraftSent(request.AgentID, draft.Target, request.Anyway)
			}
		}
		sanitizeCredentialProxyMessageSendResponse(response)
		if localCoverageReceipt != "" {
			response[MessageCoverageReceiptField] = localCoverageReceipt
		}
		writeCredentialProxyMessageJSON(w, response)
	}
}

func parseServerHeldMessageContext(response map[string]any, target string) ([]protocol.AgentMessageProjection, error) {
	raw, found := response["heldMessages"]
	if !found {
		return nil, errors.New("held Messages are required")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var messages []protocol.AgentMessageProjection
	if err := json.Unmarshal(encoded, &messages); err != nil || len(messages) == 0 || len(messages) > messageCheckMaxLimit {
		return nil, errors.New("held Messages are invalid")
	}
	target = strings.TrimSpace(target)
	for index := range messages {
		if strings.TrimSpace(messages[index].Target) == "" {
			messages[index].Target = target
		}
		if messages[index].Target != target || strings.TrimSpace(messages[index].ID) == "" || messages[index].Seq <= 0 {
			return nil, errors.New("held Message identity is invalid")
		}
	}
	return messages, nil
}

func (request *credentialProxyMessageSendRequest) validate() error {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.Target = strings.TrimSpace(request.Target)
	if request.AgentID == "" || request.WorkspaceID == "" || request.Target == "" {
		return errors.New("agent_id, workspace_id, and target are required")
	}
	if request.Anyway && !request.SendDraft {
		return errors.New("--anyway is only valid with --send-draft")
	}
	if request.SendDraft && (strings.TrimSpace(request.Content) != "" || len(request.AttachmentIDs) != 0) {
		return errors.New("--send-draft does not accept replacement message content or attachments")
	}
	if request.SendDraft && (request.NoteWrite || strings.TrimSpace(request.NotePageID) != "") {
		return errors.New("--send-draft does not accept --note-write")
	}
	if strings.TrimSpace(request.NotePageID) != "" && !request.NoteWrite {
		return errors.New("note_page_id requires note_write")
	}
	if !request.SendDraft && strings.TrimSpace(request.Content) == "" {
		return errors.New("message content is required")
	}
	return nil
}

func (d *Daemon) prepareMessageSendDraft(ctx context.Context, proxy *CredentialProxy, credential cachedAgentCredential, request credentialProxyMessageSendRequest, now time.Time) (MessageDraft, int, error) {
	var draft MessageDraft
	var err error
	if request.SendDraft {
		loaded, found, err := proxy.LoadMessageDraft(request.WorkspaceID, request.AgentID, request.Target, now)
		if err != nil {
			return MessageDraft{}, http.StatusConflict, fmt.Errorf("load saved Draft: %w", err)
		}
		if !found {
			return MessageDraft{}, http.StatusNotFound, errors.New("saved Draft not found or expired")
		}
		draft = loaded
	} else {
		contextTarget, status, err := d.resolveMessageSendTarget(ctx, credential.Token, request, request.Target)
		if err != nil {
			return MessageDraft{}, status, err
		}
		seenUpToSeq, err := proxy.MessageSendBoundarySnapshot(request.AgentID, contextTarget)
		if err != nil {
			return MessageDraft{}, http.StatusConflict, err
		}
		// Raft-aligned send identity: mint an independent uuid per intent, or
		// reuse the outstanding Draft's client_message_id when a normal send
		// re-drives the same content for the same target (seq86/87). There is no
		// turn-coordinate batch client_message_id.
		clientMessageID := uuid.NewString()
		if existing, found, loadErr := proxy.LoadMessageDraft(request.WorkspaceID, request.AgentID, request.Target, now); loadErr == nil && found {
			if reused, ok := reuseClientMessageIDForIntent(existing, request.Content); ok {
				clientMessageID = reused
			}
		}
		saved, err := proxy.SaveNormalMessageDraft(request.WorkspaceID, request.AgentID, MessageDraft{
			Target: request.Target, ContextTarget: contextTarget, Content: request.Content, AttachmentIDs: append([]string(nil), request.AttachmentIDs...),
			IdempotencyKey: clientMessageID, SeenUpToSeq: seenUpToSeq,
			Kind:      strings.TrimSpace(request.Kind),
			NoteWrite: request.NoteWrite, NotePageID: strings.TrimSpace(request.NotePageID),
		}, now)
		if err != nil {
			return MessageDraft{}, http.StatusConflict, fmt.Errorf("save local Draft before send: %w", err)
		}
		draft = saved
	}

	// A CLI target is a human-facing slug while coordinator state is keyed by
	// canonical channel/thread identity.  Normal sends resolve that mapping
	// before the one atomic Draft save above, so the saved boundary is current
	// before the first Message send. Older Drafts without the new field are
	// upgraded here for their explicit replay.
	contextTarget := draft.ContextTarget
	if contextTarget == "" {
		var status int
		contextTarget, status, err = d.resolveMessageSendTarget(ctx, credential.Token, request, draft.Target)
		if err != nil {
			return MessageDraft{}, status, err
		}
	}
	seenUpToSeq, err := proxy.MessageSendBoundarySnapshot(request.AgentID, contextTarget)
	if err != nil {
		return MessageDraft{}, http.StatusConflict, err
	}
	draft, err = proxy.UpdateMessageDraftBoundary(request.WorkspaceID, request.AgentID, draft.Target, draft.IdempotencyKey, contextTarget, seenUpToSeq, time.Now())
	if err != nil {
		return MessageDraft{}, http.StatusConflict, fmt.Errorf("persist send freshness preflight: %w", err)
	}
	return draft, http.StatusOK, nil
}

func (d *Daemon) resolveMessageSendTarget(ctx context.Context, token string, request credentialProxyMessageSendRequest, target string) (string, int, error) {
	client := d.agentCredentialClient(token, request)
	apiCtx, cancel := cli.APIContext(ctx)
	defer cancel()
	var targetResponse credentialProxyMessageTargetResponse
	if err := client.PostJSON(apiCtx, "/api/agent/messages/target", map[string]string{"target": target}, &targetResponse); err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("resolve send target through Credential Proxy: %w", err)
	}
	contextTarget := strings.TrimSpace(targetResponse.ContextTarget)
	if contextTarget == "" {
		return "", http.StatusBadGateway, errors.New("invalid target resolution from server")
	}
	return contextTarget, http.StatusOK, nil
}

func (d *Daemon) agentCredentialClient(token string, request credentialProxyMessageSendRequest) *cli.APIClient {
	client := cli.NewAPIClient(d.cfg.ServerBaseURL, request.WorkspaceID, token)
	client.AgentID = request.AgentID
	return client
}

// reuseClientMessageIDForIntent reuses an outstanding (non-expired) local Draft
// client_message_id when a normal send re-drives the SAME intent (identical
// content) to the same target. Distinct content is a new intent.
func reuseClientMessageIDForIntent(existing MessageDraft, content string) (string, bool) {
	if strings.TrimSpace(existing.Content) == strings.TrimSpace(content) {
		return existing.IdempotencyKey, true
	}
	return "", false
}

func localMessageSendHeldResponse(target string, freshness MessageSendFreshness, reason string) map[string]any {
	response := map[string]any{
		"action":              "message_send",
		"target":              target,
		"state":               "held",
		"outcome":             "held",
		"subtype":             "freshness",
		"reason":              reason,
		"decision":            "local_hold",
		"availableActions":    []string{"review_newer_messages"},
		"heldMessages":        freshness.Messages,
		"newMessageCount":     freshness.NewMessageCount,
		"shownMessageCount":   int64(len(freshness.Messages)),
		"omittedMessageCount": freshness.Omitted,
	}
	if freshness.CoverageReceipt != "" {
		response[MessageCoverageReceiptField] = freshness.CoverageReceipt
	}
	return response
}

func sanitizeCredentialProxyMessageSendResponse(response map[string]any) {
	// These fields are intentionally only Proxy↔Server implementation state.
	// A tool gets canonical messages and held context, never a cursor-like
	// boundary, internal transport record, or reusable identity.
	for _, key := range []string{"seenUpToSeq", "latestSeq", "transport_id", "producerFactId", "freshnessResolution", MessageCoverageReceiptField} {
		delete(response, key)
	}
	sanitizeCredentialProxyMessageValue(response)
}

func sanitizeCredentialProxyMessageValue(value any) {
	switch value := value.(type) {
	case map[string]any:
		delete(value, "client_message_id")
		for _, child := range value {
			sanitizeCredentialProxyMessageValue(child)
		}
	case []any:
		for _, child := range value {
			sanitizeCredentialProxyMessageValue(child)
		}
	}
}

func writeCredentialProxyMessageJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

func jsonInteger(value any) (int64, bool) {
	switch value := value.(type) {
	case float64:
		return int64(value), value == float64(int64(value))
	case int64:
		return value, true
	case int:
		return int64(value), true
	default:
		return 0, false
	}
}

func credentialProxyMessageOutputIsHeld(response map[string]any) bool {
	return strings.EqualFold(fmt.Sprint(response["state"]), "held") || strings.EqualFold(fmt.Sprint(response["outcome"]), "held")
}

// observeOverlappingMessageSend deliberately observes but never serializes
// sends.  The signal carries no authored content, credential, or identity.
func (d *Daemon) observeOverlappingMessageSend(agentID, target string) func() {
	key := strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(target)
	d.messageSendMu.Lock()
	if d.messageSends == nil {
		d.messageSends = make(map[string]int)
	}
	previous := d.messageSends[key]
	d.messageSends[key] = previous + 1
	d.messageSendMu.Unlock()
	if previous > 0 && d.logger != nil {
		d.logger.Warn("overlapping Credential Proxy message sends", "agent_id", agentID, "target", target, "inflight", previous+1)
	}
	return func() {
		d.messageSendMu.Lock()
		if d.messageSends[key] <= 1 {
			delete(d.messageSends, key)
		} else {
			d.messageSends[key]--
		}
		d.messageSendMu.Unlock()
	}
}

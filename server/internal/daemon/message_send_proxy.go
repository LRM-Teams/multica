package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// credentialProxyMessageSendRequest is deliberately not the Server request
// shape.  In particular, Agent code cannot provide a client identity or a
// seen cursor: both are machine-local Draft state.
type credentialProxyMessageSendRequest struct {
	AgentID        string   `json:"agent_id"`
	WorkspaceID    string   `json:"workspace_id"`
	Target         string   `json:"target"`
	Content        string   `json:"content"`
	AttachmentIDs  []string `json:"attachment_ids"`
	SendDraft      bool     `json:"send_draft,omitempty"`
	Anyway         bool     `json:"anyway,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
	SeqFrom        int64    `json:"seq_from,omitempty"`
	SeqTo          int64    `json:"seq_to,omitempty"`
	// Kind is the optional structured agent output kind (LRM-1529).
	Kind string `json:"kind,omitempty"`
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
		now := time.Now()
		draft, status, err := d.prepareMessageSendDraft(r.Context(), proxy, credential, request, now)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}

		if !request.Anyway {
			freshness, err := proxy.PreflightMessageSend(request.AgentID, draft.ContextTarget)
			if err != nil {
				d.observeMessageSendHold(request.AgentID, request.WorkspaceID, draft.Target, 0, "freshness_unknown")
				writeCredentialProxyMessageJSON(w, localMessageSendHeldResponse(draft.Target, MessageSendFreshness{}, "freshness_unknown"))
				return
			}
			if freshness.Held {
				if _, err := proxy.RefreshMessageDraft(request.AgentID, draft.Target, draft.ClientMessageID, draft.ContextTarget, freshness.LatestSeq, time.Now()); err != nil {
					http.Error(w, "refresh held local Draft: "+err.Error(), http.StatusConflict)
					return
				}
				d.observeMessageSendHold(request.AgentID, request.WorkspaceID, draft.Target, freshness.NewMessageCount, "local_pending")
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
			"client_message_id": draft.ClientMessageID,
			"seen_up_to_seq":    draft.SeenUpToSeq,
			"context_target":    draft.ContextTarget,
			"bypass_freshness":  request.Anyway,
		}
		if kind := strings.TrimSpace(draft.Kind); kind != "" {
			upstreamRequest["kind"] = kind
		} else if kind := strings.TrimSpace(request.Kind); kind != "" {
			upstreamRequest["kind"] = kind
		}
		client := d.agentCredentialClient(credential.Token, request)
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		var response map[string]any
		if err := client.PostJSON(ctx, "/api/agent/messages/send", upstreamRequest, &response); err != nil {
			// The outcome is unknown whenever a send request did not return a
			// successful response.  Keep the identity-bearing Draft for an
			// explicit safe replay instead of trying again in the background.
			http.Error(w, "send message through Credential Proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		if credentialProxyMessageOutputIsHeld(response) {
			latestSeq, ok := jsonInteger(response["latestSeq"])
			if !ok || latestSeq <= 0 {
				http.Error(w, "invalid held send response from server", http.StatusBadGateway)
				return
			}
			if err := proxy.AcceptHeldMessageContext(request.AgentID, draft.ContextTarget, latestSeq); err != nil {
				http.Error(w, "persist held message Context Boundary: "+err.Error(), http.StatusConflict)
				return
			}
			if _, err := proxy.RefreshMessageDraft(request.AgentID, draft.Target, draft.ClientMessageID, draft.ContextTarget, latestSeq, time.Now()); err != nil {
				http.Error(w, "refresh held local Draft: "+err.Error(), http.StatusConflict)
				return
			}
			count, _ := jsonInteger(response["newMessageCount"])
			d.observeMessageSendHold(request.AgentID, request.WorkspaceID, draft.Target, count, "server_race")
		}
		if !credentialProxyMessageOutputIsHeld(response) {
			if err := proxy.ClearMessageDraft(request.AgentID, draft.Target, draft.ClientMessageID); err != nil {
				// A canonical Message may already exist, so do not claim an unknown
				// send.  The stable server identity makes a later explicit replay
				// safe if the local cleanup needs manual recovery.
				http.Error(w, "clear sent local Draft: "+err.Error(), http.StatusConflict)
				return
			}
		}
		sanitizeCredentialProxyMessageSendResponse(response)
		writeCredentialProxyMessageJSON(w, response)
	}
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
	if !request.SendDraft && strings.TrimSpace(request.Content) == "" {
		return errors.New("message content is required")
	}
	return nil
}

func (d *Daemon) prepareMessageSendDraft(ctx context.Context, proxy *CredentialProxy, credential cachedAgentCredential, request credentialProxyMessageSendRequest, now time.Time) (messageDraft, int, error) {
	var draft messageDraft
	var err error
	if request.SendDraft {
		loaded, found, err := proxy.LoadMessageDraft(request.AgentID, request.Target, now)
		if err != nil {
			return messageDraft{}, http.StatusConflict, fmt.Errorf("load saved Draft: %w", err)
		}
		if !found {
			return messageDraft{}, http.StatusNotFound, errors.New("saved Draft not found or expired")
		}
		draft = loaded
	} else {
		contextTarget, status, err := d.resolveMessageSendTarget(ctx, credential.Token, request, request.Target)
		if err != nil {
			return messageDraft{}, status, err
		}
		seenUpToSeq, err := proxy.MessageSendBoundarySnapshot(request.AgentID, contextTarget)
		if err != nil {
			return messageDraft{}, http.StatusConflict, err
		}
		// A held normal send leaves an outstanding Draft for the target. When a
		// re-driven normal send carries the same content (same intent), reuse its
		// stable client_message_id instead of minting a fresh identity: the server
		// (workspace,channel,author,client_message_id) dedup then recognizes the
		// retry and stops the duplicate (regression seq86/87). Distinct content is
		// a new intent, so it forwards to a fresh identity as before.
		//
		// Turn-at-most-once: when the agent is processing one exchange batch (a
		// single conversation spanning seq_from..seq_to), every send/retry of that
		// batch shares ONE stable client_message_id derived from the batch
		// identity, so the server dedup collapses an accidental re-delivery (the
		// "same message sent twice" regression). Different batches derive
		// different ids, so genuinely distinct messages are never folded together.
		//
		// Fill missing turn identity from the daemon's in-flight inbox lease when
		// the CLI omitted MULTICA_TURN_* (mixed batch/uuid was the v0.4.24 gap).
		// Only when an inbox turn is active — draft/--send-draft/proactive non-turn
		// sends keep the legacy UUID path (Alice fail-closed scoping).
		_ = d.fillTurnIdentityFromActiveInboxTurn(&request)
		clientMessageID := ""
		if request.ConversationID != "" && request.SeqFrom > 0 && request.SeqTo >= request.SeqFrom {
			clientMessageID = batchClientMessageID(request.ConversationID, request.SeqFrom, request.SeqTo, request.Content, request.AttachmentIDs)
		} else {
			clientMessageID = uuid.NewString()
			if existing, found, loadErr := proxy.LoadMessageDraft(request.AgentID, request.Target, now); loadErr == nil && found {
				if reused, ok := reuseClientMessageIDForIntent(existing, request.Content); ok {
					clientMessageID = reused
				}
			}
		}
		saved, err := proxy.SaveNormalMessageDraft(request.AgentID, messageDraft{
			Target: request.Target, ContextTarget: contextTarget, Content: request.Content, AttachmentIDs: append([]string(nil), request.AttachmentIDs...),
			ClientMessageID: clientMessageID, SeenUpToSeq: seenUpToSeq,
			Kind: strings.TrimSpace(request.Kind),
		}, now)
		if err != nil {
			return messageDraft{}, http.StatusConflict, fmt.Errorf("save local Draft before send: %w", err)
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
			return messageDraft{}, status, err
		}
	}
	seenUpToSeq, err := proxy.MessageSendBoundarySnapshot(request.AgentID, contextTarget)
	if err != nil {
		return messageDraft{}, http.StatusConflict, err
	}
	draft, err = proxy.RefreshMessageDraft(request.AgentID, draft.Target, draft.ClientMessageID, contextTarget, seenUpToSeq, time.Now())
	if err != nil {
		return messageDraft{}, http.StatusConflict, fmt.Errorf("persist send freshness preflight: %w", err)
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

// outstanding (non-expired, not-yet-cleared) local Draft when a normal send
// is a re-drive of the SAME intent (identical content) to the same target.
// Reusing the same identity lets the server's (workspace,channel,author,
// client_message_id) dedup recognize a retry instead of minting a fresh UUID
// that bypasses dedup and duplicates the message (regression seq86/87).
// Distinct content is a new intent, so it is not reused.
func reuseClientMessageIDForIntent(existing messageDraft, content string) (string, bool) {
	if strings.TrimSpace(existing.Content) == strings.TrimSpace(content) {
		return existing.ClientMessageID, true
	}
	return "", false
}

// batchClientMessageID derives a stable, deterministic client_message_id for a
// single concrete message produced within one exchange batch. The batch is
// identified by (ConversationID, SeqFrom, SeqTo); a message inside it is
// distinguished by its payload (content + sorted attachment ids).
//
//   - Same batch + same content + same attachments (a retry/re-delivery of the
//     SAME message) yields the SAME id, so the server's
//     (workspace,channel,author,client_message_id) dedup collapses an accidental
//     re-delivery (the "same message sent twice" regression).
//   - Different content (or attachments) within the SAME batch yields a
//     DIFFERENT id, so distinct messages a turn sends are NOT folded together;
//     if they shared one id the server would 409-reject (and drop) the 2nd+
//     distinct message as a client_message_id conflict (LRM-1530).
//   - Different batches (different conversation or seq range) yield a different id.
//
// The 32-char form stays well under the server's client_message_id length limit.
func batchClientMessageID(conversationID string, seqFrom, seqTo int64, content string, attachmentIDs []string) string {
	h := sha256.New()
	h.Write([]byte(conversationID))
	h.Write([]byte{0})
	h.Write(strconv.AppendInt(nil, seqFrom, 10))
	h.Write([]byte{0})
	h.Write(strconv.AppendInt(nil, seqTo, 10))
	h.Write([]byte{0})
	h.Write([]byte(content))
	for _, id := range sortedCopy(attachmentIDs) {
		h.Write([]byte{0})
		h.Write([]byte(id))
	}
	return "b" + hex.EncodeToString(h.Sum(nil))[:31]
}

// sortedCopy returns a sorted copy of ids so the derived id is independent of
// attachment ordering (same attachment set always yields the same identity).
func sortedCopy(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func localMessageSendHeldResponse(target string, freshness MessageSendFreshness, reason string) map[string]any {
	return map[string]any{
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
}

func sanitizeCredentialProxyMessageSendResponse(response map[string]any) {
	// These fields are intentionally only Proxy↔Server implementation state.
	// A tool gets canonical messages and held context, never a cursor-like
	// boundary, internal transport record, or reusable identity.
	for _, key := range []string{"seenUpToSeq", "latestSeq", "transport_id", "producerFactId", "freshnessResolution"} {
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

func (d *Daemon) observeMessageSendHold(agentID, workspaceID, target string, newer int64, reason string) {
	if d.logger != nil {
		d.logger.Info("Credential Proxy message send held", "agent_id", agentID, "workspace_id", workspaceID, "target", target, "new_message_count", newer, "reason", reason)
	}
	// Project a Runner Activity entry so a soft-held send is visible on the
	// agent's Activity timeline (a "system" entry renders as a warning row with
	// title/subtext and body_kind:none). This is intentionally fail-soft and
	// never influences the send outcome. Unlike the managed-only publisher, the
	// hold entry is projected for Agents that are NOT locally managed either
	// (Raft: the daemon still reports the Activity fact with blank launch/client-
	// seq bookkeeping); a missing transport just drops it best-effort.
	entry, err := activitySystemEntry(messageSendHoldTitle(), messageSendHoldSubtext(newer))
	if err != nil {
		return
	}
	producer := d.workspaceAgentActivityProducer(workspaceID)
	if err := producer.PublishHoldEntry(agentID, d.runnerInstanceID, []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
		d.logger.Debug("send-hold Runner Activity publish deferred", "error", err, "agent_id", agentID, "target", target)
	}
}

// messageSendHoldTitle is the warning-row title surfaced on the Agent Activity
// timeline when a message send is held pending review of newer messages. It is
// the presentation contract consumed by the Activity tab (see Dax FE test).
func messageSendHoldTitle() string {
	return "Message held — review newer messages before sending"
}

// messageSendHoldSubtext builds the warning-row subtext from how many newer
// messages are pending. It is deliberately text-only; the Activity projection
// renders it as body_kind:none.
func messageSendHoldSubtext(newer int64) string {
	if newer > 0 {
		return fmt.Sprintf("%d newer messages available — review then resend", newer)
	}
	return "Send held — review the channel before resending"
}

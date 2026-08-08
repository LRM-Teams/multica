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
type credentialProxyMessageSendRequest struct {
	AgentID       string   `json:"agent_id"`
	WorkspaceID   string   `json:"workspace_id"`
	Target        string   `json:"target"`
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
	SendDraft     bool     `json:"send_draft,omitempty"`
	Anyway        bool     `json:"anyway,omitempty"`
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
		saved, err := proxy.SaveNormalMessageDraft(request.AgentID, messageDraft{
			Target: request.Target, ContextTarget: contextTarget, Content: request.Content, AttachmentIDs: append([]string(nil), request.AttachmentIDs...),
			ClientMessageID: uuid.NewString(), SeenUpToSeq: seenUpToSeq,
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
	// never influences the send outcome: activity publication is best-effort and
	// best-effort deferred by the producer when the agent is not currently
	// managed on this Runner.
	entry, err := activitySystemEntry(messageSendHoldTitle(), messageSendHoldSubtext(newer))
	if err != nil {
		return
	}
	producer := d.workspaceAgentActivityProducer(workspaceID)
	if err := producer.PublishEntryForManagedAgent(agentID, d.runnerInstanceID, []protocol.AgentActivityEntry{entry}); err != nil && d.logger != nil {
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

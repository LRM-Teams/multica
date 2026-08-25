package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// scheduleCanonicalMessageDelivery attaches live Agent projection to the same
// committed channel:message boundary used by every canonical author type. This
// keeps the live side of the recovery fence aligned with the Server recovery
// query instead of covering only browser-authored human Messages.
func (h *Handler) scheduleCanonicalMessageDelivery(ctx context.Context, eventType string, payload any) {
	if h == nil || eventType != protocol.EventChannelMessage {
		return
	}
	message, ok := payload.(ChannelMessageResponse)
	if !ok || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return
	}
	if channelMessageHasPendingVoiceTranscription(message) {
		return
	}
	h.runAfterChannelMessageAck(ctx, func(ctx context.Context) {
		h.attachGraphMemoryCitationsToPublishedMessage(ctx, message)
		channel, found := h.getChannel(ctx, message.WorkspaceID, parseUUID(message.ChannelID))
		if !found {
			slog.Warn("load canonical Message channel for Agent delivery failed", "workspace_id", message.WorkspaceID, "channel_id", message.ChannelID, "message_id", message.ID)
			return
		}
		h.deliverCanonicalMessageToChannelAgents(ctx, channel, message)
	})
}

func channelMessageHasPendingVoiceTranscription(message ChannelMessageResponse) bool {
	for _, part := range message.Parts {
		if part.Type == protocol.MessagePartTypeVoice && part.TranscriptionStatus == protocol.VoiceTranscriptionPending {
			return true
		}
	}
	return false
}

type canonicalMessageDeliveryPlan struct {
	SourceRecipient db.Agent
	Recipient       db.Agent
	RunID           string
	RunAgentID      string
	Mixed           bool
	Delivery        protocol.AgentDeliverPayload
	Created         bool
}

// planCanonicalMessageDeliveryRecipients applies channel policy to source
// members first, then resolves any per-run execution identity. Derived agents
// can therefore never add themselves to the eligible source recipient set.
func (h *Handler) planCanonicalMessageDeliveryRecipients(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) ([]*canonicalMessageDeliveryPlan, error) {
	plans := make([]*canonicalMessageDeliveryPlan, 0)
	for _, sourceRecipient := range h.canonicalMessageDeliveryRecipients(ctx, ch, message) {
		recipient := sourceRecipient
		runID, runAgentID, executionAgent, mixed, err := h.resolveCanonicalRunRecipient(ctx, ch, sourceRecipient)
		if err != nil {
			return nil, fmt.Errorf("resolve canonical mixed run recipient: %w", err)
		}
		if mixed {
			recipient = executionAgent
		}
		plans = append(plans, &canonicalMessageDeliveryPlan{
			SourceRecipient: sourceRecipient,
			Recipient:       recipient,
			RunID:           runID,
			RunAgentID:      runAgentID,
			Mixed:           mixed,
		})
	}
	return plans, nil
}

// persistCanonicalMessageDeliveryPlans commits every selected delivery and
// mixed-run obligation in one transaction. Compatibility callers use this
// wrapper; canonical sends call persistCanonicalMessageDeliveryPlansTx from
// the transaction that also inserts (or repairs) the message.
func (h *Handler) persistCanonicalMessageDeliveryPlans(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse, plans []*canonicalMessageDeliveryPlan) error {
	if len(plans) == 0 {
		return nil
	}
	if h.TxStarter == nil {
		for _, plan := range plans {
			if plan.Mixed {
				return errors.New("mixed run delivery transaction unavailable")
			}
			delivery, created, err := persistCanonicalMessageDelivery(ctx, h.DB, ch, message, plan.Recipient)
			if err != nil {
				return fmt.Errorf("persist canonical Agent Message delivery: %w", err)
			}
			plan.Delivery, plan.Created = delivery, created
		}
		return nil
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin canonical delivery transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := persistCanonicalMessageDeliveryPlansTx(ctx, tx, ch, message, plans); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit canonical delivery transaction: %w", err)
	}
	return nil
}

func persistCanonicalMessageDeliveryPlansTx(ctx context.Context, tx pgx.Tx, ch ChannelResponse, message ChannelMessageResponse, plans []*canonicalMessageDeliveryPlan) error {
	activity := service.NewEnvDispatchActivityFromQueries(db.New(tx))
	for _, plan := range plans {
		delivery, deliveryCreated, err := persistCanonicalMessageDelivery(ctx, tx, ch, message, plan.Recipient)
		if err != nil {
			return fmt.Errorf("persist canonical Agent Message delivery: %w", err)
		}
		obligationCreated := false
		if plan.Mixed {
			obligationCreated, err = createMixedRunDeliveryObligationWithActivity(ctx, activity, plan.RunID, plan.RunAgentID, message.ID, plan.SourceRecipient.ID)
			if err != nil {
				return fmt.Errorf("persist canonical mixed run delivery obligation: %w", err)
			}
			delivery.RunID = plan.RunID
			delivery.RunAgentID = plan.RunAgentID
		}
		plan.Delivery = delivery
		if delivery.Message.Directed {
			if err := persistGraphMemoryAgentSteeringEvent(ctx, tx, delivery.Message); err != nil {
				return fmt.Errorf("persist graph memory agent steering event: %w", err)
			}
		}
		// A repaired delivery or obligation needs a live notification after the
		// acceptance boundary; an ordinary complete replay creates neither.
		plan.Created = deliveryCreated || obligationCreated
	}
	return nil
}

func (h *Handler) notifyCanonicalMessageDeliveryPlans(ctx context.Context, ch ChannelResponse, plans []*canonicalMessageDeliveryPlan) {
	for _, plan := range plans {
		if plan.Created {
			h.attachCanonicalMessageMemories(ctx, ch.WorkspaceID, plan.Recipient.ID, &plan.Delivery.Message)
			h.notifyCanonicalMessageDelivery(ctx, ch, plan.Recipient, plan.Delivery)
		}
	}
}

// persistGraphMemoryAgentSteeringEvent appends an accepted directed message to
// the currently fenced trajectory. Delivery replay is idempotent by message_id;
// channels without an active run retain the ordinary Message queue only.
func persistGraphMemoryAgentSteeringEvent(ctx context.Context, tx pgx.Tx, message protocol.AgentMessageProjection) error {
	if tx == nil || !message.Directed || strings.TrimSpace(message.ID) == "" || strings.TrimSpace(message.ChannelID) == "" {
		return nil
	}
	var runID, trajectoryID string
	if err := tx.QueryRow(ctx, `
		SELECT run.id::text,trajectory.id::text
		FROM graph_memory_agent_state state
		JOIN graph_memory_agent_run run ON run.id=state.active_run_id AND run.status='running'
		JOIN graph_memory_agent_trajectory trajectory ON trajectory.run_id=run.id AND trajectory.status='active'
		WHERE state.channel_id=$1::uuid
		FOR UPDATE OF run`, message.ChannelID).Scan(&runID, &trajectoryID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	actor, err := json.Marshal(map[string]string{
		"type": message.InitiatorType,
		"id":   message.InitiatorID,
		"name": message.InitiatorName,
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"id":      message.ID,
		"seq":     message.Seq,
		"content": message.Content,
	})
	if err != nil {
		return err
	}
	target, err := json.Marshal(map[string]string{
		"channel_id": message.ChannelID,
		"target":     message.Target,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO graph_memory_agent_steering_event
		  (run_id,trajectory_id,message_id,ordinal,actor,message,target)
		SELECT $1::uuid,$2::uuid,$3::uuid,
		       COALESCE((SELECT max(ordinal)+1 FROM graph_memory_agent_steering_event WHERE run_id=$1::uuid),1),
		       $4::jsonb,$5::jsonb,$6::jsonb
		ON CONFLICT (run_id,message_id) DO NOTHING`, runID, trajectoryID, message.ID, actor, body, target)
	return err
}

func (h *Handler) observeGraphMemoryChannelActivity(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) {
	if h == nil || h.GraphMemoryAgentControl == nil || h.DB == nil || !strings.EqualFold(ch.Kind, "group") {
		return
	}
	if !channelMessageIsHumanAuthored(message.Type) && !strings.EqualFold(message.Type, "agent") {
		return
	}
	if strings.EqualFold(message.Type, "agent") && message.AuthorID != nil {
		var memoryAgentID pgtype.UUID
		if err := h.DB.QueryRow(ctx, `SELECT agent_id FROM graph_memory_channel_agent WHERE channel_id = $1`, parseUUID(ch.ID)).Scan(&memoryAgentID); err == nil && *message.AuthorID == uuidToString(memoryAgentID) {
			return
		}
	}
	observedAt := time.Now().UTC()
	if parsed, err := time.Parse(time.RFC3339Nano, message.CreatedAt); err == nil {
		observedAt = parsed
	}
	if err := h.GraphMemoryAgentControl.ObserveActivity(ctx, ch.WorkspaceID, ch.ID, observedAt); err != nil {
		slog.Warn("graph memory channel activity observation failed", "workspace_id", ch.WorkspaceID, "channel_id", ch.ID, "message_id", message.ID, "error", err)
	}
}

func (h *Handler) publishBlockedGraphMemoryDirectedFailure(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) {
	if h == nil || h.DB == nil || !channelMessageIsHumanAuthored(message.Type) || !strings.EqualFold(ch.Kind, "group") {
		return
	}
	var agentID pgtype.UUID
	var reason string
	if err := h.DB.QueryRow(ctx, `
		SELECT agent_id, blocked_reason
		FROM graph_memory_channel_agent
		WHERE channel_id = $1 AND workspace_id = $2 AND status = 'blocked' AND agent_id IS NOT NULL`,
		parseUUID(ch.ID), parseUUID(ch.WorkspaceID),
	).Scan(&agentID, &reason); err != nil || !managedGraphMemoryAgentMentionParts(agentID, message.Parts) {
		return
	}
	content := "Graph Memory Agent cannot accept this request while blocked."
	if strings.TrimSpace(reason) != "" {
		content += " Reason: " + strings.TrimSpace(reason)
	}
	failure, err := h.insertChannelMessage(ctx, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), "system", pgtype.UUID{}, "System", content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		slog.Warn("graph memory blocked directed failure insert failed", "channel_id", ch.ID, "message_id", message.ID, "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, ch.WorkspaceID, "system", "", parseUUID(ch.ID), failure)
}

func managedGraphMemoryAgentMentionParts(agentID pgtype.UUID, parts []protocol.MessagePart) bool {
	if !agentID.Valid {
		return false
	}
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefSubType == "agent" && part.RefID == uuidToString(agentID) {
			return true
		}
	}
	return false
}

// deliverCanonicalMessageToChannelAgents is the compatibility boundary for
// non-frontend canonical publishers. Frontend and env-dispatch sends call the
// same planning/persistence functions through service.SendCanonicalChannelMessage.
func (h *Handler) deliverCanonicalMessageToChannelAgents(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) error {
	if h == nil || h.DB == nil || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return nil
	}
	// LRM-1523: agent-authored pure confirmations must not enter any Agent's
	// MessageCoordinator pending set (same no-wake contract as the retired
	// task-shaped path).
	if !channelMessageIsHumanAuthored(message.Type) && channelMessageIsConfirmationNoWake(message) {
		return nil
	}
	h.observeGraphMemoryChannelActivity(ctx, ch, message)
	h.publishBlockedGraphMemoryDirectedFailure(ctx, ch, message)
	plans, err := h.planCanonicalMessageDeliveryRecipients(ctx, ch, message)
	if err != nil {
		return err
	}
	if err := h.persistCanonicalMessageDeliveryPlans(ctx, ch, message, plans); err != nil {
		return err
	}
	h.notifyCanonicalMessageDeliveryPlans(ctx, ch, plans)
	return nil
}

// persistCanonicalMessageDelivery records one explicitly selected recipient.
// Normal channel traffic supplies recipients through canonicalMessageDeliveryRecipients;
// system primitives such as Reminder use this same durable mapping with their own
// product-specific recipient rule.
func persistCanonicalMessageDelivery(ctx context.Context, exec dbExecutor, ch ChannelResponse, message ChannelMessageResponse, recipient db.Agent) (protocol.AgentDeliverPayload, bool, error) {
	if exec == nil || !recipient.RuntimeID.Valid || strings.TrimSpace(ch.WorkspaceID) == "" || strings.TrimSpace(message.ID) == "" || message.Seq <= 0 {
		return protocol.AgentDeliverPayload{}, false, nil
	}
	agentID := uuidToString(recipient.ID)
	if agentID == "" {
		return protocol.AgentDeliverPayload{}, false, nil
	}
	target := canonicalMessageDeliveryTarget(ch, message)
	replyTarget, err := canonicalMessageReplyTarget(ctx, exec, ch, message, recipient.ID)
	if err != nil {
		return protocol.AgentDeliverPayload{}, false, err
	}
	tag, err := exec.Exec(ctx, `
		INSERT INTO agent_message_delivery (workspace_id, agent_id, message_id, target, seq)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (agent_id, message_id) DO NOTHING`,
		parseUUID(ch.WorkspaceID), recipient.ID, parseUUID(message.ID), target, message.Seq)
	if err != nil {
		return protocol.AgentDeliverPayload{}, false, err
	}
	created := tag.RowsAffected() == 1
	directed := managedGraphMemoryAgentMention(ctx, exec, parseUUID(ch.ID), recipient.ID, message.Parts)
	managedGraphTools := managedGraphMemoryAgent(ctx, exec, parseUUID(ch.ID), recipient.ID)
	return protocol.AgentDeliverPayload{
		AgentID:    agentID,
		Target:     target,
		Seq:        message.Seq,
		DeliveryID: "message:" + message.ID + ":agent:" + agentID,
		Message: protocol.AgentMessageProjection{
			ID: message.ID, ChannelID: ch.ID, Target: target, ReplyTarget: replyTarget, Seq: message.Seq, Content: message.Content, Parts: message.Parts, Directed: directed, GraphMemoryTools: managedGraphTools,
			ChannelKind: ch.Kind, ProjectID: stringValue(ch.ProjectID),
			InitiatorType: canonicalMessageInitiatorType(message.Type), InitiatorID: stringValue(message.AuthorID), InitiatorName: message.AuthorName,
		},
	}, created, nil
}

func (h *Handler) attachCanonicalMessageMemories(ctx context.Context, workspaceID string, agentID pgtype.UUID, message *protocol.AgentMessageProjection) {
	if h == nil || h.TaskService == nil || message == nil {
		return
	}
	chatSessionID := ""
	if strings.EqualFold(message.ChannelKind, "group") {
		chatSessionID = message.Target
	}
	memories := h.TaskService.LoadAgentMemoriesForExecution(ctx, agentID, parseUUID(workspaceID), service.MemoryExecutionScope{
		InitiatorType: message.InitiatorType,
		InitiatorID:   message.InitiatorID,
		ProjectID:     message.ProjectID,
		ChannelID:     message.ChannelID,
		ChannelKind:   message.ChannelKind,
		ChatSessionID: chatSessionID,
		MessageTexts:  []string{message.Content},
		TaskType:      "channel_message",
	})
	message.Memories = make([]protocol.AgentMessageMemoryProjection, 0, len(memories))
	for _, memory := range memories {
		message.Memories = append(message.Memories, protocol.AgentMessageMemoryProjection{
			Name: memory.Name, Content: memory.Content, Scope: memory.Scope,
			SubjectType: memory.SubjectType, SubjectID: memory.SubjectID,
		})
	}
}

func managedGraphMemoryAgent(ctx context.Context, exec dbExecutor, channelID, agentID pgtype.UUID) bool {
	if exec == nil || !channelID.Valid || !agentID.Valid {
		return false
	}
	var managed bool
	if err := exec.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM graph_memory_channel_agent
			WHERE channel_id = $1 AND agent_id = $2 AND status = 'active'
		)`, channelID, agentID).Scan(&managed); err != nil {
		return false
	}
	return managed
}

func managedGraphMemoryAgentMention(ctx context.Context, exec dbExecutor, channelID, agentID pgtype.UUID, parts []protocol.MessagePart) bool {
	return managedGraphMemoryAgentMentionParts(agentID, parts) && managedGraphMemoryAgent(ctx, exec, channelID, agentID)
}

// redeliverUnacknowledgedComputerAgentMessages rebuilds the Computer's
// at-least-once transport queue whenever its WorkspaceDaemon becomes ready.
// The persisted delivery sequence remains the canonical target sequence; this
// path neither invents a recovery cursor nor changes local context coverage.
func (h *Handler) redeliverUnacknowledgedComputerAgentMessages(ctx context.Context, identity daemonws.ClientIdentity) error {
	if h == nil || h.DB == nil || h.AgentDeliveryNotifier == nil {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT delivery.agent_id, m.id, m.channel_id, delivery.seq, m.content, m.parts,
		       delivery.target, c.kind, c.project_id, m.author_type, m.author_id,
		       COALESCE(NULLIF(author_user.name, ''), NULLIF(author_agent.name, ''), ''),
		       CASE c.kind
		         WHEN 'group' THEN '#' || c.name
		         WHEN 'dm' THEN 'dm:@' || COALESCE(peer.handle, '')
		         ELSE ''
		       END || CASE
		         WHEN m.thread_root_message_id IS NOT NULL THEN ':' || LEFT(m.thread_root_message_id::text, 8)
		         ELSE ''
		       END
		FROM agent_message_delivery delivery
		JOIN agent recipient ON recipient.id = delivery.agent_id AND recipient.archived_at IS NULL
		JOIN agent_runtime runtime ON runtime.id = recipient.runtime_id
		JOIN channel_message m ON m.id = delivery.message_id AND m.deleted_at IS NULL
		JOIN channel c ON c.id = m.channel_id AND c.workspace_id = delivery.workspace_id
		LEFT JOIN "user" author_user ON m.author_type = 'user' AND author_user.id = m.author_id
		LEFT JOIN agent author_agent ON m.author_type = 'agent' AND author_agent.id = m.author_id
		LEFT JOIN LATERAL (
			SELECT COALESCE(NULLIF(u.name, ''), NULLIF(a.name, ''), '') AS handle
			FROM channel_member cm
			LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
			LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id AND a.archived_at IS NULL
			WHERE cm.channel_id = c.id AND cm.workspace_id = delivery.workspace_id
			  AND NOT (cm.member_type = 'agent' AND cm.member_id = delivery.agent_id)
			ORDER BY cm.created_at ASC
			LIMIT 1
		) peer ON c.kind = 'dm'
		WHERE delivery.workspace_id = $1 AND runtime.daemon_id = $2
		  AND delivery.ack_required AND delivery.acked_at IS NULL
		ORDER BY delivery.seq, delivery.message_id, delivery.agent_id`,
		parseUUID(identity.WorkspaceID), identity.DaemonID)
	if err != nil {
		return fmt.Errorf("load unacknowledged Computer Agent Messages: %w", err)
	}
	// Drain the list cursor before nested pool work (memories / graph profile)
	// so cursordeadlock cannot hold two connections (cursordeadlock / #1803).
	type pendingComputerAgentMessage struct {
		agentID pgtype.UUID
		message protocol.AgentMessageProjection
		target  string
		seq     int64
	}
	pending := make([]pendingComputerAgentMessage, 0)
	for rows.Next() {
		var agentID, messageID, channelID, projectID, authorID pgtype.UUID
		var seq int64
		var content, target, channelKind, authorType, authorName, replyTarget string
		var rawParts []byte
		if err := rows.Scan(&agentID, &messageID, &channelID, &seq, &content, &rawParts, &target, &channelKind, &projectID, &authorType, &authorID, &authorName, &replyTarget); err != nil {
			rows.Close()
			return fmt.Errorf("scan unacknowledged Computer Agent Message: %w", err)
		}
		var parts []protocol.MessagePart
		if len(rawParts) > 0 && string(rawParts) != "null" {
			if err := json.Unmarshal(rawParts, &parts); err != nil {
				rows.Close()
				return fmt.Errorf("decode unacknowledged Computer Agent Message parts: %w", err)
			}
		}
		pending = append(pending, pendingComputerAgentMessage{
			agentID: agentID,
			target:  target,
			seq:     seq,
			message: protocol.AgentMessageProjection{
				ID: uuidToString(messageID), ChannelID: uuidToString(channelID), Target: target,
				ReplyTarget: replyTarget, Seq: seq, Content: content, Parts: parts,
				ChannelKind: channelKind, ProjectID: uuidToString(projectID),
				InitiatorType: canonicalMessageInitiatorType(authorType),
				InitiatorID:   uuidToString(authorID), InitiatorName: authorName,
			},
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate unacknowledged Computer Agent Messages: %w", err)
	}
	rows.Close()

	for i := range pending {
		item := &pending[i]
		item.message.GraphMemoryTools = managedGraphMemoryAgent(ctx, h.DB, parseUUID(item.message.ChannelID), item.agentID)
		item.message.Directed = item.message.GraphMemoryTools && managedGraphMemoryAgentMentionParts(item.agentID, item.message.Parts)
		h.attachCanonicalMessageMemories(ctx, identity.WorkspaceID, item.agentID, &item.message)
		agentIDText := uuidToString(item.agentID)
		delivery := protocol.AgentDeliverPayload{
			AgentID: agentIDText, Target: item.target, Seq: item.seq,
			DeliveryID: "message:" + item.message.ID + ":agent:" + agentIDText,
			Message:    item.message,
		}
		h.applyGraphMemoryProfileToDelivery(ctx, identity.WorkspaceID, &delivery)
		if !h.AgentDeliveryNotifier.NotifyWorkspaceAgentDelivery(identity.WorkspaceID, identity.DaemonID, delivery) {
			slog.Debug("Computer Agent Message redelivery deferred", "workspace_id", identity.WorkspaceID, "computer_id", identity.DaemonID, "agent_id", agentIDText, "delivery_id", delivery.DeliveryID, "seq", item.seq)
		}
	}
	return nil
}

func canonicalMessageInitiatorType(authorType string) string {
	switch strings.TrimSpace(authorType) {
	case "user":
		return "member"
	case "agent":
		return "agent"
	default:
		return ""
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func canonicalMessageDeliveryTarget(ch ChannelResponse, message ChannelMessageResponse) string {
	if message.ThreadRootMessageID != nil && strings.TrimSpace(*message.ThreadRootMessageID) != "" {
		return "thread:" + *message.ThreadRootMessageID
	}
	return "channel:" + ch.ID
}

// canonicalMessageReplyTarget projects the internal delivery key into the
// human-facing target syntax accepted by `multica message send`. The internal
// target remains stable for coordinator boundaries; the reply target is safe
// to expose to a runtime and can be reused verbatim.
func canonicalMessageReplyTarget(ctx context.Context, exec dbExecutor, ch ChannelResponse, message ChannelMessageResponse, recipientID pgtype.UUID) (string, error) {
	var base string
	switch strings.TrimSpace(ch.Kind) {
	case "", "group":
		name := strings.TrimSpace(ch.Name)
		if name == "" {
			return "", fmt.Errorf("canonical Message group channel name is empty")
		}
		base = "#" + name
	case "dm":
		var handle string
		if err := exec.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(u.name, ''), NULLIF(a.name, ''), '')
			FROM channel_member cm
			LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
			LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id AND a.archived_at IS NULL
			WHERE cm.channel_id = $1
			  AND cm.workspace_id = $2
			  AND NOT (cm.member_type = 'agent' AND cm.member_id = $3)
			ORDER BY cm.created_at ASC
			LIMIT 1`, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), recipientID).Scan(&handle); err != nil {
			return "", fmt.Errorf("resolve canonical Message DM peer: %w", err)
		}
		handle = strings.TrimSpace(handle)
		if handle == "" {
			return "", fmt.Errorf("canonical Message DM peer handle is empty")
		}
		base = "dm:@" + handle
	default:
		return "", fmt.Errorf("unsupported canonical Message channel kind %q", ch.Kind)
	}
	if message.ThreadRootMessageID != nil {
		rootID := strings.TrimSpace(*message.ThreadRootMessageID)
		if rootID != "" {
			if len(rootID) > 8 {
				rootID = rootID[:8]
			}
			base += ":" + rootID
		}
	}
	return base, nil
}

func (h *Handler) resolveCanonicalRunRecipient(ctx context.Context, ch ChannelResponse, source db.Agent) (string, string, db.Agent, bool, error) {
	var runID, runAgentID, executionAgentID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT run.run_id, run_agent.run_agent_id, run_agent.execution_agent_id
		FROM env_dispatch_run AS run
		JOIN env_dispatch_run_agent AS run_agent ON run_agent.run_id = run.run_id
		WHERE run.local_channel_id = $1
		  AND run.workspace_id = $2
		  AND run_agent.source_agent_id = $3
		  AND run.status IN ('preflight', 'running', 'quiet_candidate')
		ORDER BY run.created_at DESC
		LIMIT 1`, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), source.ID).Scan(&runID, &runAgentID, &executionAgentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", db.Agent{}, false, nil
	}
	if err != nil {
		return "", "", db.Agent{}, false, err
	}
	execution, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: executionAgentID, WorkspaceID: parseUUID(ch.WorkspaceID)})
	if err != nil {
		return "", "", db.Agent{}, false, err
	}
	return uuidToString(runID), uuidToString(runAgentID), execution, true, nil
}

func createMixedRunDeliveryObligationWithActivity(ctx context.Context, activity *service.EnvDispatchActivity, runID, runAgentID, messageID string, sourceAgentID pgtype.UUID) (bool, error) {
	if activity == nil {
		return false, errors.New("mixed-run activity service unavailable")
	}
	_, created, err := activity.CreateDeliveryObligation(ctx, service.CreateDeliveryObligationInput{
		RunID:                  parseUUID(runID),
		ChannelMessageID:       parseUUID(messageID),
		SourceRecipientAgentID: sourceAgentID,
		RunAgentID:             parseUUID(runAgentID),
		State:                  "queued",
	})
	return created, err
}

func (h *Handler) createMixedRunDeliveryObligation(ctx context.Context, runID, runAgentID, messageID, sourceAgentID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("mixed run delivery queries unavailable")
	}
	if h.TxStarter == nil {
		return errors.New("mixed run delivery transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	activity := service.NewEnvDispatchActivityFromQueries(h.Queries.WithTx(tx))
	if _, err := createMixedRunDeliveryObligationWithActivity(ctx, activity, runID, runAgentID, messageID, parseUUID(sourceAgentID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// settleMixedRunDeliveryObligation settles a run-scoped delivery obligation
// when the daemon acknowledges canonical delivery. Pending delivery counters
// decrement through the shared activity path.
func (h *Handler) settleMixedRunDeliveryObligation(ctx context.Context, channelMessageID, executionAgentID pgtype.UUID) error {
	if h == nil || h.DB == nil {
		return errors.New("mixed run delivery executor unavailable")
	}
	_, err := service.SettleDeliveryObligationForExecutionAgent(ctx, h.DB, channelMessageID, executionAgentID)
	return err
}

func (h *Handler) notifyCanonicalMessageDelivery(ctx context.Context, ch ChannelResponse, recipient db.Agent, delivery protocol.AgentDeliverPayload) {
	if h == nil || h.DB == nil || h.AgentDeliveryNotifier == nil || !recipient.RuntimeID.Valid {
		return
	}
	var daemonID *string
	if err := h.DB.QueryRow(ctx, `SELECT daemon_id FROM agent_runtime WHERE id = $1`, recipient.RuntimeID).Scan(&daemonID); err != nil {
		slog.Warn("load Agent Message delivery daemon failed", "workspace_id", ch.WorkspaceID, "agent_id", delivery.AgentID, "runtime_id", uuidToString(recipient.RuntimeID), "message_id", delivery.Message.ID, "error", err)
		return
	}
	h.applyGraphMemoryProfileToDelivery(ctx, ch.WorkspaceID, &delivery)
	if daemonID == nil || strings.TrimSpace(*daemonID) == "" || !h.AgentDeliveryNotifier.NotifyWorkspaceAgentDelivery(ch.WorkspaceID, *daemonID, delivery) {
		slog.Debug("Agent Message live delivery deferred to recovery", "workspace_id", ch.WorkspaceID, "agent_id", delivery.AgentID, "daemon_id", daemonID, "message_id", delivery.Message.ID, "delivery_id", delivery.DeliveryID)
	}
}

// canonicalMessageDeliveryRecipients is the sole recipient policy for the
// canonical Message transport. It preserves channel semantics after the #2295
// hard-cut (no dual-write inbox wakes):
//   - normal human channel messages deliver to every unmuted Agent
//   - explicit @mentions always deliver to their targets (mute does not apply)
//   - human @mentions also deliver to other unmuted joined Agents so shared
//     channel context does not disappear for bystanders
//   - agent-authored @mentions deliver only to targets (no bystander fanout;
//     keeps agent-reply loops bounded)
//   - thread replies deliver to explicit targets or active thread participants
//
// Agents never receive deliveries of their own Messages.
func (h *Handler) canonicalMessageDeliveryRecipients(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse) []db.Agent {
	mentioned := h.channelMentionedAgents(ctx, ch.WorkspaceID, ch.ID, message.Content, message.Parts)
	threadRootID := ""
	if message.ThreadRootMessageID != nil {
		threadRootID = strings.TrimSpace(*message.ThreadRootMessageID)
	}
	if threadRootID != "" {
		if len(mentioned) > 0 {
			// Thread @mentions: targets pierce mute; human mentions also keep
			// unmuted thread followers in the delivery set.
			if channelMessageIsHumanAuthored(message.Type) {
				return h.mergeCanonicalMessageDeliveryRecipients(
					h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, mentioned, false),
					h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelThreadFollowerAgents(ctx, ch.WorkspaceID, ch.ID, threadRootID), true),
				)
			}
			return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, mentioned, false)
		}
		return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelThreadFollowerAgents(ctx, ch.WorkspaceID, ch.ID, threadRootID), true)
	}
	if channelMessageIsHumanAuthored(message.Type) && channelMessageIsGroupCommand(message.Content, message.Parts) {
		return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID), true)
	}
	if len(mentioned) > 0 {
		targets := h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, mentioned, false)
		if !channelMessageIsHumanAuthored(message.Type) {
			return targets
		}
		// Human @mention: targets + unmuted bystanders (shared context).
		return h.mergeCanonicalMessageDeliveryRecipients(
			targets,
			h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID), true),
		)
	}
	if message.Type == "system" {
		return nil
	}
	return h.filterCanonicalMessageDeliveryRecipients(ctx, ch, message, h.channelAgentMembers(ctx, ch.WorkspaceID, ch.ID), true)
}

func (h *Handler) mergeCanonicalMessageDeliveryRecipients(parts ...[]db.Agent) []db.Agent {
	unique := make(map[string]struct{})
	var result []db.Agent
	for _, part := range parts {
		for _, agent := range part {
			id := uuidToString(agent.ID)
			if id == "" {
				continue
			}
			if _, ok := unique[id]; ok {
				continue
			}
			unique[id] = struct{}{}
			result = append(result, agent)
		}
	}
	return result
}

func (h *Handler) filterCanonicalMessageDeliveryRecipients(ctx context.Context, ch ChannelResponse, message ChannelMessageResponse, candidates []db.Agent, respectMute bool) []db.Agent {
	unique := make(map[string]struct{}, len(candidates))
	result := make([]db.Agent, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.RuntimeID.Valid {
			continue
		}
		candidateID := uuidToString(candidate.ID)
		if candidateID == "" {
			continue
		}
		if message.Type == "agent" && message.AuthorID != nil && candidateID == *message.AuthorID {
			continue
		}
		if respectMute && h.isChannelAgentMuted(ctx, parseUUID(ch.ID), parseUUID(ch.WorkspaceID), candidate.ID) {
			continue
		}
		if _, exists := unique[candidateID]; exists {
			continue
		}
		unique[candidateID] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

// canonicalRunRecipient retains the authoritative source membership identity
// while carrying the derived execution identity selected for this run.
type canonicalRunRecipient struct {
	SourceAgentID  string
	ExecutionAgent db.Agent
}

// mapCanonicalRunRecipients maps only recipients already selected by the
// canonical source-agent policy. A derived identity can never add its source
// agent to the selected set.
func mapCanonicalRunRecipients(sourceRecipients []db.Agent, executionBySource map[string]db.Agent) []canonicalRunRecipient {
	mapped := make([]canonicalRunRecipient, 0, len(sourceRecipients))
	for _, source := range sourceRecipients {
		sourceID := uuidToString(source.ID)
		execution, ok := executionBySource[sourceID]
		if !ok {
			continue
		}
		mapped = append(mapped, canonicalRunRecipient{SourceAgentID: sourceID, ExecutionAgent: execution})
	}
	return mapped
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	channelUnmentionedModeAttentionRound = "attention_round"
	channelUnmentionedModeLegacyFull     = "legacy_full"
	channelAttentionOutboxBatchSize      = 64
	channelAttentionResponseGrantReason  = "attention_response_grant"
	channelAttentionConvergenceReason    = "attention_convergence"
	channelAttentionManagerReason        = "attention_manager_fallback"
)

var channelAllMentionPattern = regexp.MustCompile(`(?i)(^|[\s，。！？、,:;])[@＠]all(?:$|[\s，。！？、,:;])`)

type channelAttentionQueuedEvent struct {
	eventID     pgtype.UUID
	runtimeID   pgtype.UUID
	unavailable bool
}

type channelAttentionDecision struct {
	Decision     string   `json:"decision"`
	Confidence   float64  `json:"confidence"`
	ValueType    string   `json:"value_type"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs"`
	ModelVersion string   `json:"model_version"`
	SeenUpToSeq  int64    `json:"seen_up_to_seq"`
}

type channelAttentionCompletion struct {
	decision      string
	inputTokens   int64
	outputTokens  int64
	latencyMS     int64
	roundResolved bool
	roundOutcome  string
	wakes         []channelAttentionWake
}

type channelAttentionWake struct {
	channel ChannelResponse
	agent   db.Agent
	trigger ChannelMessageResponse
	reason  string
	result  channelAgentPromptTxResult
}

type channelAttentionConvergenceVote struct {
	Vote          string `json:"vote"`
	TargetAgentID string `json:"target_agent_id"`
	Summary       string `json:"summary"`
}

type channelAttentionRoundContext struct {
	workspaceID pgtype.UUID
	channelID   pgtype.UUID
	triggerID   pgtype.UUID
	seqFrom     int64
	seqTo       int64
	channel     ChannelResponse
	trigger     ChannelMessageResponse
}

func (h *Handler) channelAttentionModeEnabled() bool {
	if h == nil || !h.cfg.ChannelAttentionEnabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(h.cfg.ChannelUnmentionedMode))
	return mode == "" || mode == channelUnmentionedModeAttentionRound
}

func (h *Handler) recordChannelUnmentionedMessage() {
	if h == nil {
		return
	}
	denominator := atomic.AddUint64(&h.channelUnmentionedMessages, 1)
	if h.Metrics != nil {
		h.Metrics.SetChannelFullExecutionAmplificationRatio(float64(atomic.LoadUint64(&h.channelUnmentionedFullWakes)) / float64(denominator))
	}
}

func (h *Handler) recordChannelUnmentionedFullWake() {
	if h == nil {
		return
	}
	numerator := atomic.AddUint64(&h.channelUnmentionedFullWakes, 1)
	if denominator := atomic.LoadUint64(&h.channelUnmentionedMessages); denominator > 0 && h.Metrics != nil {
		h.Metrics.SetChannelFullExecutionAmplificationRatio(float64(numerator) / float64(denominator))
	}
}

func channelMessageHasAgentMention(content string, parts []protocol.MessagePart) bool {
	for _, mention := range util.ParseMentionsFromContentAndParts(content, parts) {
		if mention.Type == "agent" {
			return true
		}
	}
	return false
}

func channelMessageIsGroupCommand(content string, parts []protocol.MessagePart) bool {
	for _, part := range parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefSubType == "all" {
			return true
		}
	}
	trimmed := strings.TrimSpace(content)
	return channelAllMentionPattern.MatchString(trimmed) || strings.Contains(trimmed, "大家")
}

func (h *Handler) shouldQueueChannelAttention(ch ChannelResponse, content string, parts []protocol.MessagePart) bool {
	if !h.channelAttentionModeEnabled() || ch.Kind != "group" {
		return false
	}
	if channelMessageHasAgentMention(content, parts) || channelMessageIsGroupCommand(content, parts) {
		return false
	}
	skip, _ := deterministicChannelAmbientRelevanceSkip(content)
	return !skip
}

func channelMessageIsHumanAuthored(authorType string) bool {
	switch strings.ToLower(strings.TrimSpace(authorType)) {
	case "user", "lark":
		return true
	default:
		return false
	}
}

func enqueueChannelAttentionDispatchTx(ctx context.Context, tx pgx.Tx, messageID, workspaceID, channelID, initiatorUserID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_attention_dispatch_outbox (
		  message_id, workspace_id, channel_id, initiator_user_id
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (message_id) DO NOTHING`,
		messageID, workspaceID, channelID, nullableUUID(initiatorUserID))
	return err
}

func (h *Handler) ensureChannelAttentionDispatch(ctx context.Context, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) {
	if h == nil || h.DB == nil || h.TxStarter == nil || strings.TrimSpace(trigger.ID) == "" {
		return
	}
	// Most user messages write this row atomically with the message. The
	// idempotent insert also covers older/internal message producers that enter
	// through the shared dispatcher after their message transaction commits.
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO channel_attention_dispatch_outbox (
		  message_id, workspace_id, channel_id, initiator_user_id
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (message_id) DO NOTHING`,
		parseUUID(trigger.ID), parseUUID(trigger.WorkspaceID), parseUUID(trigger.ChannelID), nullableUUID(initiatorUserID)); err != nil {
		slog.Warn("channel attention outbox ensure failed", "message_id", trigger.ID, "error", err)
		return
	}
	if err := h.processChannelAttentionDispatch(ctx, parseUUID(trigger.ID)); err != nil {
		slog.Warn("channel attention dispatch failed", "message_id", trigger.ID, "error", err)
	}
}

func (h *Handler) processExistingChannelAttentionDispatch(ctx context.Context, trigger ChannelMessageResponse) {
	if h == nil || h.DB == nil || strings.TrimSpace(trigger.ID) == "" {
		return
	}
	if err := h.processChannelAttentionDispatch(ctx, parseUUID(trigger.ID)); err != nil {
		slog.Warn("existing channel attention dispatch failed", "message_id", trigger.ID, "error", err)
	}
}

// ProcessPendingChannelAttentionDispatches retries the durable post-message
// outbox. Round creation is idempotent by channel+seq, so a crash after the
// round commits but before the outbox delete is safe.
func (h *Handler) ProcessPendingChannelAttentionDispatches(ctx context.Context, limit int) int {
	if h == nil || h.DB == nil {
		return 0
	}
	if limit <= 0 || limit > channelAttentionOutboxBatchSize {
		limit = channelAttentionOutboxBatchSize
	}
	rows, err := h.DB.Query(ctx, `
		SELECT message_id
		FROM channel_attention_dispatch_outbox
		WHERE next_attempt_at <= now()
		ORDER BY created_at, message_id
		LIMIT $1`, limit)
	if err != nil {
		slog.Warn("channel attention outbox scan failed", "error", err)
		return 0
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	processed := 0
	for _, id := range ids {
		if err := h.processChannelAttentionDispatch(ctx, id); err != nil {
			_, _ = h.DB.Exec(ctx, `
				UPDATE channel_attention_dispatch_outbox
				SET attempt = attempt + 1,
				    last_error = $2,
				    next_attempt_at = now() + LEAST(interval '30 seconds', make_interval(secs => power(2, LEAST(attempt, 4))::int)),
				    updated_at = now()
				WHERE message_id = $1`, id, err.Error())
			continue
		}
		processed++
	}
	return processed
}

func (h *Handler) processChannelAttentionDispatch(ctx context.Context, messageID pgtype.UUID) error {
	if !messageID.Valid || h.TxStarter == nil {
		return nil
	}
	var workspaceID, channelID, initiatorUserID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT workspace_id, channel_id, initiator_user_id
		FROM channel_attention_dispatch_outbox
		WHERE message_id = $1`, messageID).Scan(&workspaceID, &channelID, &initiatorUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load channel attention outbox: %w", err)
	}
	ch, ok := h.getChannel(ctx, uuidToString(workspaceID), channelID)
	if !ok || ch.Kind != "group" {
		_, _ = h.DB.Exec(ctx, `DELETE FROM channel_attention_dispatch_outbox WHERE message_id = $1`, messageID)
		return nil
	}
	msg, err := scanChannelMessage(h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND channel_id = $3`, messageID, workspaceID, channelID))
	if err != nil {
		return fmt.Errorf("load channel attention trigger: %w", err)
	}
	if !channelMessageIsHumanAuthored(msg.Type) || channelMessageHasAgentMention(msg.Content, msg.Parts) || channelMessageIsGroupCommand(msg.Content, msg.Parts) {
		_, _ = h.DB.Exec(ctx, `DELETE FROM channel_attention_dispatch_outbox WHERE message_id = $1`, messageID)
		return nil
	}
	if !h.channelAttentionModeEnabled() {
		var alreadyRepresented bool
		if err := h.DB.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM channel_attention_round
			  WHERE workspace_id = $1 AND channel_id = $2 AND $3 BETWEEN seq_from AND seq_to
			)`, workspaceID, channelID, msg.Seq).Scan(&alreadyRepresented); err != nil {
			return fmt.Errorf("check legacy attention rollback idempotency: %w", err)
		}
		if alreadyRepresented {
			_, err := h.DB.Exec(ctx, `DELETE FROM channel_attention_dispatch_outbox WHERE message_id = $1`, messageID)
			return err
		}
		h.dispatchChannelMessageWake(ctx, ch, msg, initiatorUserID)
		if _, err := h.DB.Exec(ctx, `DELETE FROM channel_attention_dispatch_outbox WHERE message_id = $1`, messageID); err != nil {
			return fmt.Errorf("delete legacy channel attention outbox: %w", err)
		}
		return nil
	}
	queued, created, err := h.createOrMergeChannelAttentionRound(ctx, ch, msg, initiatorUserID)
	if err != nil {
		return err
	}
	if _, err := h.DB.Exec(ctx, `DELETE FROM channel_attention_dispatch_outbox WHERE message_id = $1`, messageID); err != nil {
		return fmt.Errorf("delete channel attention outbox: %w", err)
	}
	runnable := 0
	for _, item := range queued {
		if item.unavailable {
			if h.Metrics != nil {
				h.Metrics.RecordChannelAttentionProbe("none", "unavailable")
			}
			continue
		}
		runnable++
		if event, err := h.Queries.GetAgentInboxEvent(ctx, item.eventID); err == nil {
			h.publishAgentInboxTaskLifecycle(protocol.EventTaskQueued, event, item.runtimeID, "queued")
		}
	}
	if created && runnable == 0 && h.Metrics != nil {
		h.Metrics.RecordChannelAttentionRound("failed")
	}
	return nil
}

func (h *Handler) createOrMergeChannelAttentionRound(ctx context.Context, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID) ([]channelAttentionQueuedEvent, bool, error) {
	if h.TxStarter == nil {
		return nil, false, errors.New("channel attention transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin channel attention round: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('channel_attention_round'), hashtext($1))`, ch.ID); err != nil {
		return nil, false, fmt.Errorf("lock channel attention round: %w", err)
	}

	var existingID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM channel_attention_round
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND $3 BETWEEN seq_from AND seq_to
		ORDER BY created_at DESC
		LIMIT 1`, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), trigger.Seq).Scan(&existingID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("check channel attention idempotency: %w", err)
	}

	// Freeze any elapsed debounce window before creating the next collecting
	// round. Running rounds remain resolving while a later bundle collects.
	if _, err := tx.Exec(ctx, `
		UPDATE channel_attention_round
		SET status = 'resolving', updated_at = now()
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND status = 'collecting'
		  AND dispatch_at <= now()`, parseUUID(ch.WorkspaceID), parseUUID(ch.ID)); err != nil {
		return nil, false, fmt.Errorf("freeze elapsed channel attention round: %w", err)
	}

	debounce := h.cfg.ChannelAttentionDebounce
	if debounce <= 0 {
		debounce = 3 * time.Second
	}
	maxWait := h.cfg.ChannelAttentionMaxWait
	if maxWait <= debounce {
		maxWait = 8 * time.Second
	}
	debounceMS := debounce.Milliseconds()
	maxWaitMS := maxWait.Milliseconds()

	var roundID pgtype.UUID
	var roundSeqFrom, roundSeqTo int64
	created := false
	err = tx.QueryRow(ctx, `
		SELECT id, seq_from, seq_to
		FROM channel_attention_round
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND status = 'collecting'
		  AND dispatch_at > now()
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE`, parseUUID(ch.WorkspaceID), parseUUID(ch.ID)).Scan(&roundID, &roundSeqFrom, &roundSeqTo)
	if errors.Is(err, pgx.ErrNoRows) {
		created = true
		roundSeqFrom, roundSeqTo = trigger.Seq, trigger.Seq
		err = tx.QueryRow(ctx, `
			INSERT INTO channel_attention_round (
			  workspace_id, channel_id, trigger_message_id, seq_from, seq_to,
			  status, dispatch_at, deadline_at
			)
			VALUES (
			  $1, $2, $3, $4, $4, 'collecting',
			  now() + make_interval(secs => $5::double precision / 1000.0),
			  now() + make_interval(secs => $6::double precision / 1000.0)
			)
			RETURNING id`, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), parseUUID(trigger.ID), trigger.Seq, debounceMS, maxWaitMS).Scan(&roundID)
		if err != nil {
			return nil, false, fmt.Errorf("create channel attention round: %w", err)
		}
	} else if err != nil {
		return nil, false, fmt.Errorf("load collecting channel attention round: %w", err)
	} else {
		if trigger.Seq < roundSeqFrom {
			roundSeqFrom = trigger.Seq
		}
		if trigger.Seq > roundSeqTo {
			roundSeqTo = trigger.Seq
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_attention_round
			SET trigger_message_id = $2,
			    seq_from = LEAST(seq_from, $3),
			    seq_to = GREATEST(seq_to, $3),
			    dispatch_at = LEAST(
			      now() + make_interval(secs => $4::double precision / 1000.0),
			      deadline_at - interval '1 millisecond'
			    ),
			    updated_at = now()
			WHERE id = $1`, roundID, parseUUID(trigger.ID), trigger.Seq, debounceMS); err != nil {
			return nil, false, fmt.Errorf("merge channel attention round: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_inbox_event event
			SET seq_from = LEAST(event.seq_from, $2),
			    seq_to = GREATEST(event.seq_to, $3),
			    source_message_id = $4,
			    updated_at = now()
			FROM channel_attention_participant participant
			WHERE participant.round_id = $1
			  AND participant.inbox_event_id = event.id
			  AND event.status IN ('pending', 'failed')`, roundID, roundSeqFrom, roundSeqTo, parseUUID(trigger.ID)); err != nil {
			return nil, false, fmt.Errorf("merge channel attention inbox range: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit merged channel attention round: %w", err)
		}
		return nil, false, nil
	}

	queued, err := h.createChannelAttentionParticipantsTx(ctx, tx, roundID, ch, trigger, initiatorUserID, roundSeqFrom, roundSeqTo)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_attention_round round
		SET expected_agent_count = (
		      SELECT count(*) FROM channel_attention_participant participant
		      WHERE participant.round_id = round.id
		    ),
		    updated_at = now()
		WHERE round.id = $1`, roundID); err != nil {
		return nil, false, fmt.Errorf("update channel attention expected count: %w", err)
	}
	runnable := 0
	for _, item := range queued {
		if !item.unavailable {
			runnable++
		}
	}
	if runnable == 0 {
		if _, err := settleChannelAttentionRoundTx(ctx, tx, roundID); err != nil {
			return nil, false, fmt.Errorf("resolve empty channel attention round: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit channel attention round: %w", err)
	}
	return queued, created, nil
}

func (h *Handler) createChannelAttentionParticipantsTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID, ch ChannelResponse, trigger ChannelMessageResponse, initiatorUserID pgtype.UUID, seqFrom, seqTo int64) ([]channelAttentionQueuedEvent, error) {
	agents, err := h.channelAgentMembersWithDB(ctx, tx, ch.WorkspaceID, ch.ID)
	if err != nil {
		return nil, fmt.Errorf("load channel attention members: %w", err)
	}
	conversationID, err := h.channelConversationIDWithDB(ctx, tx, parseUUID(ch.ID))
	if err != nil {
		return nil, fmt.Errorf("load channel attention conversation: %w", err)
	}
	qtx := h.Queries.WithTx(tx)
	queued := make([]channelAttentionQueuedEvent, 0, len(agents))
	for _, candidate := range agents {
		var muted bool
		if err := tx.QueryRow(ctx, `
			SELECT muted_at IS NOT NULL
			FROM channel_member
			WHERE workspace_id = $1 AND channel_id = $2
			  AND member_type = 'agent' AND member_id = $3`,
			parseUUID(ch.WorkspaceID), parseUUID(ch.ID), candidate.ID).Scan(&muted); err != nil {
			return nil, fmt.Errorf("load channel attention mute state: %w", err)
		}
		if muted {
			continue
		}
		agentSession, err := qtx.UpsertAgentSession(ctx, db.UpsertAgentSessionParams{
			WorkspaceID:    parseUUID(ch.WorkspaceID),
			AgentID:        candidate.ID,
			ConversationID: conversationID,
			Scope:          "channel",
			ChannelID:      parseUUID(ch.ID),
		})
		if err != nil {
			return nil, fmt.Errorf("upsert channel attention agent session: %w", err)
		}
		runtime, available := h.channelAttentionRuntime(ctx, qtx, candidate)
		if !available {
			if _, err := tx.Exec(ctx, `
				INSERT INTO channel_attention_participant (round_id, agent_id, status, last_error, completed_at)
				VALUES ($1, $2, 'unavailable', 'runtime unavailable or incompatible', now())
				ON CONFLICT (round_id, agent_id) DO NOTHING`, roundID, candidate.ID); err != nil {
				return nil, fmt.Errorf("record unavailable channel attention participant: %w", err)
			}
			if err := upsertChannelObserveInboxEventTx(ctx, tx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), candidate.ID,
				agentSession.ID, conversationID, parseUUID(trigger.ID), seqFrom, seqTo); err != nil {
				return nil, fmt.Errorf("record unavailable channel observation: %w", err)
			}
			queued = append(queued, channelAttentionQueuedEvent{unavailable: true})
			continue
		}
		contextMessages := h.cfg.ChannelAttentionContextMessages
		if contextMessages <= 0 || contextMessages > 8 {
			contextMessages = 8
		}
		memoryBudget := h.cfg.ChannelAttentionMemoryBudgetBytes
		if memoryBudget <= 0 || memoryBudget > 4*1024 {
			memoryBudget = 4 * 1024
		}
		maxOutput := h.cfg.ChannelAttentionMaxOutputTokens
		if maxOutput <= 0 || maxOutput > 96 {
			maxOutput = 96
		}
		var eventID pgtype.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (
			  workspace_id, agent_session_id, conversation_id, channel_id,
			  agent_id, runtime_id, execution_config, source_message_id,
			  reason, delivery_mode, response_mode, requires_wake, status,
			  priority, seq_from, seq_to
			)
			SELECT
			  $1, $2, $3, $4, agent.id, $6,
			  jsonb_build_object('execution_config', jsonb_build_object(
			    'model', COALESCE(agent.model, ''),
			    'thinking_level', COALESCE(agent.thinking_level, ''),
			    'execution_profile', $7,
			    'context_messages', $8,
			    'memory_budget_bytes', $9,
			    'max_output_tokens', $10,
			    'tools_enabled', false,
			    'snapshotted', true
			  )),
			  $11, 'ambient', 'attention', 'no_public_output', true,
			  'pending', 1, $12, $13
			FROM agent
			WHERE agent.id = $5
			RETURNING id`,
			parseUUID(ch.WorkspaceID), agentSession.ID, conversationID, parseUUID(ch.ID), candidate.ID, runtime.ID,
			service.ExecutionProfileAttentionProbe, contextMessages, memoryBudget, maxOutput,
			parseUUID(trigger.ID), seqFrom, seqTo).Scan(&eventID)
		if err != nil {
			return nil, fmt.Errorf("create channel attention inbox event: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_attention_participant (round_id, agent_id, inbox_event_id, status)
			VALUES ($1, $2, $3, 'pending')`, roundID, candidate.ID, eventID); err != nil {
			return nil, fmt.Errorf("create channel attention participant: %w", err)
		}
		queued = append(queued, channelAttentionQueuedEvent{eventID: eventID, runtimeID: runtime.ID})
	}
	return queued, nil
}

func (h *Handler) channelAttentionRuntime(ctx context.Context, q *db.Queries, candidate db.Agent) (db.AgentRuntime, bool) {
	if !candidate.RuntimeID.Valid {
		return db.AgentRuntime{}, false
	}
	runtime, err := q.GetAgentRuntime(ctx, candidate.RuntimeID)
	if err != nil || runtime.Status != "online" || !strings.EqualFold(runtime.Provider, "pi") {
		return db.AgentRuntime{}, false
	}
	if !agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityRestrictedExecution) {
		return db.AgentRuntime{}, false
	}
	return runtime, true
}

func upsertChannelObserveInboxEventTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID, agentID, agentSessionID, conversationID, sourceMessageID pgtype.UUID, seqFrom, seqTo int64) error {
	var eventID pgtype.UUID
	return tx.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, agent_session_id, conversation_id, channel_id, agent_id,
		  runtime_id, execution_config, source_message_id, reason,
		  delivery_mode, response_mode, requires_wake, status, priority, seq_from, seq_to
		)
		SELECT $1, $2, $3, $4, agent.id, agent.runtime_id,
		       jsonb_build_object('execution_config', jsonb_build_object(
		         'model', COALESCE(agent.model, ''),
		         'thinking_level', COALESCE(agent.thinking_level, ''),
		         'execution_profile', 'full', 'snapshotted', true
		       )),
		       $6, 'ambient', 'observe', 'no_public_output', false, 'pending', 0, $7, $8
		FROM agent WHERE agent.id = $5
		ON CONFLICT (conversation_id, agent_id)
		  WHERE reason = 'ambient'
		    AND delivery_mode = 'observe'
		    AND status IN ('pending', 'failed')
		    AND conversation_id IS NOT NULL
		DO UPDATE SET
		  agent_session_id = EXCLUDED.agent_session_id,
		  channel_id = EXCLUDED.channel_id,
		  source_message_id = COALESCE(EXCLUDED.source_message_id, agent_inbox_event.source_message_id),
		  status = 'pending',
		  seq_from = LEAST(agent_inbox_event.seq_from, EXCLUDED.seq_from),
		  seq_to = GREATEST(agent_inbox_event.seq_to, EXCLUDED.seq_to),
		  updated_at = now()
		RETURNING id`, workspaceID, agentSessionID, conversationID, channelID, agentID,
		sourceMessageID, seqFrom, seqTo).Scan(&eventID)
}

func isChannelAttentionInboxEvent(event db.AgentInboxEvent) bool {
	config, ok := service.TaskExecutionConfigFromContext(event.ExecutionConfig)
	return ok && config.ExecutionProfile == service.ExecutionProfileAttentionProbe
}

// leaseAgentInboxEventForRuntime keeps the historical per-agent serialization
// while adding debounce, deadline, runtime-capability, and runtime-capacity
// gates for Attention participants. The runtime advisory lock makes the cap
// exact across concurrent drain requests.
func (h *Handler) leaseAgentInboxEventForRuntime(ctx context.Context, runtime db.AgentRuntime) (db.AgentEventDelivery, error) {
	if h.TxStarter == nil {
		return db.AgentEventDelivery{}, errors.New("transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('channel_attention_runtime'), hashtext($1))`, uuidToString(runtime.ID)); err != nil {
		return db.AgentEventDelivery{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('channel_attention_lifecycle'))`); err != nil {
		return db.AgentEventDelivery{}, err
	}
	cap := h.cfg.ChannelAttentionMaxConcurrentPerRuntime
	if cap <= 0 {
		cap = 16
	}
	attentionCompatible := runtime.Status == "online" && strings.EqualFold(runtime.Provider, "pi") && agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityRestrictedExecution)

	var eventID, participantID, roundID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT event.id, participant.id, participant.round_id
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		JOIN agent ON agent.id = event.agent_id
		LEFT JOIN channel_attention_participant participant ON participant.inbox_event_id = event.id
		LEFT JOIN channel_attention_round round ON round.id = participant.round_id
		WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_event_delivery active_delivery
		    JOIN agent_session active_session ON active_session.id = active_delivery.agent_session_id
		    WHERE active_session.agent_id = event.agent_id
		      AND active_delivery.status IN ('leased', 'processing')
		      AND active_delivery.lease_expires_at > now()
		  )
		  AND (
		    participant.id IS NULL
		    OR (
		      $3::boolean
		      AND participant.status = 'pending'
		      AND round.status IN ('collecting', 'resolving')
		      AND round.dispatch_at <= now()
		      AND round.deadline_at > now()
		      AND (
		        SELECT count(*)
		        FROM channel_attention_participant active_participant
		        JOIN agent_inbox_event active_event ON active_event.id = active_participant.inbox_event_id
		        WHERE active_participant.status = 'running'
		          AND active_event.runtime_id = $1
		      ) < $2
		    )
		  )
		ORDER BY event.priority DESC, event.requires_wake DESC, event.created_at, event.id
		LIMIT 1
		FOR UPDATE OF agent, event SKIP LOCKED`, runtime.ID, cap, attentionCompatible).Scan(&eventID, &participantID, &roundID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = tx.Exec(ctx, `
				UPDATE channel_attention_participant participant
				SET last_error = 'runtime_capacity', updated_at = now()
				FROM agent_inbox_event event, channel_attention_round round
				WHERE participant.inbox_event_id = event.id
				  AND participant.round_id = round.id
				  AND event.runtime_id = $1
				  AND participant.status = 'pending'
				  AND round.dispatch_at <= now() AND round.deadline_at > now()
				  AND (SELECT count(*) FROM channel_attention_participant active
				       JOIN agent_inbox_event active_event ON active_event.id = active.inbox_event_id
				       WHERE active.status = 'running' AND active_event.runtime_id = $1) >= $2`, runtime.ID, cap)
			_ = tx.Commit(ctx)
		}
		return db.AgentEventDelivery{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'draining', claimed_at = now(), attempt = attempt + 1, updated_at = now()
		WHERE id = $1`, eventID); err != nil {
		return db.AgentEventDelivery{}, err
	}
	if participantID.Valid {
		result, err := tx.Exec(ctx, `
			UPDATE channel_attention_participant
			SET status = 'running', started_at = COALESCE(started_at, now()),
			    last_error = NULL, updated_at = now()
			WHERE id = $1 AND status = 'pending'`, participantID)
		if err != nil {
			return db.AgentEventDelivery{}, err
		}
		if result.RowsAffected() != 1 {
			return db.AgentEventDelivery{}, pgx.ErrNoRows
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_attention_round
			SET status = 'resolving', updated_at = now()
			WHERE id = $1 AND status = 'collecting'`, roundID); err != nil {
			return db.AgentEventDelivery{}, err
		}
	}
	var delivery db.AgentEventDelivery
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
		  workspace_id, agent_session_id, inbox_event_id, runtime_id, status
		)
		SELECT workspace_id, agent_session_id, id, $2, 'leased'
		FROM agent_inbox_event
		WHERE id = $1
		RETURNING id, workspace_id, agent_session_id, inbox_event_id, runtime_id,
		          status, lease_token, leased_at, lease_expires_at, acked_at,
		          last_error, created_at, updated_at`, eventID, runtime.ID).Scan(
		&delivery.ID, &delivery.WorkspaceID, &delivery.AgentSessionID, &delivery.InboxEventID,
		&delivery.RuntimeID, &delivery.Status, &delivery.LeaseToken, &delivery.LeasedAt,
		&delivery.LeaseExpiresAt, &delivery.AckedAt, &delivery.LastError,
		&delivery.CreatedAt, &delivery.UpdatedAt,
	)
	if err != nil {
		return db.AgentEventDelivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AgentEventDelivery{}, err
	}
	return delivery, nil
}

func (h *Handler) reclaimChannelAttentionParticipantsForRuntime(ctx context.Context, runtimeID pgtype.UUID) error {
	_, err := h.DB.Exec(ctx, `
		UPDATE channel_attention_participant participant
		SET status = 'pending', started_at = NULL, updated_at = now()
		FROM agent_inbox_event event
		WHERE participant.inbox_event_id = event.id
		  AND event.runtime_id = $1
		  AND participant.status = 'running'
		  AND event.status IN ('pending', 'failed')
		  AND EXISTS (
		    SELECT 1 FROM channel_attention_round round
		    WHERE round.id = participant.round_id
		      AND round.deadline_at > now()
		      AND round.status IN ('collecting', 'resolving')
		  )`, runtimeID)
	return err
}

func (h *Handler) countReadyAgentInboxEventsForRuntime(ctx context.Context, runtime db.AgentRuntime) (int64, error) {
	attentionCompatible := runtime.Status == "online" && strings.EqualFold(runtime.Provider, "pi") && agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityRestrictedExecution)
	var count int64
	err := h.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event event
		JOIN agent_session session ON session.id = event.agent_session_id
		LEFT JOIN channel_attention_participant participant ON participant.inbox_event_id = event.id
		LEFT JOIN channel_attention_round round ON round.id = participant.round_id
		WHERE COALESCE(event.runtime_id, session.runtime_id) = $1
		  AND session.status = 'active'
		  AND event.status IN ('pending', 'failed')
		  AND (
		    participant.id IS NULL
		    OR ($2::boolean AND participant.status = 'pending' AND round.dispatch_at <= now() AND round.deadline_at > now())
		  )`, runtime.ID, attentionCompatible).Scan(&count)
	return count, err
}

func (h *Handler) populateChannelAttentionTaskContext(ctx context.Context, event db.AgentInboxEvent, resp *AgentTaskResponse) bool {
	if !event.ChannelID.Valid || !event.WorkspaceID.Valid || event.SeqTo <= 0 {
		return false
	}
	contextMessages := 8
	if config, ok := service.TaskExecutionConfigFromContext(event.ExecutionConfig); ok && config.ContextMessages > 0 {
		contextMessages = config.ContextMessages
	}
	if contextMessages <= 0 || contextMessages > 8 {
		contextMessages = 8
	}
	cursor := event.SeqFrom - int64(contextMessages) - 1
	if cursor < 0 {
		cursor = 0
	}
	messages := h.channelAmbientUnreadMessages(ctx, h.DB, uuidToString(event.WorkspaceID), uuidToString(event.ChannelID), cursor, event.SeqTo, contextMessages)
	if len(messages) == 0 {
		return false
	}
	var contextLines, bundleLines []string
	for _, message := range messages {
		line := fmt.Sprintf("[seq:%d message:%s] %s", message.Seq, message.ID, formatChannelMessageLine(message))
		if message.Seq >= event.SeqFrom {
			bundleLines = append(bundleLines, line)
		} else {
			contextLines = append(contextLines, line)
		}
	}
	if len(bundleLines) == 0 {
		return false
	}
	resp.WorkspaceID = uuidToString(event.WorkspaceID)
	resp.ChannelID = uuidToString(event.ChannelID)
	resp.Kind = "chat"
	resp.ThreadName = "channel attention probe"
	resp.ChatContextSummary = strings.Join(contextLines, "\n")
	resp.ChatMessage = strings.Join(bundleLines, "\n")
	if event.SourceMessageID.Valid {
		h.populateAgentInboxInitiator(ctx, event.SourceMessageID, resp)
	}
	return true
}

func boundedChannelAttentionMemories(memories []service.AgentMemoryData, maxBytes int) []service.AgentMemoryData {
	if maxBytes <= 0 {
		return nil
	}
	out := make([]service.AgentMemoryData, 0, len(memories))
	used := 2 // JSON array brackets on the wire.
	for _, memory := range memories {
		raw, err := json.Marshal(memory)
		separator := 0
		if len(out) > 0 {
			separator = 1
		}
		if err != nil || used+separator+len(raw) > maxBytes {
			continue
		}
		out = append(out, memory)
		used += separator + len(raw)
	}
	return out
}

func parseChannelAttentionDecision(raw json.RawMessage) (channelAttentionDecision, error) {
	var decision channelAttentionDecision
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return decision, errors.New("attention probe internal_output is required")
	}
	if len([]byte(trimmed)) > 4*1024 {
		return decision, errors.New("attention probe internal_output exceeds 4096 bytes")
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return decision, fmt.Errorf("invalid attention probe internal_output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return decision, errors.New("attention probe internal_output must contain exactly one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil || fields == nil {
		return decision, errors.New("attention probe internal_output must be a JSON object")
	}
	required := []string{"decision", "confidence", "value_type", "summary", "evidence_refs", "model_version", "seen_up_to_seq"}
	if len(fields) != len(required) {
		return decision, errors.New("attention probe internal_output must contain exactly seven fields")
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || strings.TrimSpace(string(value)) == "null" {
			return decision, fmt.Errorf("attention probe internal_output missing field %q", key)
		}
	}
	switch decision.Decision {
	case "SILENT", "ANSWER", "CONTRIBUTE", "COORDINATE":
	default:
		return decision, fmt.Errorf("invalid attention decision %q", decision.Decision)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return decision, errors.New("attention confidence must be within [0,1]")
	}
	switch decision.ValueType {
	case "none", "direct_answer", "unique_evidence", "correction", "task_claim", "needs_protocol":
	default:
		return decision, fmt.Errorf("invalid attention value_type %q", decision.ValueType)
	}
	if decision.EvidenceRefs == nil {
		return decision, errors.New("attention evidence_refs must be an array")
	}
	if len(decision.EvidenceRefs) > 16 {
		return decision, errors.New("attention evidence_refs exceeds 16 entries")
	}
	for _, ref := range decision.EvidenceRefs {
		if strings.TrimSpace(ref) == "" || len([]byte(ref)) > 256 {
			return decision, errors.New("attention evidence_refs entries must be non-empty and at most 256 bytes")
		}
	}
	if len([]byte(decision.Summary)) > 1024 {
		return decision, errors.New("attention summary exceeds 1024 bytes")
	}
	if strings.TrimSpace(decision.ModelVersion) == "" {
		return decision, errors.New("attention model_version is required")
	}
	if len([]byte(decision.ModelVersion)) > 256 {
		return decision, errors.New("attention model_version exceeds 256 bytes")
	}
	if decision.SeenUpToSeq < 0 {
		return decision, errors.New("attention seen_up_to_seq must be non-negative")
	}
	return decision, nil
}

func parseChannelAttentionConvergenceVote(raw json.RawMessage) (channelAttentionConvergenceVote, error) {
	var vote channelAttentionConvergenceVote
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return vote, errors.New("attention convergence internal_output is required")
	}
	if len([]byte(trimmed)) > 2*1024 {
		return vote, errors.New("attention convergence internal_output exceeds 2048 bytes")
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&vote); err != nil {
		return vote, fmt.Errorf("invalid attention convergence internal_output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return vote, errors.New("attention convergence internal_output must contain exactly one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil || fields == nil {
		return vote, errors.New("attention convergence internal_output must be a JSON object")
	}
	if len(fields) != 3 {
		return vote, errors.New("attention convergence internal_output must contain exactly three fields")
	}
	for _, key := range []string{"vote", "target_agent_id", "summary"} {
		value, ok := fields[key]
		if !ok || strings.TrimSpace(string(value)) == "null" {
			return vote, fmt.Errorf("attention convergence internal_output missing field %q", key)
		}
	}
	switch vote.Vote {
	case "YIELD", "KEEP", "MERGE", "REQUEST_MANAGER":
	default:
		return vote, fmt.Errorf("invalid attention convergence vote %q", vote.Vote)
	}
	if len([]byte(vote.Summary)) > 1024 {
		return vote, errors.New("attention convergence summary exceeds 1024 bytes")
	}
	if vote.TargetAgentID != "" {
		if _, err := uuid.Parse(vote.TargetAgentID); err != nil {
			return vote, errors.New("attention convergence target_agent_id must be a UUID or empty string")
		}
	}
	return vote, nil
}

func (h *Handler) completeChannelAttentionParticipantTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent, executionID pgtype.UUID, decision channelAttentionDecision) (channelAttentionCompletion, error) {
	var participantID, roundID pgtype.UUID
	var participantStatus string
	var roundSeqTo int64
	var startedAt pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT participant.id, participant.round_id, participant.status, round.seq_to, participant.started_at
		FROM channel_attention_participant participant
		JOIN channel_attention_round round ON round.id = participant.round_id
		WHERE participant.inbox_event_id = $1
		FOR UPDATE OF round, participant`, event.ID).Scan(&participantID, &roundID, &participantStatus, &roundSeqTo, &startedAt)
	if err != nil {
		return channelAttentionCompletion{}, err
	}
	if participantStatus != "running" {
		return channelAttentionCompletion{}, fmt.Errorf("attention participant is %s", participantStatus)
	}

	var inputTokens, outputTokens int64
	var actualModel pgtype.Text
	err = tx.QueryRow(ctx, `
		SELECT execution.id,
		       COALESCE(sum(usage.input_tokens), 0)::bigint,
		       COALESCE(sum(usage.output_tokens), 0)::bigint,
		       max(NULLIF(usage.model, ''))
		FROM agent_execution execution
		LEFT JOIN agent_usage usage ON usage.execution_id = execution.id
		WHERE execution.source_kind = 'inbox'
		  AND execution.source_event_id = $1
		  AND execution.id = $2
		GROUP BY execution.id`, event.ID, executionID).Scan(&executionID, &inputTokens, &outputTokens, &actualModel)
	if err != nil {
		return channelAttentionCompletion{}, err
	}
	maxOutputTokens := int64(96)
	if config, ok := service.TaskExecutionConfigFromContext(event.ExecutionConfig); ok && config.MaxOutputTokens > 0 && config.MaxOutputTokens < int(maxOutputTokens) {
		maxOutputTokens = int64(config.MaxOutputTokens)
	}
	if outputTokens > maxOutputTokens {
		return channelAttentionCompletion{}, fmt.Errorf("attention output token usage %d exceeds limit %d", outputTokens, maxOutputTokens)
	}
	modelVersion := ""
	if actualModel.Valid {
		modelVersion = strings.TrimSpace(actualModel.String)
	}
	if modelVersion == "" {
		return channelAttentionCompletion{}, errors.New("attention execution usage model is missing")
	}
	decision.ModelVersion = modelVersion
	decision.SeenUpToSeq = roundSeqTo
	evidence, err := json.Marshal(decision.EvidenceRefs)
	if err != nil {
		return channelAttentionCompletion{}, err
	}
	latencyMS := int64(0)
	if startedAt.Valid {
		latencyMS = time.Since(startedAt.Time).Milliseconds()
		if latencyMS < 0 {
			latencyMS = 0
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_attention_participant
		SET execution_id = $2,
		    status = 'decided', decision = $3, confidence = $4, value_type = $5,
		    summary = $6, evidence_refs = $7::jsonb, seen_up_to_seq = $8,
		    input_tokens = $9, output_tokens = $10, model_version = $11,
		    latency_ms = $12, completed_at = now(), updated_at = now()
		WHERE id = $1`, participantID, nullableUUID(executionID), decision.Decision,
		decision.Confidence, decision.ValueType, decision.Summary, evidence,
		decision.SeenUpToSeq, inputTokens, outputTokens, decision.ModelVersion, latencyMS); err != nil {
		return channelAttentionCompletion{}, err
	}
	if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
		WorkspaceID: event.WorkspaceID, ChannelID: event.ChannelID, SourceKind: "attention_participant",
		SourceID: participantID, EventType: "attention_decision", AgentID: event.AgentID, InboxEventID: event.ID,
		Payload: map[string]any{
			"round_id": uuidToString(roundID), "decision": decision.Decision, "confidence": decision.Confidence,
			"value_type": decision.ValueType, "summary": decision.Summary, "seen_up_to_seq": decision.SeenUpToSeq,
			"input_tokens": inputTokens, "output_tokens": outputTokens, "model_version": decision.ModelVersion,
		},
	}); err != nil {
		return channelAttentionCompletion{}, err
	}
	if err := recordAttentionTrainingExampleExec(ctx, tx, attentionTrainingExampleInput{
		workspaceID: event.WorkspaceID, channelID: event.ChannelID, messageID: event.SourceMessageID,
		agentID: event.AgentID, inboxEventID: event.ID, participantID: participantID, roundID: roundID,
		executionID: executionID, decision: decision, inputTokens: inputTokens, outputTokens: outputTokens, latencyMS: latencyMS,
	}); err != nil {
		return channelAttentionCompletion{}, err
	}
	resolved, err := settleChannelAttentionRoundTx(ctx, tx, roundID)
	if err != nil {
		return channelAttentionCompletion{}, err
	}
	roundOutcome := ""
	var wakes []channelAttentionWake
	if resolved {
		roundOutcome, err = channelAttentionRoundOutcomeTx(ctx, tx, roundID)
		if err != nil {
			return channelAttentionCompletion{}, err
		}
		wakes, err = h.resolveCompletedChannelAttentionRoundTx(ctx, tx, roundID)
		if err != nil {
			return channelAttentionCompletion{}, err
		}
	}
	return channelAttentionCompletion{
		decision: decision.Decision, inputTokens: inputTokens, outputTokens: outputTokens,
		latencyMS: latencyMS, roundResolved: resolved, roundOutcome: roundOutcome, wakes: wakes,
	}, nil
}

func (h *Handler) resolveCompletedChannelAttentionRoundTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) ([]channelAttentionWake, error) {
	if h == nil {
		return nil, nil
	}
	roundCtx, err := h.channelAttentionRoundContextTx(ctx, tx, roundID)
	if err != nil {
		return nil, err
	}
	if err := upsertChannelAttentionContributionOffersTx(ctx, tx, roundID); err != nil {
		return nil, err
	}

	type answerCandidate struct {
		participantID pgtype.UUID
		agentID       pgtype.UUID
		confidence    float64
		summary       string
	}
	rows, err := tx.Query(ctx, `
		SELECT id, agent_id, COALESCE(confidence, 0), summary
		FROM channel_attention_participant
		WHERE round_id = $1 AND status = 'decided' AND decision = 'ANSWER'
		ORDER BY COALESCE(confidence, 0) DESC, completed_at ASC, agent_id ASC`, roundID)
	if err != nil {
		return nil, err
	}
	var answers []answerCandidate
	for rows.Next() {
		var item answerCandidate
		if err := rows.Scan(&item.participantID, &item.agentID, &item.confidence, &item.summary); err != nil {
			rows.Close()
			return nil, err
		}
		answers = append(answers, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var coordinateCount, decidedCount, unsuccessfulCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'decided' AND decision = 'COORDINATE')::int,
		       count(*) FILTER (WHERE status = 'decided')::int,
		       count(*) FILTER (WHERE status IN ('failed', 'timed_out', 'unavailable'))::int
		FROM channel_attention_participant
		WHERE round_id = $1`, roundID).Scan(&coordinateCount, &decidedCount, &unsuccessfulCount); err != nil {
		return nil, err
	}

	switch len(answers) {
	case 0:
		if coordinateCount > 0 || (decidedCount == 0 && unsuccessfulCount > 0) {
			return h.grantChannelAttentionManagerFallbackTx(ctx, tx, roundCtx, roundID, "coordinate_or_all_failed")
		}
		return nil, nil
	case 1:
		return h.grantChannelAttentionResponderTx(ctx, tx, roundCtx, roundID, answers[0].agentID, "unique_answer", "single ANSWER decision")
	default:
		return nil, h.createChannelAttentionConvergenceTurnsTx(ctx, tx, roundCtx, roundID)
	}
}

func (h *Handler) channelAttentionRoundContextTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) (channelAttentionRoundContext, error) {
	var rc channelAttentionRoundContext
	if err := tx.QueryRow(ctx, `
		SELECT workspace_id, channel_id, trigger_message_id, seq_from, seq_to
		FROM channel_attention_round
		WHERE id = $1`, roundID).Scan(&rc.workspaceID, &rc.channelID, &rc.triggerID, &rc.seqFrom, &rc.seqTo); err != nil {
		return rc, err
	}
	ch, ok := h.getChannel(ctx, uuidToString(rc.workspaceID), rc.channelID)
	if !ok {
		return rc, errors.New("attention round channel not found")
	}
	rc.channel = ch
	row := tx.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND (($3::uuid IS NOT NULL AND id = $3) OR ($3::uuid IS NULL AND seq = $4))
		ORDER BY seq DESC
		LIMIT 1`, rc.workspaceID, rc.channelID, nullableUUID(rc.triggerID), rc.seqTo)
	trigger, err := scanChannelMessage(row)
	if err != nil {
		return rc, fmt.Errorf("load attention round trigger: %w", err)
	}
	rc.trigger = trigger
	return rc, nil
}

func upsertChannelAttentionContributionOffersTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_attention_contribution_offer (
		  round_id, participant_id, agent_id, offer_source, value_type, summary, evidence_refs
		)
		SELECT round_id, id, agent_id, 'attention_decision',
		       CASE WHEN value_type = 'none' THEN 'unique_evidence' ELSE value_type END,
		       summary, evidence_refs
		FROM channel_attention_participant
		WHERE round_id = $1
		  AND status = 'decided'
		  AND decision = 'CONTRIBUTE'
		  AND value_type IS NOT NULL
		  AND value_type <> 'none'
		ON CONFLICT (round_id, agent_id, offer_source)
		DO UPDATE SET
		  participant_id = EXCLUDED.participant_id,
		  value_type = EXCLUDED.value_type,
		  summary = EXCLUDED.summary,
		  evidence_refs = EXCLUDED.evidence_refs,
		  updated_at = now()`, roundID)
	return err
}

func (h *Handler) grantChannelAttentionResponderTx(ctx context.Context, tx pgx.Tx, rc channelAttentionRoundContext, roundID, agentID pgtype.UUID, grantType, reason string) ([]channelAttentionWake, error) {
	agent, err := h.Queries.WithTx(tx).GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: rc.workspaceID})
	if err != nil {
		return nil, err
	}
	prompt, err := h.buildChannelAttentionGrantPromptTx(ctx, tx, roundID, rc, false, reason)
	if err != nil {
		return nil, err
	}
	result, err := h.enqueueChannelAgentPromptRangeWithTx(ctx, h.Queries.WithTx(tx), tx, rc.channel, agent, rc.trigger, pgtype.UUID{}, prompt, channelAttentionResponseGrantReason, 10, rc.seqFrom, rc.seqTo)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_attention_response_grant (round_id, agent_id, inbox_event_id, grant_type, reason)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (round_id) DO NOTHING`, roundID, agentID, result.Event.ID, grantType, reason); err != nil {
		return nil, err
	}
	if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
		WorkspaceID: rc.workspaceID, ChannelID: rc.channelID, SourceKind: "response_grant", SourceID: roundID,
		EventType: "response_grant_created", AgentID: agentID, InboxEventID: result.Event.ID,
		Payload: map[string]any{"grant_type": grantType, "reason": reason, "seq_from": rc.seqFrom, "seq_to": rc.seqTo},
	}); err != nil {
		return nil, err
	}
	return []channelAttentionWake{{channel: rc.channel, agent: agent, trigger: rc.trigger, reason: channelAttentionResponseGrantReason, result: result}}, nil
}

func (h *Handler) grantChannelAttentionManagerFallbackTx(ctx context.Context, tx pgx.Tx, rc channelAttentionRoundContext, roundID pgtype.UUID, reason string) ([]channelAttentionWake, error) {
	managerID, ok := h.resolveGroupManagerForChannel(ctx, rc.workspaceID, rc.channelID)
	if !ok {
		return nil, nil
	}
	manager, err := h.Queries.WithTx(tx).GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: managerID, WorkspaceID: rc.workspaceID})
	if err != nil {
		return nil, err
	}
	prompt, err := h.buildChannelAttentionGrantPromptTx(ctx, tx, roundID, rc, true, reason)
	if err != nil {
		return nil, err
	}
	result, err := h.enqueueChannelAgentPromptRangeWithTx(ctx, h.Queries.WithTx(tx), tx, rc.channel, manager, rc.trigger, pgtype.UUID{}, prompt, channelAttentionManagerReason, 10, rc.seqFrom, rc.seqTo)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_attention_response_grant (round_id, agent_id, inbox_event_id, grant_type, reason)
		VALUES ($1, $2, $3, 'manager_fallback', $4)
		ON CONFLICT (round_id) DO NOTHING`, roundID, managerID, result.Event.ID, reason); err != nil {
		return nil, err
	}
	if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
		WorkspaceID: rc.workspaceID, ChannelID: rc.channelID, SourceKind: "response_grant", SourceID: roundID,
		EventType: "manager_fallback_grant_created", AgentID: managerID, InboxEventID: result.Event.ID,
		Payload: map[string]any{"grant_type": "manager_fallback", "reason": reason, "seq_from": rc.seqFrom, "seq_to": rc.seqTo},
	}); err != nil {
		return nil, err
	}
	return []channelAttentionWake{{channel: rc.channel, agent: manager, trigger: rc.trigger, reason: channelAttentionManagerReason, result: result}}, nil
}

func (h *Handler) createChannelAttentionConvergenceTurnsTx(ctx context.Context, tx pgx.Tx, rc channelAttentionRoundContext, roundID pgtype.UUID) error {
	type candidate struct {
		participantID pgtype.UUID
		agentID       pgtype.UUID
	}
	rows, err := tx.Query(ctx, `
		SELECT id, agent_id
		FROM channel_attention_participant
		WHERE round_id = $1 AND status = 'decided' AND decision = 'ANSWER'
		ORDER BY COALESCE(confidence, 0) DESC, completed_at ASC, agent_id`, roundID)
	if err != nil {
		return err
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.participantID, &item.agentID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range candidates {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channel_attention_convergence_vote WHERE round_id = $1 AND agent_id = $2)`, roundID, item.agentID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		agent, err := h.Queries.WithTx(tx).GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: item.agentID, WorkspaceID: rc.workspaceID})
		if err != nil {
			return err
		}
		prompt, err := h.buildChannelAttentionConvergencePromptTx(ctx, tx, roundID, rc)
		if err != nil {
			return err
		}
		eventID, err := h.enqueueChannelAttentionProtocolTurnTx(ctx, tx, rc, agent, prompt, channelAttentionConvergenceReason)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_attention_convergence_vote (round_id, participant_id, agent_id, inbox_event_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (round_id, agent_id) DO NOTHING`, roundID, item.participantID, item.agentID, eventID); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) enqueueChannelAttentionProtocolTurnTx(ctx context.Context, tx pgx.Tx, rc channelAttentionRoundContext, agent db.Agent, prompt, reason string) (pgtype.UUID, error) {
	qtx := h.Queries.WithTx(tx)
	session, err := h.ensureChannelAgentSessionWithDB(ctx, qtx, tx, rc.channel, agent.ID, pgtype.UUID{})
	if err != nil {
		return pgtype.UUID{}, err
	}
	promptMsg, err := h.createChannelAgentPromptMessageWithDB(ctx, tx, session.ID, prompt, rc.channel.Kind, rc.trigger)
	if err != nil {
		return pgtype.UUID{}, err
	}
	conversationID, err := h.channelConversationIDWithDB(ctx, tx, rc.channelID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	agentSession, err := qtx.UpsertAgentSession(ctx, db.UpsertAgentSessionParams{
		WorkspaceID:    rc.workspaceID,
		AgentID:        agent.ID,
		ConversationID: conversationID,
		Scope:          channelAgentSessionScope(rc.channel.Kind),
		ChannelID:      rc.channelID,
		ChatSessionID:  session.ID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	var eventID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, agent_session_id, conversation_id, channel_id, chat_session_id,
		  agent_id, runtime_id, execution_config, source_message_id, reason,
		  delivery_mode, response_mode, requires_wake, status, priority, seq_from, seq_to
		)
		SELECT $1, $2, $3, $4, $5, agent.id, agent.runtime_id,
		       jsonb_build_object('execution_config', jsonb_build_object(
		         'model', COALESCE(agent.model, ''),
		         'thinking_level', COALESCE(agent.thinking_level, ''),
		         'execution_profile', $8,
		         'context_messages', 8,
		         'memory_budget_bytes', 4096,
		         'max_output_tokens', 96,
		         'tools_enabled', false,
		         'snapshotted', true
		       )),
		       $6, $7, 'attention', 'convergence_vote', true, 'pending', 2, $9, $10
		FROM agent
		WHERE agent.id = $11
		RETURNING id`, rc.workspaceID, agentSession.ID, conversationID, rc.channelID, session.ID,
		channelAmbientTriggerID(rc.trigger), reason, service.ExecutionProfileProtocolTurn, rc.seqFrom, rc.seqTo, agent.ID).Scan(&eventID); err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE chat_message SET task_id = $1 WHERE id = $2`, eventID, promptMsg.ID); err != nil {
		return pgtype.UUID{}, err
	}
	return eventID, nil
}

func (h *Handler) buildChannelAttentionGrantPromptTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID, rc channelAttentionRoundContext, manager bool, reason string) (string, error) {
	bundle, err := channelAttentionMessageBundleTx(ctx, tx, rc.channelID, rc.seqFrom, rc.seqTo)
	if err != nil {
		return "", err
	}
	decisions, err := channelAttentionDecisionLinesTx(ctx, tx, roundID)
	if err != nil {
		return "", err
	}
	offers, err := channelAttentionOfferLinesTx(ctx, tx, roundID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if manager {
		b.WriteString("Attention round could not auto-select a single responder. You are the group manager fallback and have public_response authorization. Resolve the conflict or coordinate next steps.\n")
	} else {
		b.WriteString("You received the response_grant for this unmentioned group message. You are the only agent authorized to post a visible reply for this round.\n")
	}
	b.WriteString("Reason: " + reason + "\n")
	b.WriteString("Message bundle:\n" + bundle + "\n")
	if decisions != "" {
		b.WriteString("Attention decisions:\n" + decisions + "\n")
	}
	if offers != "" {
		b.WriteString("Internal contribution offers to consider:\n" + offers + "\n")
	}
	b.WriteString("Use the channel output contract. If a visible answer is no longer useful, finish without visible output; otherwise answer once and incorporate useful offers without naming this internal protocol.\n")
	return b.String(), nil
}

func (h *Handler) buildChannelAttentionConvergencePromptTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID, rc channelAttentionRoundContext) (string, error) {
	bundle, err := channelAttentionMessageBundleTx(ctx, tx, rc.channelID, rc.seqFrom, rc.seqTo)
	if err != nil {
		return "", err
	}
	answers, err := channelAttentionAnswerLinesTx(ctx, tx, roundID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("This is a restricted protocol_turn for an Attention Round with multiple ANSWER claims. Do not send a public message.\n")
	b.WriteString("Message bundle:\n" + bundle + "\n")
	b.WriteString("ANSWER claims:\n" + answers + "\n")
	b.WriteString("Return exactly one JSON object with fields vote, target_agent_id, summary. vote must be one of YIELD, KEEP, MERGE, REQUEST_MANAGER. Use KEEP only if you should be the sole public responder. Use MERGE to let another responder incorporate your key point; set target_agent_id when known, otherwise empty string. Use REQUEST_MANAGER only for irreducible coordination.\n")
	return b.String(), nil
}

func channelAttentionMessageBundleTx(ctx context.Context, tx pgx.Tx, channelID pgtype.UUID, seqFrom, seqTo int64) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE channel_id = $1 AND seq BETWEEN $2 AND $3
		ORDER BY seq ASC`, channelID, seqFrom, seqTo)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		msg, err := scanChannelMessage(rows)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("[seq:%d message:%s] %s", msg.Seq, msg.ID, formatChannelMessageLine(msg)))
	}
	return strings.Join(lines, "\n"), rows.Err()
}

func channelAttentionDecisionLinesTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT agent_id::text, decision, COALESCE(confidence, 0), COALESCE(value_type, ''), summary
		FROM channel_attention_participant
		WHERE round_id = $1 AND status = 'decided' AND decision <> 'SILENT'
		ORDER BY decision, COALESCE(confidence, 0) DESC, agent_id`, roundID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var agentID, decision, valueType, summary string
		var confidence float64
		if err := rows.Scan(&agentID, &decision, &confidence, &valueType, &summary); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("- agent:%s decision:%s confidence:%.2f value:%s summary:%s", agentID, decision, confidence, valueType, summary))
	}
	return strings.Join(lines, "\n"), rows.Err()
}

func channelAttentionAnswerLinesTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT agent_id::text, COALESCE(confidence, 0), summary
		FROM channel_attention_participant
		WHERE round_id = $1 AND status = 'decided' AND decision = 'ANSWER'
		ORDER BY COALESCE(confidence, 0) DESC, completed_at ASC, agent_id`, roundID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var agentID, summary string
		var confidence float64
		if err := rows.Scan(&agentID, &confidence, &summary); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("- agent:%s confidence:%.2f summary:%s", agentID, confidence, summary))
	}
	return strings.Join(lines, "\n"), rows.Err()
}

func channelAttentionOfferLinesTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT agent_id::text, value_type, summary, evidence_refs::text
		FROM channel_attention_contribution_offer
		WHERE round_id = $1 AND status = 'pending'
		ORDER BY created_at ASC, agent_id`, roundID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var agentID, valueType, summary, evidence string
		if err := rows.Scan(&agentID, &valueType, &summary, &evidence); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("- agent:%s value:%s summary:%s evidence:%s", agentID, valueType, summary, evidence))
	}
	return strings.Join(lines, "\n"), rows.Err()
}

func settleChannelAttentionRoundTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) (bool, error) {
	var expected, terminal, decided int32
	err := tx.QueryRow(ctx, `
		SELECT round.expected_agent_count,
		       count(*) FILTER (WHERE participant.status IN ('decided', 'failed', 'timed_out', 'unavailable'))::int,
		       count(*) FILTER (WHERE participant.status = 'decided')::int
		FROM channel_attention_round round
		LEFT JOIN channel_attention_participant participant ON participant.round_id = round.id
		WHERE round.id = $1
		GROUP BY round.expected_agent_count`, roundID).Scan(&expected, &terminal, &decided)
	if err != nil {
		return false, err
	}
	resolved := terminal >= expected
	result, err := tx.Exec(ctx, `
		UPDATE channel_attention_round
		SET completed_agent_count = $2,
		    status = CASE WHEN NOT $3 THEN 'resolving' WHEN $4 = 0 THEN 'failed' ELSE 'resolved' END,
		    resolved_at = CASE WHEN $3 THEN COALESCE(resolved_at, now()) ELSE resolved_at END,
		    updated_at = now()
		WHERE id = $1 AND status IN ('collecting', 'resolving')`, roundID, terminal, resolved, decided)
	if err != nil {
		return false, err
	}
	return resolved && result.RowsAffected() == 1, nil
}

func channelAttentionRoundOutcomeTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) (string, error) {
	var expected, decided, unsuccessful int32
	err := tx.QueryRow(ctx, `
		SELECT round.expected_agent_count,
		       count(*) FILTER (WHERE participant.status = 'decided')::int,
		       count(*) FILTER (WHERE participant.status IN ('failed', 'timed_out', 'unavailable'))::int
		FROM channel_attention_round round
		LEFT JOIN channel_attention_participant participant ON participant.round_id = round.id
		WHERE round.id = $1
		GROUP BY round.expected_agent_count`, roundID).Scan(&expected, &decided, &unsuccessful)
	if err != nil {
		return "", err
	}
	if decided == 0 {
		return "failed", nil
	}
	if unsuccessful > 0 || decided < expected {
		return "partial", nil
	}
	return "completed", nil
}

func isChannelAttentionProtocolTurnInboxEvent(event db.AgentInboxEvent) bool {
	config, ok := service.TaskExecutionConfigFromContext(event.ExecutionConfig)
	return ok && config.ExecutionProfile == service.ExecutionProfileProtocolTurn
}

func (h *Handler) completeChannelAttentionConvergenceVoteTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent, executionID pgtype.UUID, vote channelAttentionConvergenceVote) (channelAttentionCompletion, error) {
	var voteID, roundID, agentID pgtype.UUID
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT id, round_id, agent_id, status
		FROM channel_attention_convergence_vote
		WHERE inbox_event_id = $1
		FOR UPDATE`, event.ID).Scan(&voteID, &roundID, &agentID, &status); err != nil {
		return channelAttentionCompletion{}, err
	}
	if status != "pending" {
		return channelAttentionCompletion{}, fmt.Errorf("attention convergence vote is %s", status)
	}

	var inputTokens, outputTokens int64
	var actualModel pgtype.Text
	if err := tx.QueryRow(ctx, `
		SELECT execution.id,
		       COALESCE(sum(usage.input_tokens), 0)::bigint,
		       COALESCE(sum(usage.output_tokens), 0)::bigint,
		       max(NULLIF(usage.model, ''))
		FROM agent_execution execution
		LEFT JOIN agent_usage usage ON usage.execution_id = execution.id
		WHERE execution.source_kind = 'inbox'
		  AND execution.source_event_id = $1
		  AND execution.id = $2
		GROUP BY execution.id`, event.ID, executionID).Scan(&executionID, &inputTokens, &outputTokens, &actualModel); err != nil {
		return channelAttentionCompletion{}, err
	}
	modelVersion := ""
	if actualModel.Valid {
		modelVersion = strings.TrimSpace(actualModel.String)
	}
	if modelVersion == "" {
		return channelAttentionCompletion{}, errors.New("attention convergence execution usage model is missing")
	}
	var targetAgentID pgtype.UUID
	if strings.TrimSpace(vote.TargetAgentID) != "" {
		targetAgentID = parseUUID(vote.TargetAgentID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_attention_convergence_vote
		SET status = 'completed', vote = $2, target_agent_id = $3, summary = $4,
		    input_tokens = $5, output_tokens = $6, model_version = $7,
		    completed_at = now(), updated_at = now()
		WHERE id = $1`, voteID, vote.Vote, nullableUUID(targetAgentID), vote.Summary, inputTokens, outputTokens, modelVersion); err != nil {
		return channelAttentionCompletion{}, err
	}

	wakes, err := h.resolveChannelAttentionConvergenceVotesTx(ctx, tx, roundID)
	if err != nil {
		return channelAttentionCompletion{}, err
	}
	return channelAttentionCompletion{
		decision: vote.Vote, inputTokens: inputTokens, outputTokens: outputTokens,
		roundResolved: len(wakes) > 0, roundOutcome: "converged", wakes: wakes,
	}, nil
}

func (h *Handler) resolveChannelAttentionConvergenceVotesTx(ctx context.Context, tx pgx.Tx, roundID pgtype.UUID) ([]channelAttentionWake, error) {
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM channel_attention_convergence_vote WHERE round_id = $1 AND status = 'pending'`, roundID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending > 0 {
		return nil, nil
	}
	var existingGrant bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channel_attention_response_grant WHERE round_id = $1)`, roundID).Scan(&existingGrant); err != nil {
		return nil, err
	}
	if existingGrant {
		return nil, nil
	}
	rc, err := h.channelAttentionRoundContextTx(ctx, tx, roundID)
	if err != nil {
		return nil, err
	}
	var managerRequested bool
	var keepCount int
	var keepAgentID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE vote = 'REQUEST_MANAGER') > 0,
		       count(*) FILTER (WHERE vote = 'KEEP')::int,
		       COALESCE((array_agg(agent_id ORDER BY completed_at ASC) FILTER (WHERE vote = 'KEEP'))[1], '00000000-0000-0000-0000-000000000000'::uuid)
		FROM channel_attention_convergence_vote
		WHERE round_id = $1`, roundID).Scan(&managerRequested, &keepCount, &keepAgentID); err != nil {
		return nil, err
	}
	if managerRequested || keepCount > 1 {
		return h.grantChannelAttentionManagerFallbackTx(ctx, tx, rc, roundID, "convergence_conflict")
	}
	if keepCount == 1 {
		return h.grantChannelAttentionResponderTx(ctx, tx, rc, roundID, keepAgentID, "converged", "single KEEP convergence vote")
	}
	// If everyone yielded or merged, pick the strongest original ANSWER and fold
	// MERGE votes into contribution offers for the selected responder.
	var selectedAgentID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT agent_id
		FROM channel_attention_participant
		WHERE round_id = $1 AND status = 'decided' AND decision = 'ANSWER'
		ORDER BY COALESCE(confidence, 0) DESC, completed_at ASC, agent_id ASC
		LIMIT 1`, roundID).Scan(&selectedAgentID); err != nil {
		return nil, err
	}
	if err := upsertChannelAttentionMergeOffersTx(ctx, tx, roundID, selectedAgentID); err != nil {
		return nil, err
	}
	return h.grantChannelAttentionResponderTx(ctx, tx, rc, roundID, selectedAgentID, "converged", "all responders yielded or merged")
}

func upsertChannelAttentionMergeOffersTx(ctx context.Context, tx pgx.Tx, roundID, selectedAgentID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO channel_attention_contribution_offer (
		  round_id, participant_id, agent_id, offer_source, value_type, summary, evidence_refs
		)
		SELECT vote.round_id, vote.participant_id, vote.agent_id, 'convergence_merge',
		       COALESCE(participant.value_type, 'direct_answer'), vote.summary,
		       COALESCE(participant.evidence_refs, '[]'::jsonb)
		FROM channel_attention_convergence_vote vote
		LEFT JOIN channel_attention_participant participant ON participant.id = vote.participant_id
		WHERE vote.round_id = $1
		  AND vote.agent_id <> $2
		  AND vote.vote = 'MERGE'
		ON CONFLICT (round_id, agent_id, offer_source)
		DO UPDATE SET
		  participant_id = EXCLUDED.participant_id,
		  value_type = EXCLUDED.value_type,
		  summary = EXCLUDED.summary,
		  evidence_refs = EXCLUDED.evidence_refs,
		  updated_at = now()`, roundID, selectedAgentID)
	return err
}

func failChannelAttentionParticipantTx(ctx context.Context, tx pgx.Tx, eventID pgtype.UUID, errText string) (string, error) {
	var roundID pgtype.UUID
	err := tx.QueryRow(ctx, `
		UPDATE channel_attention_participant
		SET status = 'failed', last_error = $2, completed_at = now(), updated_at = now()
		WHERE inbox_event_id = $1 AND status IN ('pending', 'running')
		RETURNING round_id`, eventID, errText).Scan(&roundID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	resolved, err := settleChannelAttentionRoundTx(ctx, tx, roundID)
	if err != nil || !resolved {
		return "", err
	}
	return channelAttentionRoundOutcomeTx(ctx, tx, roundID)
}

// SweepChannelAttentionTimeouts terminates overdue rounds and their active
// leases. No decision from a late completion can become public or overwrite
// the timeout because participant and round rows are locked in this transition.
func (h *Handler) SweepChannelAttentionTimeouts(ctx context.Context, limit int) int {
	if h == nil || h.TxStarter == nil {
		return 0
	}
	if limit <= 0 || limit > channelAttentionOutboxBatchSize {
		limit = channelAttentionOutboxBatchSize
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("channel attention timeout sweep begin failed", "error", err)
		return 0
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('channel_attention_lifecycle'))`); err != nil {
		slog.Warn("channel attention timeout lifecycle lock failed", "error", err)
		return 0
	}
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM channel_attention_round
		WHERE status IN ('collecting', 'resolving') AND deadline_at <= now()
		ORDER BY deadline_at, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		slog.Warn("channel attention timeout scan failed", "error", err)
		return 0
	}
	var roundIDs []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) == nil {
			roundIDs = append(roundIDs, id)
		}
	}
	rows.Close()
	if len(roundIDs) == 0 {
		_ = tx.Commit(ctx)
		return 0
	}
	timedOutParticipants := int64(0)
	capacityRounds := 0
	cap := h.cfg.ChannelAttentionMaxConcurrentPerRuntime
	if cap <= 0 {
		cap = 16
	}
	for _, roundID := range roundIDs {
		// Reconstruct capacity pressure from durable running participants as well
		// as denied drain attempts. This keeps timeouts attributable even when a
		// saturated daemon does not issue another drain before the deadline.
		if _, err := tx.Exec(ctx, `
			UPDATE channel_attention_participant pending
			SET last_error = 'runtime_capacity', updated_at = now()
			FROM agent_inbox_event pending_event
			WHERE pending.round_id = $1
			  AND pending.status = 'pending'
			  AND pending.inbox_event_id = pending_event.id
			  AND pending_event.runtime_id IS NOT NULL
			  AND (
			    SELECT count(*)
			    FROM channel_attention_participant running
			    JOIN agent_inbox_event running_event ON running_event.id = running.inbox_event_id
			    WHERE running.status = 'running'
			      AND running_event.runtime_id = pending_event.runtime_id
			  ) >= $2`, roundID, cap); err != nil {
			return 0
		}
		var capacityBlocked bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM channel_attention_participant
			  WHERE round_id = $1 AND status = 'pending' AND last_error = 'runtime_capacity'
			)`, roundID).Scan(&capacityBlocked); err != nil {
			return 0
		}
		if capacityBlocked {
			capacityRounds++
		}
		result, err := tx.Exec(ctx, `
			UPDATE channel_attention_participant
			SET status = 'timed_out',
			    last_error = CASE WHEN last_error = 'runtime_capacity' THEN 'runtime_capacity' ELSE 'max_wait' END,
			    completed_at = now(), updated_at = now()
			WHERE round_id = $1 AND status IN ('pending', 'running')`, roundID)
		if err != nil {
			slog.Warn("channel attention timeout participant update failed", "round_id", uuidToString(roundID), "error", err)
			return 0
		}
		timedOutParticipants += result.RowsAffected()
		if _, err := tx.Exec(ctx, `
			UPDATE agent_event_delivery delivery
			SET status = 'expired', last_error = 'attention round deadline exceeded', updated_at = now()
			FROM channel_attention_participant participant
			WHERE participant.round_id = $1
			  AND participant.inbox_event_id = delivery.inbox_event_id
			  AND delivery.status IN ('leased', 'processing')`, roundID); err != nil {
			return 0
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_inbox_event event
			SET status = 'suppressed', last_error = 'attention round deadline exceeded',
			    terminal_outcome = 'no_reply', retryable = false,
			    terminal_at = now(), updated_at = now()
			FROM channel_attention_participant participant
			WHERE participant.round_id = $1
			  AND participant.inbox_event_id = event.id
			  AND event.status IN ('pending', 'failed', 'draining')`, roundID); err != nil {
			return 0
		}
		if _, err := tx.Exec(ctx, `
			UPDATE channel_attention_round round
			SET status = 'timed_out',
			    completed_agent_count = (
			      SELECT count(*) FROM channel_attention_participant participant
			      WHERE participant.round_id = round.id
			        AND participant.status IN ('decided', 'failed', 'timed_out', 'unavailable')
			    ),
			    resolved_at = now(), updated_at = now()
			WHERE round.id = $1`, roundID); err != nil {
			return 0
		}
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("channel attention timeout commit failed", "error", err)
		return 0
	}
	if h.Metrics != nil {
		for range roundIDs {
			h.Metrics.RecordChannelAttentionRound("timed_out")
		}
		for range capacityRounds {
			h.Metrics.RecordChannelAttentionTimeout("runtime_capacity")
		}
		for range len(roundIDs) - capacityRounds {
			h.Metrics.RecordChannelAttentionTimeout("max_wait")
		}
		for i := int64(0); i < timedOutParticipants; i++ {
			h.Metrics.RecordChannelAttentionProbe("none", "timed_out")
		}
	}
	return len(roundIDs)
}

func (h *Handler) channelAgentMembersWithDB(ctx context.Context, exec db.DBTX, workspaceID, channelID string) ([]db.Agent, error) {
	rows, err := exec.Query(ctx, `
		SELECT a.id, a.workspace_id, a.name, a.avatar_url, a.runtime_mode, a.runtime_config, a.visibility, a.status,
		       a.max_concurrent_tasks, a.owner_id, a.created_at, a.updated_at, a.description, a.runtime_id,
		       a.instructions, a.archived_at, a.display_name
		FROM channel_member cm
		JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id
		WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND a.archived_at IS NULL
		ORDER BY a.id`, parseUUID(channelID), parseUUID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Agent
	for rows.Next() {
		var agent db.Agent
		if err := rows.Scan(&agent.ID, &agent.WorkspaceID, &agent.Name, &agent.AvatarUrl, &agent.RuntimeMode, &agent.RuntimeConfig, &agent.Visibility, &agent.Status, &agent.MaxConcurrentTasks, &agent.OwnerID, &agent.CreatedAt, &agent.UpdatedAt, &agent.Description, &agent.RuntimeID, &agent.Instructions, &agent.ArchivedAt, &agent.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, rows.Err()
}

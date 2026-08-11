package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const channelOnboardingReason = protocol.ChannelOnboardingReason

var errChannelOnboardingExpired = errors.New("channel onboarding membership generation is no longer active")
var errChannelOnboardingDecisionRequired = errors.New("channel onboarding requires an explicit send or typed skip receipt")

type channelOnboardingRecord struct {
	ID                     pgtype.UUID
	WorkspaceID            pgtype.UUID
	ChannelID              pgtype.UUID
	AgentID                pgtype.UUID
	MembershipGenerationID pgtype.UUID
	SystemMessageID        pgtype.UUID
	SystemMessageSeq       int64
	SourceType             string
	SourceActorType        string
	SourceActorID          pgtype.UUID
	ChannelName            string
	ChannelDescription     pgtype.Text
	ChannelSystemKey       pgtype.Text
}

func (h *Handler) materializeNextChannelOnboardingForRuntime(ctx context.Context, runtime db.AgentRuntime) error {
	if h.TxStarter == nil {
		return errors.New("channel onboarding transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin channel onboarding transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := expireInvalidChannelOnboardingsForRuntimeTx(ctx, tx, runtime.ID); err != nil {
		return fmt.Errorf("expire invalid channel onboarding: %w", err)
	}

	var onboarding channelOnboardingRecord
	err = tx.QueryRow(ctx, `
		SELECT onboarding.id,
		       onboarding.workspace_id,
		       onboarding.channel_id,
		       onboarding.agent_id,
		       onboarding.membership_generation_id,
		       onboarding.system_message_id,
		       system_message.seq,
		       onboarding.source_type,
		       onboarding.source_actor_type,
		       onboarding.source_actor_id,
		       channel_row.name,
		       channel_row.description,
		       channel_row.system_key
		FROM channel_agent_onboarding onboarding
		JOIN agent agent_row
		  ON agent_row.id = onboarding.agent_id
		 AND agent_row.workspace_id = onboarding.workspace_id
		 AND agent_row.runtime_id = $1
		 AND agent_row.archived_at IS NULL
		JOIN channel channel_row
		  ON channel_row.id = onboarding.channel_id
		 AND channel_row.workspace_id = onboarding.workspace_id
		 AND channel_row.kind = 'group'
		 AND channel_row.archived_at IS NULL
		JOIN channel_member membership
		  ON membership.channel_id = onboarding.channel_id
		 AND membership.workspace_id = onboarding.workspace_id
		 AND membership.member_type = 'agent'
		 AND membership.member_id = onboarding.agent_id
		 AND membership.generation_id = onboarding.membership_generation_id
		JOIN channel_message system_message
		  ON system_message.id = onboarding.system_message_id
		 AND system_message.channel_id = onboarding.channel_id
		 AND system_message.workspace_id = onboarding.workspace_id
		 AND system_message.membership_generation_id = onboarding.membership_generation_id
		 AND system_message.author_type = 'system'
		WHERE onboarding.status IN ('pending', 'claimed')
		  AND onboarding.publication_status = 'published'
		  AND NOT EXISTS (
		    SELECT 1
		    FROM agent_inbox_event inbox
		    WHERE inbox.channel_onboarding_id = onboarding.id
		  )
		ORDER BY onboarding.created_at, onboarding.id
		LIMIT 1
		FOR UPDATE OF onboarding, agent_row, channel_row, membership SKIP LOCKED`, runtime.ID).Scan(
		&onboarding.ID,
		&onboarding.WorkspaceID,
		&onboarding.ChannelID,
		&onboarding.AgentID,
		&onboarding.MembershipGenerationID,
		&onboarding.SystemMessageID,
		&onboarding.SystemMessageSeq,
		&onboarding.SourceType,
		&onboarding.SourceActorType,
		&onboarding.SourceActorID,
		&onboarding.ChannelName,
		&onboarding.ChannelDescription,
		&onboarding.ChannelSystemKey,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tx.Commit(ctx)
		}
		return fmt.Errorf("select channel onboarding: %w", err)
	}

	qtx := h.Queries.WithTx(tx)
	agentRow, err := qtx.GetAgent(ctx, onboarding.AgentID)
	if err != nil {
		return fmt.Errorf("load channel onboarding agent: %w", err)
	}
	channel := ChannelResponse{
		ID:          uuidToString(onboarding.ChannelID),
		WorkspaceID: uuidToString(onboarding.WorkspaceID),
		Name:        onboarding.ChannelName,
		Description: textToPtr(onboarding.ChannelDescription),
		Kind:        "group",
		SystemKey:   textToPtr(onboarding.ChannelSystemKey),
	}
	conversationID, err := h.channelConversationIDWithDB(ctx, tx, onboarding.ChannelID)
	if err != nil {
		return fmt.Errorf("load channel onboarding conversation: %w", err)
	}
	agentSession, err := qtx.UpsertAgentSession(ctx, db.UpsertAgentSessionParams{
		WorkspaceID:    onboarding.WorkspaceID,
		AgentID:        onboarding.AgentID,
		ConversationID: conversationID,
		ChannelID:      onboarding.ChannelID,
		Scope:          "channel",
	})
	if err != nil {
		return fmt.Errorf("upsert channel onboarding agent session: %w", err)
	}

	prompt := h.buildChannelOnboardingPrompt(ctx, onboarding, channel, agentRow)
	wakeContext, err := buildChannelWakeContext(channel, ChannelMessageResponse{}, prompt)
	if err != nil {
		return fmt.Errorf("encode channel onboarding prompt: %w", err)
	}
	var inboxEventID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, agent_session_id, conversation_id, channel_id,
		  agent_id, runtime_id, execution_config, context, source_message_id, reason,
		  delivery_mode, response_mode, requires_wake, status, priority, seq_from, seq_to,
		  channel_onboarding_id
		)
		SELECT $1, $2, $3, $4, agent.id, agent.runtime_id,
		       jsonb_build_object(
		         'model', COALESCE(agent.model, ''),
		         'thinking_level', COALESCE(agent.thinking_level, ''),
		         'snapshotted', true
		       ),
		       $6::jsonb, $7, 'channel_onboarding', 'execute', 'public_response', true,
		       'pending', 0, $8, $8, $9
		FROM agent
		WHERE agent.id = $5
		ON CONFLICT (channel_onboarding_id) DO NOTHING
		RETURNING id`, onboarding.WorkspaceID, agentSession.ID, conversationID,
		onboarding.ChannelID, onboarding.AgentID, string(wakeContext), onboarding.SystemMessageID,
		onboarding.SystemMessageSeq, onboarding.ID).Scan(&inboxEventID)
	if err != nil {
		return fmt.Errorf("create channel onboarding inbox event: %w", err)
	}
	return tx.Commit(ctx)
}

type channelOnboardingPublication struct {
	OnboardingID    pgtype.UUID
	WorkspaceID     pgtype.UUID
	ChannelID       pgtype.UUID
	SystemMessageID pgtype.UUID
}

type channelOnboardingTransportBinding struct {
	ID                     pgtype.UUID
	ChannelID              pgtype.UUID
	AgentID                pgtype.UUID
	MembershipGenerationID pgtype.UUID
	InboxEventID           pgtype.UUID
}

func channelOnboardingClientMessageID(onboardingID pgtype.UUID) string {
	return "channel-onboarding:" + uuidToString(onboardingID)
}

// channelOnboardingForClientMessage recognizes the product-scoped id emitted
// in an onboarding brief. It deliberately does not use a task, inbox, or
// lease header: regular Agent chat remains credential-authenticated.
func channelOnboardingForClientMessage(ctx context.Context, exec dbExecutor, source agentTransportSource, clientMessageID string) (channelOnboardingTransportBinding, bool, error) {
	const prefix = "channel-onboarding:"
	value := strings.TrimSpace(clientMessageID)
	if !strings.HasPrefix(value, prefix) {
		return channelOnboardingTransportBinding{}, false, nil
	}
	onboardingID := parseUUID(strings.TrimPrefix(value, prefix))
	if !onboardingID.Valid {
		return channelOnboardingTransportBinding{}, true, errChannelOnboardingExpired
	}
	var binding channelOnboardingTransportBinding
	err := exec.QueryRow(ctx, `
		SELECT onboarding.id, onboarding.channel_id, onboarding.agent_id,
		       onboarding.membership_generation_id, event.id
		FROM channel_agent_onboarding onboarding
		LEFT JOIN agent_inbox_event event
		  ON event.channel_onboarding_id = onboarding.id
		 AND event.reason = 'channel_onboarding'
		WHERE onboarding.id = $1
		  AND onboarding.workspace_id = $2
		  AND onboarding.agent_id = $3`,
		onboardingID, source.origin.workspaceID, source.origin.agentID).Scan(
		&binding.ID, &binding.ChannelID, &binding.AgentID, &binding.MembershipGenerationID, &binding.InboxEventID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return channelOnboardingTransportBinding{}, true, errChannelOnboardingExpired
		}
		return channelOnboardingTransportBinding{}, true, err
	}
	return binding, true, nil
}

func channelOnboardingTargetMatches(binding channelOnboardingTransportBinding, target agentTransportTarget) bool {
	return target.channel.Kind == "group" &&
		parseUUID(target.channel.ID) == binding.ChannelID &&
		!target.threadRootMessageID.Valid
}

func (h *Handler) requireActiveChannelOnboardingBeforeTarget(ctx context.Context, binding channelOnboardingTransportBinding) error {
	if h.TxStarter == nil {
		return errors.New("channel onboarding transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	active, err := channelOnboardingGenerationActiveTx(ctx, tx, binding.ID, binding.ChannelID, binding.AgentID, true)
	if err != nil {
		return err
	}
	if !active {
		if err := expireChannelOnboardingForInboxEventTx(ctx, tx, binding); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errChannelOnboardingExpired
	}
	return tx.Commit(ctx)
}

func expireChannelOnboardingForInboxEventTx(ctx context.Context, tx pgx.Tx, binding channelOnboardingTransportBinding) error {
	if binding.InboxEventID.Valid {
		if _, err := tx.Exec(ctx, `
			UPDATE agent_event_delivery
			SET status = 'expired',
			    last_error = 'channel onboarding membership generation is no longer active',
			    updated_at = now()
			WHERE inbox_event_id = $1
			  AND status IN ('leased', 'processing')`, binding.InboxEventID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_inbox_event
			SET status = 'suppressed',
			    terminal_outcome = 'expired',
			    retryable = FALSE,
			    terminal_at = now(),
			    last_error = 'channel onboarding membership generation is no longer active',
			    updated_at = now()
			WHERE id = $1
			  AND status IN ('pending', 'draining', 'failed')`, binding.InboxEventID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE channel_agent_onboarding
		SET status = 'expired',
		    terminal_evidence = jsonb_build_object('reason', 'membership_generation_inactive'),
		    terminal_at = now(),
		    updated_at = now()
		WHERE id = $1
		  AND status IN ('pending', 'claimed')`, binding.ID)
	return err
}

func (h *Handler) PublishPendingChannelOnboardingSystemMessages(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 1
	}
	published := 0
	for published < limit {
		didPublish, err := h.publishOneChannelOnboardingSystemMessage(ctx, pgtype.UUID{})
		if err != nil {
			return published, err
		}
		if !didPublish {
			return published, nil
		}
		published++
	}
	return published, nil
}

func (h *Handler) publishChannelOnboardingSystemMessageForGeneration(ctx context.Context, generationID pgtype.UUID) error {
	_, err := h.publishOneChannelOnboardingSystemMessage(ctx, generationID)
	return err
}

func (h *Handler) publishOneChannelOnboardingSystemMessage(ctx context.Context, generationID pgtype.UUID) (bool, error) {
	if h.TxStarter == nil {
		return false, errors.New("channel onboarding publication transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	publication, err := h.claimChannelOnboardingPublication(ctx, tx, generationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if err := h.publishClaimedChannelOnboardingSystemMessage(ctx, tx, publication); err != nil {
		if resetErr := resetChannelOnboardingPublicationAfterFailure(ctx, tx, publication.OnboardingID); resetErr != nil {
			return false, errors.Join(err, resetErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return false, errors.Join(err, fmt.Errorf("commit failed onboarding publication reset: %w", commitErr))
		}
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// claimChannelOnboardingPublication is the sole publication claim boundary.
// Both the synchronous post-membership fast path and the background retry
// worker therefore enforce the same active channel/membership/agent/status
// eligibility before transitioning a row to publishing.
func (h *Handler) claimChannelOnboardingPublication(ctx context.Context, executor dbExecutor, generationID pgtype.UUID) (channelOnboardingPublication, error) {
	var publication channelOnboardingPublication
	err := executor.QueryRow(ctx, `
		WITH candidate AS (
		  SELECT onboarding.id
		  FROM channel_agent_onboarding onboarding
		  JOIN channel channel_row
		    ON channel_row.id = onboarding.channel_id
		   AND channel_row.workspace_id = onboarding.workspace_id
		   AND channel_row.kind = 'group'
		   AND channel_row.archived_at IS NULL
		  JOIN channel_member membership
		    ON membership.channel_id = onboarding.channel_id
		   AND membership.workspace_id = onboarding.workspace_id
		   AND membership.member_type = 'agent'
		   AND membership.member_id = onboarding.agent_id
		   AND membership.generation_id = onboarding.membership_generation_id
		  JOIN agent agent_row
		    ON agent_row.id = onboarding.agent_id
		   AND agent_row.workspace_id = onboarding.workspace_id
		   AND agent_row.archived_at IS NULL
		  WHERE (NOT $1::boolean OR onboarding.membership_generation_id = $2::uuid)
		    AND onboarding.status IN ('pending', 'claimed')
		    AND (
		      onboarding.publication_status = 'pending'
		      OR (
		        onboarding.publication_status = 'publishing'
		        AND onboarding.publication_lease_expires_at <= now()
		      )
		    )
		  ORDER BY onboarding.created_at, onboarding.id
		  LIMIT 1
		  -- Lock the generation row before UPDATE takes the onboarding row lock.
		  -- DELETE uses the same membership -> onboarding order through the
		  -- migration trigger, avoiding a lock-order inversion under a true race.
		  FOR UPDATE OF membership, channel_row, agent_row SKIP LOCKED
		)
		UPDATE channel_agent_onboarding onboarding
		SET publication_status = 'publishing',
		    publication_attempt = onboarding.publication_attempt + 1,
		    publication_lease_expires_at = now() + interval '30 seconds',
		    updated_at = now()
		FROM candidate
		WHERE onboarding.id = candidate.id
		RETURNING onboarding.id, onboarding.workspace_id, onboarding.channel_id,
		          onboarding.system_message_id`, generationID.Valid, generationID).Scan(
		&publication.OnboardingID,
		&publication.WorkspaceID,
		&publication.ChannelID,
		&publication.SystemMessageID,
	)
	return publication, err
}

func (h *Handler) publishClaimedChannelOnboardingSystemMessage(ctx context.Context, executor dbExecutor, publication channelOnboardingPublication) error {
	row := executor.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE id = $1
		  AND channel_id = $2
		  AND workspace_id = $3
		  AND author_type = 'system'`, publication.SystemMessageID, publication.ChannelID, publication.WorkspaceID)
	message, err := scanChannelMessage(row)
	if err != nil {
		return err
	}
	if err := h.publishChannelToMembersWithID(ctx, protocol.EventChannelMessage, uuidToString(publication.WorkspaceID), "system", "", publication.ChannelID, message, uuidToString(publication.SystemMessageID)); err != nil {
		return err
	}
	tag, err := executor.Exec(ctx, `
		UPDATE channel_agent_onboarding
		SET publication_status = 'published',
		    publication_lease_expires_at = NULL,
		    published_at = COALESCE(published_at, now()),
		    updated_at = now()
		WHERE id = $1
		  AND publication_status = 'publishing'`, publication.OnboardingID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("channel onboarding publication lease is no longer active")
	}
	return nil
}

func resetChannelOnboardingPublicationAfterFailure(ctx context.Context, executor dbExecutor, onboardingID pgtype.UUID) error {
	tag, err := executor.Exec(ctx, `
		UPDATE channel_agent_onboarding
		SET publication_status = 'pending',
		    publication_lease_expires_at = NULL,
		    updated_at = now()
		WHERE id = $1
		  AND publication_status = 'publishing'`, onboardingID)
	if err != nil {
		return fmt.Errorf("reset failed onboarding publication: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("failed onboarding publication lease is no longer active")
	}
	return nil
}

func expireInvalidChannelOnboardingsForRuntimeTx(ctx context.Context, tx pgx.Tx, runtimeID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `
		WITH invalid AS (
		  SELECT onboarding.id
		  FROM channel_agent_onboarding onboarding
		  JOIN agent runtime_agent
		    ON runtime_agent.id = onboarding.agent_id
		   AND runtime_agent.runtime_id = $1
		  WHERE onboarding.status IN ('pending', 'claimed')
		    AND NOT EXISTS (
		      SELECT 1
		      FROM channel_member membership
		      JOIN channel channel_row
		        ON channel_row.id = membership.channel_id
		       AND channel_row.workspace_id = membership.workspace_id
		       AND channel_row.kind = 'group'
		       AND channel_row.archived_at IS NULL
		      JOIN agent active_agent
		        ON active_agent.id = membership.member_id
		       AND active_agent.workspace_id = membership.workspace_id
		       AND active_agent.archived_at IS NULL
		      WHERE membership.channel_id = onboarding.channel_id
		        AND membership.workspace_id = onboarding.workspace_id
		        AND membership.member_type = 'agent'
		        AND membership.member_id = onboarding.agent_id
		        AND membership.generation_id = onboarding.membership_generation_id
		    )
		  FOR UPDATE OF onboarding
		),
		expired_delivery AS (
		  UPDATE agent_event_delivery delivery
		  SET status = 'expired',
		      last_error = 'channel onboarding membership generation is no longer active',
		      updated_at = now()
		  FROM agent_inbox_event inbox, invalid
		  WHERE inbox.channel_onboarding_id = invalid.id
		    AND delivery.inbox_event_id = inbox.id
		    AND delivery.status IN ('leased', 'processing')
		  RETURNING delivery.id
		),
		expired_inbox AS (
		  UPDATE agent_inbox_event inbox
		  SET status = 'suppressed',
		      terminal_outcome = 'expired',
		      retryable = FALSE,
		      terminal_at = now(),
		      last_error = 'channel onboarding membership generation is no longer active',
		      updated_at = now()
		  FROM invalid
		  WHERE inbox.channel_onboarding_id = invalid.id
		    AND inbox.status IN ('pending', 'draining', 'failed')
		  RETURNING inbox.id
		)
		UPDATE channel_agent_onboarding onboarding
		SET status = 'expired',
		    terminal_evidence = jsonb_build_object('reason', 'membership_generation_inactive'),
		    terminal_at = now(),
		    updated_at = now()
		FROM invalid
		WHERE onboarding.id = invalid.id`, runtimeID)
	return err
}

func (h *Handler) buildChannelOnboardingPrompt(ctx context.Context, onboarding channelOnboardingRecord, channel ChannelResponse, agentRow db.Agent) string {
	actor := "system"
	if onboarding.SourceActorType != channelMemberActorSystem && onboarding.SourceActorID.Valid {
		ref := h.channelMemberSystemEventActorRef(
			ctx,
			channel.WorkspaceID,
			onboarding.SourceActorType,
			onboarding.SourceActorID,
		)
		if ref.Type == "" {
			actor = fmt.Sprintf(
				"%s (id %s)",
				onboarding.SourceActorType,
				uuidToString(onboarding.SourceActorID),
			)
		} else {
			actor = fmt.Sprintf(
				"%s (@%s, id %s)",
				ref.DisplayName,
				ref.Handle,
				uuidToString(onboarding.SourceActorID),
			)
		}
	}
	systemKey := "none"
	if onboarding.ChannelSystemKey.Valid {
		systemKey = onboarding.ChannelSystemKey.String
	}
	description := strings.TrimSpace(onboarding.ChannelDescription.String)
	if description == "" {
		description = "none"
	}

	var b strings.Builder
	b.WriteString("You have one channel-onboarding decision to make as yourself. This is not a must-reply mention.\n")
	fmt.Fprintf(&b, "Onboarding event id: %s\n", uuidToString(onboarding.ID))
	fmt.Fprintf(&b, "Membership generation id: %s\n", uuidToString(onboarding.MembershipGenerationID))
	fmt.Fprintf(&b, "Exact message target: #%s\n", channel.Name)
	fmt.Fprintf(&b, "Channel id: %s\n", channel.ID)
	fmt.Fprintf(&b, "Channel name: %s\n", channel.Name)
	fmt.Fprintf(&b, "Channel description: %s\n", description)
	fmt.Fprintf(&b, "Channel system_key: %s\n", systemKey)
	fmt.Fprintf(&b, "Join source: %s\n", onboarding.SourceType)
	fmt.Fprintf(&b, "Join actor: %s\n", actor)
	fmt.Fprintf(&b, "Your agent name: %s\n", agentDisplayName(agentRow))
	fmt.Fprintf(&b, "Your agent id: %s\n", uuidToString(onboarding.AgentID))
	if strings.TrimSpace(agentRow.Description) != "" {
		fmt.Fprintf(&b, "Your role/profile: %s\n", strings.TrimSpace(agentRow.Description))
	}
	if strings.TrimSpace(agentRow.Instructions) != "" {
		fmt.Fprintf(&b, "Your agent instructions: %s\n", strings.TrimSpace(agentRow.Instructions))
	}
	b.WriteString("\nDecision contract:\n")
	b.WriteString("- Introduce yourself or acknowledge useful team context only when that adds natural value in this channel.\n")
	fmt.Fprintf(&b, "- For a low-noise, announcement-only, or already-familiar context, skip explicitly by making your entire final response exactly `%s`. The runtime consumes this typed receipt; it is never shown in the channel.\n", protocol.ChannelOnboardingSkipReceipt)
	fmt.Fprintf(&b, "- If you send, use the runtime brief's canonical message-send action to the exact target above with client_message_id `%s`. Send at most one concise message; this idempotently binds the Message to this onboarding decision.\n", channelOnboardingClientMessageID(onboarding.ID))
	b.WriteString("- Never rely on ordinary final/completion text for visible output; only an explicit message-send action is visible.\n")
	b.WriteString("- Do not invent a backend-authored greeting, @-mention unrelated people, assign work, or start a welcome loop.\n")

	messages := h.recentChannelMessages(ctx, channel.WorkspaceID, channel.ID, 8)
	if len(messages) > 0 {
		b.WriteString("\nBounded recent channel context:\n")
		for _, message := range messages {
			fmt.Fprintf(&b, "%s\n", formatChannelMessageLine(message))
		}
	}
	return b.String()
}

func (h *Handler) completeChannelOnboardingTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent, deliveryID, onboardingID pgtype.UUID, active bool, decision string) (string, error) {
	outcome := ""
	if !active {
		outcome = "expired"
	} else if h.channelOnboardingHasCanonicalMessageTx(ctx, tx, event, onboardingID) {
		outcome = "sent"
	} else if decision == protocol.ChannelOnboardingDecisionSkipped {
		outcome = "skipped"
	} else {
		return "", errChannelOnboardingDecisionRequired
	}
	evidence, err := json.Marshal(map[string]any{
		"inbox_event_id": uuidToString(event.ID),
		"delivery_id":    uuidToString(deliveryID),
		"outcome":        outcome,
	})
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_agent_onboarding
		SET status = $2,
		    terminal_evidence = $3::jsonb,
		    terminal_at = now(),
		    updated_at = now()
		WHERE id = $1
		  AND status IN ('pending', 'claimed')`, onboardingID, outcome, evidence); err != nil {
		return "", err
	}
	return outcome, nil
}

func (h *Handler) channelOnboardingHasCanonicalMessageTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent, onboardingID pgtype.UUID) bool {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message
			WHERE workspace_id = $1
			  AND channel_id = $2
			  AND author_type = 'agent'
			  AND author_id = $3
			  AND client_message_id = $4
			  AND deleted_at IS NULL
		)`,
		event.WorkspaceID,
		event.ChannelID,
		event.AgentID,
		channelOnboardingClientMessageID(onboardingID),
	).Scan(&exists)
	return err == nil && exists
}

func channelOnboardingIDForInboxEventTx(ctx context.Context, tx pgx.Tx, eventID pgtype.UUID) (pgtype.UUID, error) {
	var onboardingID pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT channel_onboarding_id
		FROM agent_inbox_event
		WHERE id = $1
		  AND reason = 'channel_onboarding'`, eventID).Scan(&onboardingID)
	return onboardingID, err
}

func channelOnboardingGenerationActiveTx(ctx context.Context, tx pgx.Tx, onboardingID, channelID, agentID pgtype.UUID, lock bool) (bool, error) {
	if lock {
		return lockChannelOnboardingGenerationActiveTx(ctx, tx, onboardingID, channelID, agentID)
	}
	query := `
		SELECT 1
		FROM channel_agent_onboarding onboarding
		JOIN channel_member membership
		  ON membership.channel_id = onboarding.channel_id
		 AND membership.workspace_id = onboarding.workspace_id
		 AND membership.member_type = 'agent'
		 AND membership.member_id = onboarding.agent_id
		 AND membership.generation_id = onboarding.membership_generation_id
		JOIN channel channel_row
		  ON channel_row.id = onboarding.channel_id
		 AND channel_row.workspace_id = onboarding.workspace_id
		 AND channel_row.kind = 'group'
		 AND channel_row.archived_at IS NULL
		JOIN agent agent_row
		  ON agent_row.id = onboarding.agent_id
		 AND agent_row.workspace_id = onboarding.workspace_id
		 AND agent_row.archived_at IS NULL
		WHERE onboarding.id = $1
		  AND onboarding.channel_id = $2
		  AND onboarding.agent_id = $3
		  AND onboarding.status IN ('pending', 'claimed')`
	var one int
	err := tx.QueryRow(ctx, query, onboardingID, channelID, agentID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func lockChannelOnboardingGenerationActiveTx(ctx context.Context, tx pgx.Tx, onboardingID, channelID, agentID pgtype.UUID) (bool, error) {
	// Read immutable generation coordinates without taking a row lock, then lock
	// every eligibility row in the same global order as reminder fire:
	// channel -> agent -> membership -> onboarding. The membership-before-
	// onboarding suffix also matches the channel_member DELETE trigger, which
	// owns the deleted membership row before expiring its onboarding generation.
	// Revalidate the onboarding row last so a terminal transition that raced the
	// coordinate read cannot be mistaken for an active generation.
	var workspaceID, generationID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT workspace_id, membership_generation_id
		FROM channel_agent_onboarding
		WHERE id = $1 AND channel_id = $2 AND agent_id = $3`, onboardingID, channelID, agentID).Scan(&workspaceID, &generationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`SELECT 1 FROM channel
			WHERE id = $1 AND workspace_id = $2 AND kind = 'group' AND archived_at IS NULL
			FOR UPDATE`, []any{channelID, workspaceID}},
		{`SELECT 1 FROM agent
			WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL
			FOR UPDATE`, []any{agentID, workspaceID}},
		{`SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2
			  AND member_type = 'agent' AND member_id = $3 AND generation_id = $4
			FOR UPDATE`, []any{channelID, workspaceID, agentID, generationID}},
		{`SELECT 1 FROM channel_agent_onboarding
			WHERE id = $1 AND workspace_id = $2 AND channel_id = $3 AND agent_id = $4
			  AND membership_generation_id = $5 AND status IN ('pending', 'claimed')
			FOR UPDATE`, []any{onboardingID, workspaceID, channelID, agentID, generationID}},
	} {
		var one int
		if err := tx.QueryRow(ctx, query.sql, query.args...).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

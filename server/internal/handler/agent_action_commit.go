package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/messageparts"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// codedActionCommitError carries an HTTP status + business code for the
// CreateAgent action-message commit path so the caller can respond with the
// right status (403/404/409/500) without a huge switch at the call site.
type codedActionCommitError struct {
	status int
	code   string
	msg    string
}

func (e *codedActionCommitError) Error() string { return e.msg }

func actionCommitError(status int, code, msg string) error {
	return &codedActionCommitError{status: status, code: code, msg: msg}
}

// agentActionMessageMissingCode is returned when an action_message_id refers to
// a Message the committer cannot see or that does not exist, using object
// invisible semantics so the response does not leak whether a private Message
// exists (LRM-2343 story 21).
const agentActionMessageMissingCode = "agent_action_message_not_found"

// agentActionMessageNotPrepared is the business code for an action that is no
// longer prepared (already executed with different content, or concurrently
// committed).
const agentActionMessageNotPrepared = "agent_action_not_prepared"

// createAgentFromActionMessage performs the canonical, atomic, idempotent commit
// of a prepared agent:create proposal Message (LRM-2343 S2). It runs in a single
// DB transaction: FOR UPDATE lock + prepared->executed CAS, Agent creation,
// system #general membership and commit snapshots. A successful confirmation
// is terminal: every later attempt returns 409, regardless of whether its final
// input matches. The separately stored summary is non-sensitive (stories 28-29).
func (h *Handler) createAgentFromActionMessage(ctx context.Context, wsUUID, committerID, actionMessageID pgtype.UUID, createParams db.CreateAgentParams, displayName string) (db.Agent, error) {
	if !actionMessageID.Valid {
		return db.Agent{}, actionCommitError(404, agentActionMessageMissingCode, "action message not found")
	}
	// Load the action state + proposal snapshot first (non-locking) so a visibly
	// missing message yields an object-invisible 404 before we open a write tx.
	preExisting, exists, err := h.loadAgentActionForCommit(ctx, wsUUID, actionMessageID)
	if err != nil {
		return db.Agent{}, err
	}
	if !exists {
		return db.Agent{}, actionCommitError(404, agentActionMessageMissingCode, "action message not found")
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.Agent{}, actionCommitError(500, "", "failed to begin agent commit transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status      string
		finalHash   pgtype.Text
		resultAgent pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT status, final_payload_hash, result_agent_id
		FROM agent_action
		WHERE channel_message_id = $1 AND workspace_id = $2
		FOR UPDATE`,
		actionMessageID, wsUUID,
	).Scan(&status, &finalHash, &resultAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, actionCommitError(404, agentActionMessageMissingCode, "action message not found")
	}
	if err != nil {
		return db.Agent{}, err
	}

	// Visibility: committer must still be able to read the Message (its channel),
	// so a leaked/invisible id cannot commit into a private channel/DM/thread.
	if !h.committerCanSeeChannelMessage(ctx, tx, wsUUID, actionMessageID, committerID) {
		return db.Agent{}, actionCommitError(404, agentActionMessageMissingCode, "action message not found")
	}
	if strings.TrimSpace(createParams.DisplayName) == "" {
		createParams.DisplayName = displayName
	}

	switch status {
	case agentActionStatusExecuted:
		return db.Agent{}, actionCommitError(409, agentActionMessageNotPrepared, "action message is already committed")
	case agentActionStatusPrepared:
		// proceed below
	default:
		return db.Agent{}, actionCommitError(500, "", "unknown action state")
	}

	// prepared -> create Agent + #general membership + durable first-start
	// intent + snapshots in one tx.
	qtx := h.Queries.WithTx(tx)
	if strings.TrimSpace(createParams.Name) == "" {
		return db.Agent{}, errIdentityHandleInvalid
	}
	createParams.DisplayName = firstNonEmpty(createParams.DisplayName, createParams.Name)
	created, err := qtx.CreateAgent(ctx, createParams)
	if err != nil {
		return db.Agent{}, err
	}

	if err := ensureAgentGeneralMembership(ctx, tx, wsUUID, created.ID); err != nil {
		return db.Agent{}, err
	}
	if _, err := ensureAgentDurableStartIntent(ctx, tx, wsUUID, created.ID, createParams.RuntimeID); err != nil {
		return db.Agent{}, err
	}

	finalPayload := agentActionFinalPayload(createParams, preExisting.proposed)
	finalPayloadRaw, err := json.Marshal(finalPayload)
	if err != nil {
		return db.Agent{}, err
	}
	// Keep the persisted audit record non-sensitive, while the idempotency key
	// still covers the complete final configuration. Replays compare against
	// agentActionFinalPayloadHash below, so storing the summary hash here would
	// make an otherwise identical replay look like a conflicting commit.
	hash := agentActionFinalPayloadHash(createParams, preExisting.proposed)
	tag, err := tx.Exec(ctx, `
		UPDATE agent_action
		SET status = 'executed',
		    final_payload_hash = $3,
		    final_payload = $4::jsonb,
		    committer_user_id = $5,
		    result_agent_id = $6,
		    executed_at = now(),
		    updated_at = now()
		WHERE channel_message_id = $1 AND workspace_id = $2 AND status = 'prepared'`,
		actionMessageID, wsUUID, hash, finalPayloadRaw, committerID, created.ID)
	if err != nil {
		return db.Agent{}, err
	}
	if tag.RowsAffected() == 0 {
		// Another commit won the CAS. Re-check idempotency against the winner.
		var finalHash2 pgtype.Text
		var resultAgent2 pgtype.UUID
		_ = tx.QueryRow(ctx, `
			SELECT final_payload_hash, result_agent_id FROM agent_action
			WHERE channel_message_id = $1 AND workspace_id = $2`,
			actionMessageID, wsUUID).Scan(&finalHash2, &resultAgent2)
		if finalHash2.Valid && finalHash2.String == hash && resultAgent2.Valid {
			if existing, gerr := h.Queries.GetAgent(ctx, resultAgent2); gerr == nil {
				_ = tx.Commit(ctx)
				return existing, nil
			}
		}
		return db.Agent{}, actionCommitError(409, agentActionMessageNotPrepared, "action message was concurrently committed")
	}
	if err := updateAgentActionMessagePartTx(ctx, tx, actionMessageID, committerID, created.ID); err != nil {
		return db.Agent{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return db.Agent{}, err
	}
	return created, nil
}

// updateAgentActionMessagePartTx updates only the public, non-sensitive
// Proposal part. Runtime/model/reasoning configuration stays out of the
// Message; viewers see the commit result through the standard Message read
// path without another action-card resource.
func updateAgentActionMessagePartTx(ctx context.Context, tx pgx.Tx, actionMessageID, committerID, resultAgentID pgtype.UUID) error {
	var rawParts []byte
	if err := tx.QueryRow(ctx, `SELECT parts FROM channel_message WHERE id = $1 FOR UPDATE`, actionMessageID).Scan(&rawParts); err != nil {
		return err
	}
	parts := messageparts.Decode(rawParts)
	found := false
	for i := range parts {
		if parts[i].Type != protocol.MessagePartTypeReference || parts[i].RefType != agentActionTypeCreate {
			continue
		}
		params := map[string]any{}
		if len(parts[i].Params) > 0 {
			if err := json.Unmarshal(parts[i].Params, &params); err != nil {
				return err
			}
		}
		params["status"] = agentActionStatusExecuted
		params["committer_user_id"] = uuidToString(committerID)
		params["result_agent_id"] = uuidToString(resultAgentID)
		updated, err := json.Marshal(params)
		if err != nil {
			return err
		}
		parts[i].Params = updated
		found = true
	}
	if !found {
		return actionCommitError(500, "", "action message has no agent:create proposal part")
	}
	_, err := tx.Exec(ctx, `UPDATE channel_message SET parts = $2::jsonb, edited_at = now() WHERE id = $1`, actionMessageID, messageparts.MustJSON(parts))
	return err
}

// publishAgentActionMessageUpdated is a post-commit effect. It keeps the
// canonical timeline query fresh without treating the realtime payload as a
// second long-lived state source.
func (h *Handler) publishAgentActionMessageUpdated(ctx context.Context, wsUUID, actionMessageID, committerID pgtype.UUID) {
	msg, err := scanChannelMessage(h.DB.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message WHERE id = $1 AND workspace_id = $2`, actionMessageID, wsUUID))
	if err != nil {
		slog.Warn("load committed agent:create proposal message", "message_id", uuidToString(actionMessageID), "error", err)
		return
	}
	h.publishChannelToMembers(ctx, protocol.EventChannelMessageUpdated, msg.WorkspaceID, "member", uuidToString(committerID), parseUUID(msg.ChannelID), msg)
}

// ensureAgentDurableStartIntent records the first-start request in the same
// transaction as the Agent identity. A later dispatcher may retry delivery,
// but it must always use this original start_dispatch_id.
type agentStartIntentQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ensureAgentDurableStartIntent(ctx context.Context, queryer agentStartIntentQueryer, wsUUID, agentID, runtimeID pgtype.UUID) (pgtype.UUID, error) {
	var dispatchID pgtype.UUID
	if !runtimeID.Valid {
		return dispatchID, nil
	}
	err := queryer.QueryRow(ctx, `
		INSERT INTO agent_start_intent (
			start_dispatch_id, agent_id, workspace_id, runtime_id, status
		) VALUES (gen_random_uuid(), $1, $2, $3, 'pending')
		ON CONFLICT (agent_id) DO NOTHING
		RETURNING start_dispatch_id
	`, agentID, wsUUID, runtimeID).Scan(&dispatchID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return dispatchID, err
	}
	return dispatchID, nil
}

// createAgentManagedTx is the single transaction-scoped Agent creation path:
// identity, #general membership, and first-start intent are one atomic unit.
// Callers may add role-specific records in the same transaction, but must not
// duplicate the generic Agent creation steps.
func (h *Handler) createAgentManagedTx(ctx context.Context, tx pgx.Tx, qtx *db.Queries, wsUUID pgtype.UUID, params db.CreateAgentParams, displayName string) (db.Agent, error) {
	var created db.Agent
	var err error
	if strings.TrimSpace(params.Name) != "" {
		params.DisplayName = firstNonEmpty(params.DisplayName, params.Name)
		created, err = qtx.CreateAgent(ctx, params)
	} else {
		created, err = h.createAgentWithIdentityTx(ctx, tx, qtx, params, displayName, displayName)
	}
	if err != nil {
		return db.Agent{}, err
	}
	if err := ensureAgentGeneralMembership(ctx, tx, wsUUID, created.ID); err != nil {
		return db.Agent{}, err
	}
	if _, err := ensureAgentDurableStartIntent(ctx, tx, wsUUID, created.ID, params.RuntimeID); err != nil {
		return db.Agent{}, err
	}
	return created, nil
}

// createAgentManagedCommit gives ordinary human creation and committed
// Proposals the shared atomic Agent creation boundary.
func (h *Handler) createAgentManagedCommit(ctx context.Context, wsUUID pgtype.UUID, params db.CreateAgentParams, displayName string) (db.Agent, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.Agent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	created, err := h.createAgentManagedTx(ctx, tx, h.Queries.WithTx(tx), wsUUID, params, displayName)
	if err != nil {
		return db.Agent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Agent{}, err
	}
	return created, nil
}

// agentActionPreload is the proposal snapshot and current visible state used
// for final-payload-hash idempotency. The preferred Computer is part of the
// proposal but not of the committed runtime config.
type agentActionPreload struct {
	proposed map[string]any
	status   string
}

func (h *Handler) loadAgentActionForCommit(ctx context.Context, wsUUID, actionMessageID pgtype.UUID) (agentActionPreload, bool, error) {
	var proposedRaw []byte
	var status string
	err := h.DB.QueryRow(ctx, `
		SELECT proposed_payload, status FROM agent_action
		WHERE channel_message_id = $1 AND workspace_id = $2`,
		actionMessageID, wsUUID).Scan(&proposedRaw, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentActionPreload{}, false, nil
	}
	if err != nil {
		return agentActionPreload{}, false, err
	}
	var proposed map[string]any
	_ = json.Unmarshal(proposedRaw, &proposed)
	return agentActionPreload{proposed: proposed, status: status}, true, nil
}

// committerCanSeeChannelMessage reports whether the human committer is a member
// of the channel that owns the action Message. DMs and group channels both
// record human membership in channel_member, so this covers DM/group/thread
// visibility in one check.
func (h *Handler) committerCanSeeChannelMessage(ctx context.Context, tx pgx.Tx, wsUUID, actionMessageID, committerID pgtype.UUID) bool {
	var channelID pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT channel_id FROM channel_message
		WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`,
		actionMessageID, wsUUID).Scan(&channelID)
	if err != nil || !channelID.Valid {
		return false
	}
	var n int
	err = tx.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, wsUUID, committerID).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

// ensureAgentGeneralMembership adds the newly created Agent to the workspace's
// system channel identified by the stable system_key='general' (not by display
// name), so identity creation always yields a default collaboration space and is
// part of the same commit transaction (stories 32-34).
func ensureAgentGeneralMembership(ctx context.Context, tx pgx.Tx, wsUUID, agentID pgtype.UUID) error {
	var generalID pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM channel
		WHERE workspace_id = $1 AND system_key = 'general' AND archived_at IS NULL`,
		wsUUID).Scan(&generalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return actionCommitError(500, "", "workspace has no system #general channel")
	}
	if err != nil {
		return err
	}
	if !generalID.Valid {
		return actionCommitError(500, "", "workspace has no system #general channel")
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT (channel_id, member_type, member_id) DO NOTHING`,
		generalID, wsUUID, agentID)
	return err
}

// agentActionFinalPayloadHash returns the SHA-256 (hex) of every final create
// input that can change the resulting Agent. The separately persisted
// final_payload deliberately remains a non-sensitive audit summary, but the
// idempotency discriminator must still include opaque JSON and secret-bearing
// inputs so a replay with different final configuration cannot silently return
// an Agent created with another person's choices.
func agentActionFinalPayloadHash(params db.CreateAgentParams, proposed map[string]any) string {
	hashInput := agentActionFinalPayload(params, proposed)
	hashInput["instructions"] = params.Instructions
	hashInput["runtime_config"] = actionFinalHashJSON(params.RuntimeConfig)
	hashInput["custom_env"] = actionFinalHashJSON(params.CustomEnv)
	hashInput["custom_args"] = actionFinalHashJSON(params.CustomArgs)
	hashInput["mcp_config"] = actionFinalHashJSON(params.McpConfig)
	hashInput["avatar_url"] = params.AvatarUrl.String
	hashInput["avatar_source"] = params.AvatarSource
	return canonicalJSONHash(hashInput)
}

// actionFinalHashJSON keeps the idempotency hash semantic rather than
// formatting-sensitive for JSON fields. If a legacy caller sent malformed raw
// JSON, retain its bytes as a string: the eventual insert is the authoritative
// validator, but a retry must still compare that exact opaque value.
func actionFinalHashJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return string(raw)
}

// agentActionFinalPayload is the non-sensitive audit summary retained with the
// committed Proposal. Credentials, instructions, and opaque runtime settings
// can carry secrets; they participate in the hash above but are never copied
// into this durable proposal audit record or the public Message part.
func agentActionFinalPayload(params db.CreateAgentParams, proposed map[string]any) map[string]any {
	preferredComputer := ""
	if proposed != nil {
		if v, ok := proposed["preferred_computer"].(string); ok {
			preferredComputer = strings.TrimSpace(v)
		}
	}
	final := map[string]any{
		"display_name":         params.DisplayName,
		"name":                 params.Name,
		"description":          params.Description,
		"runtime_id":           uuidToString(params.RuntimeID),
		"runtime_mode":         params.RuntimeMode,
		"model":                params.Model.String,
		"thinking_level":       params.ThinkingLevel.String,
		"max_concurrent_tasks": params.MaxConcurrentTasks,
		"preferred_computer":   preferredComputer,
	}
	return final
}

// canonicalJSONHash produces a SHA-256 (hex) over the canonical JSON of a map,
// ordering object keys deterministically (sorted) so byte-level stability holds
// across marshalling round-trips.
func canonicalJSONHash(v map[string]any) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kBytes, _ := json.Marshal(k)
		vBytes, _ := json.Marshal(v[k])
		sb.Write(kBytes)
		sb.WriteByte(':')
		sb.Write(vBytes)
	}
	sb.WriteByte('}')
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

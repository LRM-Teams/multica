package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
// system #general membership and commit snapshots. Idempotency is keyed on
// action_message_id + the final non-sensitive payload hash: the same Message
// re-committed with the same final payload returns the same Agent; a different
// final payload returns 409 (stories 28-29).
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

	switch status {
	case agentActionStatusExecuted:
		// Idempotent replay: already executed. Same final hash -> same Agent.
		if resultAgent.Valid && finalHash.Valid {
			hash := agentActionFinalPayloadHash(createParams, preExisting.proposed)
			if finalHash.String == hash {
				existing, gerr := h.Queries.GetAgent(ctx, resultAgent)
				if gerr == nil {
					_ = tx.Commit(ctx)
					return existing, nil
				}
				if gerr != pgx.ErrNoRows {
					return db.Agent{}, gerr
				}
			}
			return db.Agent{}, actionCommitError(409, agentActionMessageNotPrepared, "action message already committed with different content")
		}
		return db.Agent{}, actionCommitError(500, "", "action already committed but missing result")
	case agentActionStatusPrepared:
		// proceed below
	default:
		return db.Agent{}, actionCommitError(500, "", "unknown action state")
	}

	// prepared -> create Agent + #general membership + snapshots in one tx.
	qtx := h.Queries.WithTx(tx)
	created, err := h.createAgentWithIdentity(ctx, qtx, createParams, displayName, displayName)
	if err != nil {
		return db.Agent{}, err
	}

	if err := ensureAgentGeneralMembership(ctx, tx, wsUUID, created.ID); err != nil {
		return db.Agent{}, err
	}

	hash := agentActionFinalPayloadHash(createParams, preExisting.proposed)
	tag, err := tx.Exec(ctx, `
		UPDATE agent_action
		SET status = 'executed',
		    final_payload_hash = $3,
		    committer_user_id = $4,
		    result_agent_id = $5,
		    executed_at = now(),
		    updated_at = now()
		WHERE channel_message_id = $1 AND workspace_id = $2 AND status = 'prepared'`,
		actionMessageID, wsUUID, hash, committerID, created.ID)
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

	if err := tx.Commit(ctx); err != nil {
		return db.Agent{}, err
	}
	return created, nil
}

// agentActionPreload is the proposal snapshot used for final-payload-hash
// idempotency (the preferred Computer is part of the proposal but not of the
// committed runtime config).
type agentActionPreload struct{ proposed map[string]any }

func (h *Handler) loadAgentActionForCommit(ctx context.Context, wsUUID, actionMessageID pgtype.UUID) (agentActionPreload, bool, error) {
	var proposedRaw []byte
	err := h.DB.QueryRow(ctx, `
		SELECT proposed_payload FROM agent_action
		WHERE channel_message_id = $1 AND workspace_id = $2`,
		actionMessageID, wsUUID).Scan(&proposedRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentActionPreload{}, false, nil
	}
	if err != nil {
		return agentActionPreload{}, false, err
	}
	var proposed map[string]any
	_ = json.Unmarshal(proposedRaw, &proposed)
	return agentActionPreload{proposed: proposed}, true, nil
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

// agentActionFinalPayloadHash returns the SHA-256 (hex) of the canonical JSON of
// the final, non-sensitive committed configuration used for idempotent replay.
// It deliberately excludes secrets (API keys/credentials are never part of the
// create params hash) and uses a stable canonical form (sorted keys) so that the
// same final content always hashes the same regardless of field order (stories
// 27-29).
func agentActionFinalPayloadHash(params db.CreateAgentParams, proposed map[string]any) string {
	preferredComputer := ""
	if proposed != nil {
		if v, ok := proposed["preferred_computer"].(string); ok {
			preferredComputer = strings.TrimSpace(v)
		}
	}
	final := map[string]any{
		"display_name":        params.DisplayName,
		"name":                params.Name,
		"description":         params.Description,
		"runtime_id":          uuidToString(params.RuntimeID),
		"model":               params.Model.String,
		"thinking_level":      params.ThinkingLevel.String,
		"max_concurrent_tasks": params.MaxConcurrentTasks,
		"preferred_computer":  preferredComputer,
	}
	return canonicalJSONHash(final)
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

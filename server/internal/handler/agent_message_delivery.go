package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The installed Raft Computer v1.0.15 does not expose recovery pagination.
// This is Multica's own bounded internal protocol limit; 200 matches the
// repository's existing bounded Agent event reads.
const agentMessageRecoveryMaxPage = 200

type agentMessageRecoverySnapshot struct {
	Targets map[string]int64 `json:"targets"`
}

type agentMessageRecoveryCursor struct {
	Target       string `json:"target"`
	Seq          int64  `json:"seq"`
	ID           string `json:"id"`
	SnapshotHash string `json:"snapshot_hash"`
	Checksum     string `json:"checksum"`
}

func (h *Handler) HandleAgentDeliveryAck(ctx context.Context, identity daemonws.ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
	if strings.TrimSpace(ack.AgentID) == "" || strings.TrimSpace(ack.DeliveryID) == "" || ack.Seq <= 0 {
		return errors.New("invalid agent delivery acknowledgement")
	}
	return h.requireAgentMessageDaemonScope(ctx, identity, ack.AgentID)
}

func (h *Handler) HandleAgentMessageHandoff(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.AgentMessageHandoffPayload) error {
	if strings.TrimSpace(payload.AgentID) == "" || strings.TrimSpace(payload.RuntimeID) == "" || strings.TrimSpace(payload.HandoffID) == "" || payload.Count <= 0 {
		return errors.New("invalid agent Message handoff")
	}
	if err := h.requireAgentMessageDaemonScope(ctx, identity, payload.AgentID); err != nil {
		return err
	}
	var boundRuntimeID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1 AND workspace_id = $2`, parseUUID(payload.AgentID), parseUUID(identity.WorkspaceID)).Scan(&boundRuntimeID); err != nil {
		return err
	}
	if uuidToString(boundRuntimeID) != payload.RuntimeID {
		return errors.New("agent Message handoff runtime mismatch")
	}
	if h.TxStarter == nil {
		return errors.New("agent Message handoff transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agent_message_handoff'), hashtext($1))`, payload.HandoffID); err != nil {
		return err
	}
	targets, err := json.Marshal(payload.Targets)
	if err != nil {
		return fmt.Errorf("encode Message handoff targets: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_message_handoff_receipt (workspace_id, agent_id, handoff_id, message_count, targets)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, agent_id, handoff_id) DO NOTHING`,
		parseUUID(identity.WorkspaceID), parseUUID(payload.AgentID), payload.HandoffID, payload.Count, targets); err != nil {
		return fmt.Errorf("persist Message handoff receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

// HandleAgentMessageRecovery statelessly reads canonical Messages under a
// target-sequence snapshot fence. The complete boundary map is supplied on
// every page; the Server stores no Agent-facing receive cursor.
func (h *Handler) HandleAgentMessageRecovery(ctx context.Context, identity daemonws.ClientIdentity, request protocol.AgentRecoveryRequest) (protocol.AgentRecoveryPage, error) {
	if strings.TrimSpace(request.RecoveryID) == "" {
		return protocol.AgentRecoveryPage{}, errors.New("recovery id is required")
	}
	slog.Info("agent message recovery request", "agent_id", request.AgentID, "workspace_id", identity.WorkspaceID, "recovery_id", request.RecoveryID, "boundary_count", len(request.Boundaries), "limit", request.Limit, "daemon_id", identity.DaemonID)
	if err := h.requireAgentMessageDaemonScope(ctx, identity, request.AgentID); err != nil {
		slog.Warn("agent message recovery rejected by daemon scope", "agent_id", request.AgentID, "workspace_id", identity.WorkspaceID, "recovery_id", request.RecoveryID, "error", err)
		return protocol.AgentRecoveryPage{}, err
	}
	if err := validateAgentRecoveryBoundaries(request.Boundaries); err != nil {
		slog.Warn("agent message recovery invalid boundaries", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "error", err)
		return protocol.AgentRecoveryPage{}, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > agentMessageRecoveryMaxPage {
		limit = agentMessageRecoveryMaxPage
	}

	snapshot, snapshotID, err := h.resolveAgentRecoverySnapshot(ctx, identity.WorkspaceID, request.AgentID, request.SnapshotID)
	if err != nil {
		slog.Warn("agent message recovery snapshot resolution failed", "agent_id", request.AgentID, "workspace_id", identity.WorkspaceID, "recovery_id", request.RecoveryID, "error", err)
		return protocol.AgentRecoveryPage{}, err
	}
	slog.Info("agent message recovery snapshot resolved", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "target_count", len(snapshot.Targets), "snapshot_id", truncateString(snapshotID, 24))
	cursor, err := decodeAgentRecoveryCursor(request.Cursor, snapshotID)
	if err != nil {
		slog.Warn("agent message recovery cursor decode failed", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "error", err)
		return protocol.AgentRecoveryPage{}, err
	}
	if cursor.Target != "" {
		fence, ok := snapshot.Targets[cursor.Target]
		if !ok || cursor.Seq > fence {
			slog.Warn("agent message recovery cursor exceeds fence", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "target", cursor.Target, "seq", cursor.Seq)
			return protocol.AgentRecoveryPage{}, errors.New("recovery cursor exceeds snapshot fence")
		}
	}
	boundariesJSON, err := json.Marshal(request.Boundaries)
	if err != nil {
		return protocol.AgentRecoveryPage{}, err
	}
	fenceJSON, err := json.Marshal(snapshot.Targets)
	if err != nil {
		return protocol.AgentRecoveryPage{}, err
	}

	rows, err := h.DB.Query(ctx, `
		WITH eligible AS (
			SELECT m.id, delivery.seq, m.content, m.parts, delivery.target,
			       CASE c.kind
			         WHEN 'group' THEN '#' || c.name
			         WHEN 'dm' THEN 'dm:@' || COALESCE(peer.handle, '')
			         ELSE ''
			       END || CASE
			         WHEN m.thread_root_message_id IS NOT NULL THEN ':' || LEFT(m.thread_root_message_id::text, 8)
			         ELSE ''
			       END AS reply_target
			FROM agent_message_delivery delivery
			JOIN channel_message m ON m.id = delivery.message_id
			JOIN channel c ON c.id = m.channel_id AND c.workspace_id = delivery.workspace_id
			LEFT JOIN LATERAL (
				SELECT COALESCE(NULLIF(u.name, ''), NULLIF(a.name, ''), '') AS handle
				FROM channel_member cm
				LEFT JOIN "user" u ON cm.member_type = 'user' AND u.id = cm.member_id
				LEFT JOIN agent a ON cm.member_type = 'agent' AND a.id = cm.member_id AND a.archived_at IS NULL
				WHERE cm.channel_id = c.id
				  AND cm.workspace_id = delivery.workspace_id
				  AND NOT (cm.member_type = 'agent' AND cm.member_id = $2)
				ORDER BY cm.created_at ASC
				LIMIT 1
			) peer ON c.kind = 'dm'
			WHERE delivery.workspace_id = $1
			  AND delivery.agent_id = $2
			  AND m.deleted_at IS NULL
		)
		SELECT id, seq, content, parts, target, reply_target
		FROM eligible
		WHERE seq > COALESCE(($3::jsonb ->> target)::bigint, 0)
		  AND seq <= COALESCE(($4::jsonb ->> target)::bigint, 0)
		  AND (target, seq, id) > ($5, $6, $7)
		ORDER BY target, seq, id
		LIMIT $8`, parseUUID(identity.WorkspaceID), parseUUID(request.AgentID),
		boundariesJSON, fenceJSON, cursor.Target, cursor.Seq, parseUUIDOrZero(cursor.ID), limit+1)
	if err != nil {
		slog.Warn("agent message recovery query failed", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "error", err)
		return protocol.AgentRecoveryPage{}, err
	}
	defer rows.Close()

	items := make([]protocol.AgentMessageProjection, 0, limit+1)
	for rows.Next() {
		var id pgtype.UUID
		var seq int64
		var content, target, replyTarget string
		var rawParts []byte
		if err := rows.Scan(&id, &seq, &content, &rawParts, &target, &replyTarget); err != nil {
			slog.Warn("agent message recovery row scan failed", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "error", err)
			return protocol.AgentRecoveryPage{}, err
		}
		if strings.TrimSpace(replyTarget) == "" {
			slog.Warn("agent message recovery reply target unavailable", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "target", target, "seq", seq)
			return protocol.AgentRecoveryPage{}, errors.New("canonical Message recovery reply target is unavailable")
		}
		var parts []protocol.MessagePart
		if len(rawParts) > 0 && string(rawParts) != "null" {
			if err := json.Unmarshal(rawParts, &parts); err != nil {
				return protocol.AgentRecoveryPage{}, fmt.Errorf("decode canonical Message parts: %w", err)
			}
		}
		items = append(items, protocol.AgentMessageProjection{
			ID: uuidToString(id), Target: target, ReplyTarget: replyTarget, Seq: seq, Content: content, Parts: parts,
		})
	}
	if err := rows.Err(); err != nil {
		slog.Warn("agent message recovery rows iteration failed", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "error", err)
		return protocol.AgentRecoveryPage{}, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor, err = encodeAgentRecoveryCursor(agentMessageRecoveryCursor{Target: last.Target, Seq: last.Seq, ID: last.ID}, snapshotID)
		if err != nil {
			return protocol.AgentRecoveryPage{}, err
		}
	}
	slog.Info("agent message recovery page", "agent_id", request.AgentID, "recovery_id", request.RecoveryID, "messages", len(items), "has_more", hasMore, "next_cursor", truncateString(nextCursor, 24))
	return protocol.AgentRecoveryPage{
		AgentID: request.AgentID, RecoveryID: request.RecoveryID, SnapshotID: snapshotID, HighWatermark: snapshotID,
		Messages: items, NextCursor: nextCursor, HasMore: hasMore,
	}, nil
}

func (h *Handler) resolveAgentRecoverySnapshot(ctx context.Context, workspaceID, agentID, encoded string) (agentMessageRecoverySnapshot, string, error) {
	if encoded != "" {
		snapshot, err := decodeAgentRecoverySnapshot(encoded)
		return snapshot, encoded, err
	}
	rows, err := h.DB.Query(ctx, `
		WITH eligible AS (
			SELECT delivery.seq, delivery.target
			FROM agent_message_delivery delivery
			JOIN channel_message m ON m.id = delivery.message_id
			WHERE delivery.workspace_id = $1
			  AND delivery.agent_id = $2
			  AND m.deleted_at IS NULL
		)
		SELECT target, max(seq)
		FROM eligible
		GROUP BY target`, parseUUID(workspaceID), parseUUID(agentID))
	if err != nil {
		return agentMessageRecoverySnapshot{}, "", err
	}
	defer rows.Close()
	targets := make(map[string]int64)
	for rows.Next() {
		var target string
		var seq int64
		if err := rows.Scan(&target, &seq); err != nil {
			return agentMessageRecoverySnapshot{}, "", err
		}
		targets[target] = seq
	}
	if err := rows.Err(); err != nil {
		return agentMessageRecoverySnapshot{}, "", err
	}
	snapshot := agentMessageRecoverySnapshot{Targets: targets}
	snapshotID, err := encodeAgentRecoverySnapshot(snapshot)
	return snapshot, snapshotID, err
}

func (h *Handler) requireAgentMessageDaemonScope(ctx context.Context, identity daemonws.ClientIdentity, agentID string) error {
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return errors.New("invalid agent id")
	}
	var workspaceID, runtimeID pgtype.UUID
	var daemonID *string
	if err := h.DB.QueryRow(ctx, `
		SELECT agent.workspace_id, agent.runtime_id, runtime.daemon_id
		FROM agent
		LEFT JOIN agent_runtime runtime ON runtime.id = agent.runtime_id
		WHERE agent.id = $1 AND agent.archived_at IS NULL`, agentUUID).Scan(&workspaceID, &runtimeID, &daemonID); err != nil {
		return err
	}
	if uuidToString(workspaceID) != identity.WorkspaceID {
		return errors.New("agent delivery workspace mismatch")
	}
	if daemonID == nil || *daemonID == "" || identity.DaemonID == "" || *daemonID != identity.DaemonID {
		return errors.New("agent delivery daemon mismatch")
	}
	return nil
}

func validateAgentRecoveryBoundaries(boundaries map[string]int64) error {
	for target, seq := range boundaries {
		if strings.TrimSpace(target) == "" || seq < 0 {
			return errors.New("invalid recovery boundaries")
		}
	}
	return nil
}

func encodeAgentRecoverySnapshot(snapshot agentMessageRecoverySnapshot) (string, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeAgentRecoverySnapshot(encoded string) (agentMessageRecoverySnapshot, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return agentMessageRecoverySnapshot{}, errors.New("invalid recovery snapshot")
	}
	var snapshot agentMessageRecoverySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.Targets == nil {
		return agentMessageRecoverySnapshot{}, errors.New("invalid recovery snapshot")
	}
	if err := validateAgentRecoveryBoundaries(snapshot.Targets); err != nil {
		return agentMessageRecoverySnapshot{}, errors.New("invalid recovery snapshot")
	}
	return snapshot, nil
}

func encodeAgentRecoveryCursor(cursor agentMessageRecoveryCursor, snapshotID string) (string, error) {
	cursor.SnapshotHash = fmt.Sprintf("%x", sha256.Sum256([]byte(snapshotID)))
	cursor.Checksum = agentRecoveryCursorChecksum(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeAgentRecoveryCursor(encoded, snapshotID string) (agentMessageRecoveryCursor, error) {
	if encoded == "" {
		return agentMessageRecoveryCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return agentMessageRecoveryCursor{}, errors.New("invalid recovery cursor")
	}
	var cursor agentMessageRecoveryCursor
	wantSnapshotHash := fmt.Sprintf("%x", sha256.Sum256([]byte(snapshotID)))
	if err := json.Unmarshal(raw, &cursor); err != nil || strings.TrimSpace(cursor.Target) == "" || cursor.Seq <= 0 || cursor.ID == "" || cursor.SnapshotHash != wantSnapshotHash || cursor.Checksum != agentRecoveryCursorChecksum(cursor) {
		return agentMessageRecoveryCursor{}, errors.New("invalid recovery cursor")
	}
	if _, err := util.ParseUUID(cursor.ID); err != nil {
		return agentMessageRecoveryCursor{}, errors.New("invalid recovery cursor")
	}
	return cursor, nil
}

func agentRecoveryCursorChecksum(cursor agentMessageRecoveryCursor) string {
	value := cursor.Target + "\x00" + fmt.Sprint(cursor.Seq) + "\x00" + cursor.ID + "\x00" + cursor.SnapshotHash
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func parseUUIDOrZero(value string) pgtype.UUID {
	if value == "" {
		return parseUUID("00000000-0000-0000-0000-000000000000")
	}
	return parseUUID(value)
}

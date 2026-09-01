// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// rewardDeliveryStaleWindow is how long an in-flight outbox delivery may sit
// before a crashed worker's claim is reclaimable (spec §7, A25).
const rewardDeliveryStaleWindow = 2 * time.Minute

// ErrRewardRevisionConflict: a replay of the same reward identity (trajectory,
// kind) under the same judged input manifest carried a different status or
// value. The immutable record is never overwritten (spec 14.4, A48).
var ErrRewardRevisionConflict = errors.New("graph memory reward: revision conflict: same identity judged with a different value")

// TrajectoryRewardRecord is one immutable reward revision (Task 19, spec
// 14.4) as produced by the dive judge path. Value is non-nil only for
// Status "available"; Components carries the raw judged dimensions so the
// ledger never needs re-derivation.
type TrajectoryRewardRecord struct {
	WorkspaceID       pgtype.UUID
	TrajectoryID      pgtype.UUID
	RewardKind        string
	Status            string // available|unavailable|rejected
	Value             *float64
	Components        memorygraph.RewardComponents
	PolicyVersion     string
	InputManifestHash string
}

// RecordTrajectoryRewardTx appends one immutable reward revision on the
// caller's transaction and returns the revision written (or found). The
// trajectory row is locked first so per-trajectory writers serialize. A
// record with the same identity and input manifest hash must replay the
// same status and value — a differing replay is ErrRewardRevisionConflict
// and writes nothing; a different input manifest appends the next revision
// (re-evaluation, spec 14.4: revisions never overwrite consumed history).
func RecordTrajectoryRewardTx(ctx context.Context, tx pgx.Tx, rec TrajectoryRewardRecord) (int, error) {
	if rec.RewardKind == "" {
		return 0, fmt.Errorf("graph memory reward: reward kind required")
	}
	available := rec.Status == "available"
	if available != (rec.Value != nil) {
		return 0, fmt.Errorf("graph memory reward: status %q requires a %s value", rec.Status, map[bool]string{true: "numeric", false: "nil"}[available])
	}
	components, err := json.Marshal(rec.Components)
	if err != nil {
		return 0, fmt.Errorf("graph memory reward: components: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM graph_memory_trajectory WHERE id = $1 FOR UPDATE
	`, rec.TrajectoryID); err != nil {
		return 0, fmt.Errorf("graph memory reward: lock trajectory: %w", err)
	}
	var (
		existingRevision int
		existingStatus   string
		existingValue    *float64
		existingHash     string
	)
	err = tx.QueryRow(ctx, `
		SELECT revision, status, value, input_manifest_hash
		FROM graph_memory_reward_record
		WHERE trajectory_id = $1 AND reward_kind = $2
		ORDER BY revision DESC
		LIMIT 1
	`, rec.TrajectoryID, rec.RewardKind).Scan(&existingRevision, &existingStatus, &existingValue, &existingHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			INSERT INTO graph_memory_reward_record
			  (workspace_id, trajectory_id, reward_kind, revision, status, value, components, policy_version, input_manifest_hash)
			VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8)
		`, rec.WorkspaceID, rec.TrajectoryID, rec.RewardKind, rec.Status, rec.Value, components, rec.PolicyVersion, rec.InputManifestHash); err != nil {
			return 0, fmt.Errorf("graph memory reward: insert revision 1: %w", err)
		}
		return 1, nil
	case err != nil:
		return 0, fmt.Errorf("graph memory reward: load latest revision: %w", err)
	}
	if existingHash == rec.InputManifestHash {
		if existingStatus != rec.Status || !sameRewardValue(existingValue, rec.Value) {
			return existingRevision, fmt.Errorf("%w: trajectory %s kind %s revision %d", ErrRewardRevisionConflict, util.UUIDToString(rec.TrajectoryID), rec.RewardKind, existingRevision)
		}
		return existingRevision, nil // idempotent replay of the same judgement
	}
	revision := existingRevision + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_memory_reward_record
		  (workspace_id, trajectory_id, reward_kind, revision, status, value, components, policy_version, input_manifest_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, rec.WorkspaceID, rec.TrajectoryID, rec.RewardKind, revision, rec.Status, rec.Value, components, rec.PolicyVersion, rec.InputManifestHash); err != nil {
		return 0, fmt.Errorf("graph memory reward: insert revision %d: %w", revision, err)
	}
	return revision, nil
}

// sameRewardValue compares two optional reward values for replay equality.
func sameRewardValue(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// RLSessionOpener mirrors arealrl.Client.StartSession (tests substitute
// fakes).
type RLSessionOpener interface {
	StartSession(ctx context.Context, sessionRef, envID string) (arealrl.SessionCreds, error)
}

// RLSessionRemover mirrors arealrl.Client.RemoveSession - the authoritative
// AReaL-side teardown (v2 has no end_session).
type RLSessionRemover interface {
	RemoveSession(ctx context.Context, sessionID string) error
}

// GraphMemoryRLSessionService owns the durable online-RL session mapping and
// reward outbox for graph-memory Explore trajectories (spec §6/§7, brief
// D13; acceptance A12/A25/A29). It satisfies arealrl.RewardStore so a
// RewardSink drains it directly.
//
// Opening is a fenced intent: a row in status 'opening' with a generation
// counter is persisted before the StartSession RPC; a crash mid-open leaves a
// reconcilable row instead of an ambiguous session. The proxy key is stored
// ONLY here and is cleared exactly when the reward's durable terminal ack is
// recorded (MarkDelivered) - never before. Eventual AReaL-side cleanup is the
// reaper's job (ReapStaleSessions); nothing is exported or removed inline.
type GraphMemoryRLSessionService struct {
	pool    *pgxpool.Pool
	opener  RLSessionOpener
	remover RLSessionRemover
}

// NewGraphMemoryRLSessionService builds the session/outbox service. opener
// and remover may be the same arealrl.Client.
func NewGraphMemoryRLSessionService(pool *pgxpool.Pool, opener RLSessionOpener, remover RLSessionRemover) *GraphMemoryRLSessionService {
	return &GraphMemoryRLSessionService{pool: pool, opener: opener, remover: remover}
}

// OpenForTrajectory returns the trajectory's AReaL session id, opening one
// (exactly once per generation) if none is open. It is idempotent: a row
// already 'open' (or 'rewarded') is reused without another StartSession RPC.
// envID forwards the env binding to the bridge when non-empty.
func (s *GraphMemoryRLSessionService) OpenForTrajectory(ctx context.Context, trajectoryID, envID string) (string, error) {
	traj, err := parseUUIDColumn("trajectory", trajectoryID)
	if err != nil {
		return "", err
	}

	generation, reusable, err := s.beginOpen(ctx, traj)
	if err != nil {
		return "", err
	}
	if reusable.SessionID != "" {
		return reusable.SessionID, nil
	}

	// RPC outside the row transaction: a fenced generation makes a lost race
	// a no-op instead of a duplicate effective session.
	creds, err := s.opener.StartSession(ctx, trajectoryID, envID)
	if err != nil {
		_, _ = s.pool.Exec(ctx, `
			UPDATE graph_memory_rl_session
			SET status = 'failed', last_error = $2, updated_at = now()
			WHERE trajectory_id = $1 AND generation = $3 AND status = 'opening'
		`, traj, err.Error(), generation)
		return "", fmt.Errorf("graph memory rl session: open for trajectory %s: %w", trajectoryID, err)
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_rl_session
		SET status = 'open', session_id = $2, proxy_key = $3, opened_at = now(), updated_at = now()
		WHERE trajectory_id = $1 AND generation = $4 AND status = 'opening'
	`, traj, creds.SessionID, creds.ProxyKey, generation)
	if err != nil {
		return "", fmt.Errorf("graph memory rl session: record open session: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return "", fmt.Errorf(
			"graph memory rl session: open generation %d for trajectory %s was overtaken", generation, trajectoryID)
	}
	return creds.SessionID, nil
}

// reusableOpen is the outcome of beginOpen: either a session id to reuse or
// the fenced generation for a fresh open attempt.
type reusableOpen struct {
	SessionID string
}

// beginOpen persists (or reconciles) the opening intent and returns either a
// reusable open session or the generation to open under.
func (s *GraphMemoryRLSessionService) beginOpen(ctx context.Context, traj pgtype.UUID) (int, reusableOpen, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		rowID  pgtype.UUID
		wsID   pgtype.UUID
		recall pgtype.UUID
		status string
		sessID string
		gen    int
	)
	scanErr := tx.QueryRow(ctx, `
		SELECT id, workspace_id, recall_id, status, session_id, generation
		FROM graph_memory_rl_session WHERE trajectory_id = $1
		FOR UPDATE
	`, traj).Scan(&rowID, &wsID, &recall, &status, &sessID, &gen)
	switch {
	case errors.Is(scanErr, pgx.ErrNoRows):
		var trainingMode string
		if err := tx.QueryRow(ctx, `
			SELECT r.training_mode, t.workspace_id, t.recall_id
			FROM graph_memory_trajectory t
			JOIN graph_memory_recall r ON r.id = t.recall_id
			WHERE t.id = $1
		`, traj).Scan(&trainingMode, &wsID, &recall); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: trajectory %s not found", util.UUIDToString(traj))
			}
			return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: load trajectory: %w", err)
		}
		if trainingMode != "online_rl" {
			return 0, reusableOpen{}, fmt.Errorf(
				"graph memory rl session: trajectory %s recall is %s, not online_rl", util.UUIDToString(traj), trainingMode)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO graph_memory_rl_session (workspace_id, trajectory_id, recall_id, status, generation)
			VALUES ($1, $2, $3, 'opening', 1)
		`, wsID, traj, recall); err != nil {
			return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: insert opening intent: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: commit opening intent: %w", err)
		}
		return 1, reusableOpen{}, nil

	case scanErr != nil:
		return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: load session row: %w", scanErr)
	}

	switch {
	case (status == "open" || status == "rewarded") && sessID != "":
		if err := tx.Commit(ctx); err != nil {
			return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: commit reuse: %w", err)
		}
		return gen, reusableOpen{SessionID: sessID}, nil
	case status == "closed":
		return 0, reusableOpen{}, fmt.Errorf(
			"graph memory rl session: trajectory %s session is closed", util.UUIDToString(traj))
	}
	// 'opening' (crashed mid-open) or 'failed': fence a new generation.
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_rl_session
		SET status = 'opening', generation = generation + 1, session_id = '', proxy_key = '',
		    last_error = '', updated_at = now()
		WHERE id = $1
	`, rowID); err != nil {
		return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: fence new generation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, reusableOpen{}, fmt.Errorf("graph memory rl session: commit generation fence: %w", err)
	}
	return gen + 1, reusableOpen{}, nil
}

// EnqueueReward records one reward revision in the delivery outbox, keyed by
// the delivery identity (trajectory, reward_kind, revision) — replays insert
// nothing (Task 19, spec 14.4). The immutable record for the identity must
// exist with status 'available' and carry exactly the given value: an
// unavailable or unknown reward never enters the outbox (A46: judge failure
// delivers no numeric 0).
func (s *GraphMemoryRLSessionService) EnqueueReward(ctx context.Context, trajectoryID, rewardKind string, revision int, reward float64) error {
	traj, err := parseUUIDColumn("trajectory", trajectoryID)
	if err != nil {
		return err
	}
	var (
		hasSession bool
		valueMatch bool
	)
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  EXISTS (
		    SELECT 1 FROM graph_memory_rl_session
		    WHERE trajectory_id = $1 AND status IN ('open', 'rewarded') AND session_id <> ''
		  ),
		  EXISTS (
		    SELECT 1 FROM graph_memory_reward_record r
		    WHERE r.trajectory_id = $1 AND r.reward_kind = $2 AND r.revision = $3
		      AND r.status = 'available' AND r.value = $4
		  )
	`, traj, rewardKind, revision, reward).Scan(&hasSession, &valueMatch); err != nil {
		return fmt.Errorf("graph memory reward outbox: load session/record: %w", err)
	}
	if !hasSession {
		return fmt.Errorf("graph memory reward outbox: trajectory %s has no open online session", trajectoryID)
	}
	if !valueMatch {
		return fmt.Errorf("graph memory reward outbox: trajectory %s has no available %s reward revision %d matching %v", trajectoryID, rewardKind, revision, reward)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO graph_memory_reward_outbox (workspace_id, trajectory_id, reward_kind, reward_revision, reward)
		SELECT t.workspace_id, t.id, $2, $3, $4 FROM graph_memory_trajectory t WHERE t.id = $1
		ON CONFLICT (trajectory_id, reward_kind, reward_revision) DO NOTHING
	`, traj, rewardKind, revision, reward); err != nil {
		return fmt.Errorf("graph memory reward outbox: enqueue: %w", err)
	}
	return nil
}

// ClaimPending moves due rows to 'delivering' and returns them. In-flight
// rows are reclaimable only after rewardDeliveryStaleWindow (crash recovery);
// terminal rows are never returned.
func (s *GraphMemoryRLSessionService) ClaimPending(ctx context.Context, limit int) ([]arealrl.PendingReward, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("graph memory reward outbox: begin claim: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT o.id, o.trajectory_id, o.reward_kind, o.reward_revision, o.reward, o.attempts, s.proxy_key
		FROM graph_memory_reward_outbox o
		JOIN graph_memory_rl_session s ON s.trajectory_id = o.trajectory_id
		WHERE (o.status = 'pending' AND o.next_attempt_at <= now())
		   OR (o.status = 'delivering' AND o.updated_at < now() - $2::interval)
		ORDER BY o.created_at
		LIMIT $1
		FOR UPDATE OF o SKIP LOCKED
	`, limit, rewardDeliveryStaleWindow.String())
	if err != nil {
		return nil, fmt.Errorf("graph memory reward outbox: select claimable: %w", err)
	}
	defer rows.Close()

	var out []arealrl.PendingReward
	var ids []string
	for rows.Next() {
		var (
			id       pgtype.UUID
			traj     pgtype.UUID
			claim    arealrl.PendingReward
			proxyKey string
		)
		if err := rows.Scan(&id, &traj, &claim.RewardKind, &claim.RewardRevision, &claim.Reward, &claim.Attempts, &proxyKey); err != nil {
			return nil, fmt.Errorf("graph memory reward outbox: scan claimable: %w", err)
		}
		claim.OutboxID = util.UUIDToString(id)
		claim.TrajectoryID = util.UUIDToString(traj)
		claim.ProxyKey = proxyKey
		out = append(out, claim)
		ids = append(ids, claim.OutboxID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph memory reward outbox: iterate claimable: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			UPDATE graph_memory_reward_outbox SET status = 'delivering', updated_at = now()
			WHERE id = $1::uuid AND status IN ('pending', 'delivering')
		`, id); err != nil {
			return nil, fmt.Errorf("graph memory reward outbox: mark delivering: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("graph memory reward outbox: commit claim: %w", err)
	}
	return out, nil
}

// MarkDelivered records the durable terminal ack: the outbox row becomes
// 'delivered' and the session's proxy key is cleared in the same transaction
// (A29 - the key outlives delivery only until this ack exists).
func (s *GraphMemoryRLSessionService) MarkDelivered(ctx context.Context, outboxID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("graph memory reward outbox: begin delivered: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE graph_memory_reward_outbox
		SET status = 'delivered', delivered_at = now(), updated_at = now()
		WHERE id = $1::uuid AND status = 'delivering'
	`, outboxID)
	if err != nil {
		return fmt.Errorf("graph memory reward outbox: mark delivered: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("graph memory reward outbox: outbox row %s is not delivering", outboxID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_rl_session s
		SET status = CASE WHEN s.status = 'open' THEN 'rewarded' ELSE s.status END,
		    proxy_key = '',
		    key_cleared_at = COALESCE(key_cleared_at, now()),
		    updated_at = now()
		WHERE s.trajectory_id = (SELECT trajectory_id FROM graph_memory_reward_outbox WHERE id = $1::uuid)
		  AND NOT EXISTS (
		    SELECT 1 FROM graph_memory_reward_outbox o
		    WHERE o.trajectory_id = s.trajectory_id AND o.status IN ('pending', 'delivering')
		  )
	`, outboxID); err != nil {
		return fmt.Errorf("graph memory reward outbox: clear session key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("graph memory reward outbox: commit delivered: %w", err)
	}
	return nil
}

// MarkRetry requeues an in-flight row after a transient failure.
func (s *GraphMemoryRLSessionService) MarkRetry(ctx context.Context, outboxID string, attempts int, nextAt time.Time, cause error) error {
	return s.markOutbox(ctx, outboxID, `
		UPDATE graph_memory_reward_outbox
		SET status = 'pending', attempts = $2, next_attempt_at = $3, last_error = $4, updated_at = now()
		WHERE id = $1::uuid AND status = 'delivering'
	`, attempts, nextAt, errorText(cause))
}

// MarkFailed terminally fails an in-flight row (non-retryable response or
// attempts exhausted). The session key stays for the reaper's cleanup.
func (s *GraphMemoryRLSessionService) MarkFailed(ctx context.Context, outboxID string, cause error) error {
	return s.markOutbox(ctx, outboxID, `
		UPDATE graph_memory_reward_outbox
		SET status = 'failed', last_error = $2, updated_at = now()
		WHERE id = $1::uuid AND status = 'delivering'
	`, errorText(cause))
}

func (s *GraphMemoryRLSessionService) markOutbox(ctx context.Context, outboxID, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, append([]any{outboxID}, args...)...)
	if err != nil {
		return fmt.Errorf("graph memory reward outbox: update outbox row: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("graph memory reward outbox: outbox row %s is not delivering", outboxID)
	}
	return nil
}

// ReapStaleSessions closes stale sessions: open sessions idle past maxAge,
// rewarded sessions (ack recorded, key already cleared), and failed open
// attempts. Each closed session with an AReaL session id is torn down via
// RemoveSession; a failed teardown leaves the row for the next cycle. It
// returns how many rows were closed.
func (s *GraphMemoryRLSessionService) ReapStaleSessions(ctx context.Context, maxAge time.Duration, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, status
		FROM graph_memory_rl_session
		WHERE (status IN ('open', 'failed') AND updated_at < now() - $1::interval)
		   OR status = 'rewarded'
		ORDER BY updated_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`, maxAge.String(), limit)
	if err != nil {
		return 0, fmt.Errorf("graph memory rl session: select reapable: %w", err)
	}
	defer rows.Close()

	type reapTarget struct {
		id        pgtype.UUID
		sessionID string
		status    string
	}
	var targets []reapTarget
	for rows.Next() {
		var tgt reapTarget
		if err := rows.Scan(&tgt.id, &tgt.sessionID, &tgt.status); err != nil {
			return 0, fmt.Errorf("graph memory rl session: scan reapable: %w", err)
		}
		targets = append(targets, tgt)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("graph memory rl session: iterate reapable: %w", err)
	}
	rows.Close()

	closed := 0
	for _, tgt := range targets {
		if tgt.sessionID != "" {
			if err := s.remover.RemoveSession(ctx, tgt.sessionID); err != nil {
				// Keep the row open so the next cycle retries the teardown.
				continue
			}
		}
		tag, err := s.pool.Exec(ctx, `
			UPDATE graph_memory_rl_session
			SET status = 'closed', closed_at = now(), updated_at = now(),
			    proxy_key = '',
			    key_cleared_at = COALESCE(key_cleared_at, now())
			WHERE id = $1 AND status = $2
		`, tgt.id, tgt.status)
		if err != nil {
			return closed, fmt.Errorf("graph memory rl session: close reaped row: %w", err)
		}
		closed += int(tag.RowsAffected())
	}
	return closed, nil
}

// parseUUIDColumn parses a uuid path/lookup parameter.
func parseUUIDColumn(column, value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, fmt.Errorf("graph memory rl session: invalid %s id %q: %w", column, value, err)
	}
	return id, nil
}

// errorText renders a delivery failure for storage; nil becomes a generic
// message so last_error is never empty.
func errorText(cause error) string {
	if cause == nil {
		return "delivery failed"
	}
	return cause.Error()
}

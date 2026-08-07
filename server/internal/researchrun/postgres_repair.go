package researchrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// recordTargetRepairTx persists the repair decision for a settled execution
// failure inside the same transaction that settles the Attempt. The unique
// repair key makes the record target-idempotent: a recomputed identical
// failure advances the observation counters on the existing decision instead
// of opening a second remediation path. created reports whether this call
// established a new decision.
func recordTargetRepairTx(ctx context.Context, tx pgx.Tx, in AttemptFailure) (TargetRepair, bool, error) {
	var repair TargetRepair
	var targetConfigFingerprint string
	err := tx.QueryRow(ctx, `
		SELECT a.workspace_id::text, a.session_id::text, a.task_id::text,
		       t.goal_version, t.plan_version, a.target_config_fingerprint
		FROM research_task_attempt a
		JOIN research_task t ON t.id = a.task_id
		WHERE a.id = $1::uuid
	`, in.AttemptID).Scan(
		&repair.WorkspaceID, &repair.SessionID, &repair.TaskID,
		&repair.GoalVersion, &repair.PlanVersion, &targetConfigFingerprint,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TargetRepair{}, false, fmt.Errorf("%w: attempt has no repair context", ErrInvalidTransition)
	}
	if err != nil {
		return TargetRepair{}, false, err
	}

	class := FailureClass(in.FailureClass)
	kind, fingerprint, recordable, err := repairDecisionFor(class, in.SourceReason, targetConfigFingerprint)
	if err != nil {
		return TargetRepair{}, false, err
	}
	if !recordable {
		return TargetRepair{}, false, nil
	}

	repair.FailureClass = class
	repair.FailureFingerprint = fingerprint
	repair.RepairKind = kind
	repair.TargetConfigFingerprint = targetConfigFingerprint
	repair.SourceReason = truncateBytes(in.SourceReason, 160)
	repair.Diagnostics = truncateBytes(in.Diagnostics, 4096)
	repair.RepairKey = RepairKeyFor(
		repair.SessionID, repair.TaskID, repair.GoalVersion, repair.PlanVersion, fingerprint, kind,
	)

	var created bool
	err = tx.QueryRow(ctx, `
		INSERT INTO research_target_repair (
		  workspace_id, session_id, task_id, goal_version, plan_version,
		  failure_class, failure_fingerprint, repair_kind, repair_key,
		  source_failure_reason, target_config_fingerprint, diagnostics,
		  first_attempt_id, last_attempt_id
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4, $5,
		  $6, $7, $8, $9,
		  $10, $11, $12,
		  NULLIF($13, '')::uuid, NULLIF($13, '')::uuid
		)
		ON CONFLICT (workspace_id, repair_key) DO UPDATE
		SET occurrence_count = research_target_repair.occurrence_count + 1,
		    last_attempt_id = COALESCE(NULLIF($13, '')::uuid, research_target_repair.last_attempt_id),
		    diagnostics = $12,
		    last_observed_at = now(),
		    updated_at = now()
		RETURNING id::text, occurrence_count, first_attempt_id::text, last_attempt_id::text,
		          first_observed_at, last_observed_at, (xmax = 0) AS created
	`,
		repair.WorkspaceID, repair.SessionID, repair.TaskID, repair.GoalVersion, repair.PlanVersion,
		string(class), fingerprint, string(kind), repair.RepairKey,
		repair.SourceReason, targetConfigFingerprint, repair.Diagnostics,
		in.AttemptID,
	).Scan(
		&repair.ID, &repair.OccurrenceCount, &repair.FirstAttemptID, &repair.LastAttemptID,
		&repair.FirstObservedAt, &repair.LastObservedAt, &created,
	)
	if err != nil {
		return TargetRepair{}, false, err
	}

	// A repair decision projects exactly once. A later occurrence of the same
	// canonical failure updates the durable observation counters above, but it
	// must not replay target_repair_decided with occurrence-specific attempt or
	// diagnostics fields under the decision's idempotency key.
	if created {
		if _, err = appendEvent(ctx, tx, repair.WorkspaceID, repair.SessionID, "target_repair_decided",
			"target-repair:"+repair.RepairKey, "system", "", map[string]any{
				"repair_id": repair.ID, "repair_key": repair.RepairKey, "repair_kind": kind,
				"task_id": repair.TaskID, "attempt_id": in.AttemptID,
				"failure_class": class, "source_failure_reason": repair.SourceReason,
				"failure_fingerprint": fingerprint, "goal_version": repair.GoalVersion,
				"plan_version": repair.PlanVersion, "diagnostics": repair.Diagnostics,
				"target_config_fingerprint": targetConfigFingerprint,
				"allowed_repair_actions":    AllowedRepairActions(class),
			}); err != nil {
			return TargetRepair{}, false, err
		}
	}
	return repair, created, nil
}

// ListTargetRepairs returns the repair decisions recorded for a session,
// newest observation first.
func (s *PostgresStore) ListTargetRepairs(ctx context.Context, sessionID string) ([]TargetRepair, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, task_id::text,
		       goal_version, plan_version, failure_class, failure_fingerprint,
		       repair_kind, repair_key, source_failure_reason,
		       target_config_fingerprint, diagnostics, occurrence_count,
		       COALESCE(first_attempt_id::text, ''), COALESCE(last_attempt_id::text, ''),
		       first_observed_at, last_observed_at
		FROM research_target_repair
		WHERE session_id = $1::uuid
		ORDER BY last_observed_at DESC, id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repairs := []TargetRepair{}
	for rows.Next() {
		var repair TargetRepair
		if err = rows.Scan(
			&repair.ID, &repair.WorkspaceID, &repair.SessionID, &repair.TaskID,
			&repair.GoalVersion, &repair.PlanVersion, &repair.FailureClass, &repair.FailureFingerprint,
			&repair.RepairKind, &repair.RepairKey, &repair.SourceReason,
			&repair.TargetConfigFingerprint, &repair.Diagnostics, &repair.OccurrenceCount,
			&repair.FirstAttemptID, &repair.LastAttemptID,
			&repair.FirstObservedAt, &repair.LastObservedAt,
		); err != nil {
			return nil, err
		}
		repairs = append(repairs, repair)
	}
	return repairs, rows.Err()
}

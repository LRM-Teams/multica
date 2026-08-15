package researchrun

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// bindTaskInquiryTargetsTx persists the typed Inquiry scope frozen by a Plan.
// It is intentionally transaction-only: callers must create the Task and its
// target bindings in the same accepted Plan Result transaction.
func bindTaskInquiryTargetsTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, taskID string, targets []TaskInquiryTarget) error {
	if err := (inquiryModule{}).ValidateTaskTargets(targets); err != nil {
		return err
	}
	for ordinal, target := range targets {
		tag, err := tx.Exec(ctx, `
			INSERT INTO research_task_inquiry_target (
			  workspace_id,session_id,task_id,target_kind,target_entity_id,ordinal,goal_version,plan_version
			)
			SELECT $1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,task.goal_version,task.plan_version
			FROM research_task task
			WHERE task.workspace_id=$1::uuid AND task.session_id=$2::uuid AND task.id=$3::uuid
		`, workspaceID, sessionID, taskID, string(target.Kind), target.EntityID, ordinal)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("%w: Task Inquiry target was not inserted", ErrInvalidTransition)
		}
	}
	return nil
}

package researchrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProcessV6EventTriggers starts the next Director cycle after a material Run
// state change. Coverage is recorded by the cycle's immutable event watermark,
// so restart and concurrent reconcilers can safely rediscover the same event.
func (s *PostgresStore) ProcessV6EventTriggers(ctx context.Context, limit int) (int, error) {
	processed := 0
	for processed < limit {
		var eventID, workspaceID, runID string
		var fromSequence, throughSequence, stateVersion int64
		err := s.pool.QueryRow(ctx, `SELECT e.id::text,e.workspace_id::text,e.session_id::text,e.sequence,
			(SELECT max(last.sequence) FROM research_run_event last WHERE last.session_id=e.session_id),s.state_version
			FROM research_run_event e JOIN research_session s ON s.workspace_id=e.workspace_id AND s.id=e.session_id
			JOIN research_director_assignment a ON a.id=s.current_director_assignment_id AND a.status='active'
			WHERE s.orchestrator_version='research-run-v6'
			AND e.event_type IN (
				'v6_result_node_accepted','v6_plan_materialized','v6_evidence_screened','v6_integration_materialized',
				'v6_deliberation_materialized','v6_director_adjudication_materialized','v6_work_item_succeeded',
				'v6_work_item_recovered','v6_work_submission_rejected','v6_agent_creation_requested',
				'v6_team_member_joined','v6_team_member_archived','v6_work_item_created','v6_branch_created',
				'v6_match_decision_recorded','v6_discussion_open','v6_discussion_append_turn','v6_discussion_close',
				'v6_dispute_review_tasks_created','v6_integration_commit','v6_report_work_created',
				'v6_report_draft_accepted','v6_report_reviewed','v6_director_unavailable',
				'v6_director_assigned',
				'task_result_accepted','task_attempt_failed','task_blocked','budget_exhausted','run_resumed'
			)
			AND NOT EXISTS (SELECT 1 FROM research_director_cycle covered WHERE covered.session_id=e.session_id
				AND covered.trigger_from_sequence<=e.sequence AND covered.trigger_through_sequence>=e.sequence)
			AND NOT EXISTS (SELECT 1 FROM research_work_item active WHERE active.session_id=e.session_id AND active.kind='director'
				AND active.status IN ('ready','dispatching','enqueued','running','awaiting_input'))
			ORDER BY e.created_at,e.sequence,e.id LIMIT 1`).Scan(&eventID, &workspaceID, &runID, &fromSequence, &throughSequence, &stateVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		_, err = (directorBriefModule{store: s, compiler: contextCompilerModule{}}).Start(ctx, StartV6DirectorCycleInput{
			WorkspaceID: workspaceID, RunID: runID, TriggerKey: "event:" + eventID,
			FromSequence: fromSequence, ThroughSequence: throughSequence, ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
		})
		if errors.Is(err, ErrWorkItemChanged) {
			return processed, nil
		}
		if err != nil {
			return processed, fmt.Errorf("start V6 Director event cycle: %w", err)
		}
		processed++
	}
	return processed, nil
}

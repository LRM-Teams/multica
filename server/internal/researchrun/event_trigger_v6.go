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
	processed, skipped := 0, 0
	for processed+skipped < limit {
		var eventID, workspaceID, runID string
		var fromSequence, throughSequence, stateVersion int64
		var previouslyCovered bool
		err := s.pool.QueryRow(ctx, `SELECT e.id::text,e.workspace_id::text,e.session_id::text,e.sequence,
			(SELECT max(last.sequence) FROM research_run_event last WHERE last.workspace_id=e.workspace_id AND last.session_id=e.session_id),s.state_version,
			EXISTS (SELECT 1 FROM research_director_cycle previous WHERE previous.workspace_id=e.workspace_id AND previous.session_id=e.session_id
				AND previous.trigger_from_sequence<=e.sequence AND previous.trigger_through_sequence>=e.sequence)
			FROM research_run_event e JOIN research_session s ON s.workspace_id=e.workspace_id AND s.id=e.session_id
			JOIN research_director_assignment a ON a.id=s.current_director_assignment_id AND a.status='active'
			WHERE s.orchestrator_version='research-run-v6' AND s.status='running'
			AND e.event_type IN (
				'v6_run_bootstrapped','v6_result_node_accepted','v6_plan_materialized','v6_evidence_screened','v6_integration_materialized',
				'v6_deliberation_materialized','v6_director_adjudication_materialized','v6_work_item_succeeded',
				'v6_work_item_recovered','v6_work_submission_rejected','v6_agent_creation_requested',
				'v6_team_member_joined','v6_team_member_archived','v6_work_item_created','v6_branch_created',
				'v6_match_decision_recorded','v6_discussion_open','v6_discussion_append_turn','v6_discussion_close',
				'v6_discussion_resolved',
				'v6_dispute_review_tasks_created','v6_integration_commit','v6_report_work_created',
				'v6_report_draft_accepted','v6_report_reviewed','v6_director_unavailable',
				'v6_director_assigned',
				'task_result_accepted','task_attempt_failed','task_blocked','budget_exhausted','run_resumed'
			)
			AND NOT EXISTS (SELECT 1 FROM research_director_cycle covered WHERE covered.workspace_id=e.workspace_id AND covered.session_id=e.session_id
				AND covered.trigger_from_sequence<=e.sequence AND covered.trigger_through_sequence>=e.sequence
				AND (e.event_type<>'v6_result_node_accepted' OR EXISTS (
					SELECT 1 FROM research_director_brief_page page
					CROSS JOIN LATERAL jsonb_array_elements(COALESCE(convert_from(page.content_bytes,'UTF8')::jsonb #> '{research,branches}','[]'::jsonb)) branch
					CROSS JOIN LATERAL jsonb_array_elements(COALESCE(branch #> '{frontier_nodes}','[]'::jsonb)) frontier
					WHERE page.workspace_id=e.workspace_id AND page.session_id=e.session_id AND page.director_cycle_id=covered.id
					AND frontier #>> '{node,version_id}'=e.payload->>'artifact_version_id'
					AND (
						NOT EXISTS (
							SELECT 1 FROM research_result_node unresolved
							WHERE unresolved.workspace_id=e.workspace_id AND unresolved.session_id=e.session_id
							AND unresolved.artifact_version_id::text=e.payload->>'artifact_version_id'
							AND jsonb_array_length(COALESCE(unresolved.open_questions,'[]'::jsonb))>0
						)
						OR strpos(COALESCE(frontier #>> '{brief_summary}',''),$1)>0
					)
				))
				AND (e.event_type<>'v6_discussion_close' OR e.payload->>'status'<>'escalated' OR EXISTS (
					SELECT 1 FROM research_work_item followup
					WHERE followup.workspace_id=e.workspace_id AND followup.session_id=e.session_id
					  AND followup.kind='research' AND followup.created_at>e.created_at
				)))
			AND NOT EXISTS (SELECT 1 FROM research_work_item active WHERE active.workspace_id=e.workspace_id AND active.session_id=e.session_id AND active.kind='director'
				AND active.status IN ('ready','dispatching','enqueued','running','awaiting_input'))
			AND NOT EXISTS (SELECT 1 FROM research_v6_outbox material_effect
				WHERE material_effect.workspace_id=e.workspace_id AND material_effect.session_id=e.session_id
				AND material_effect.kind IN ('create_agent','archive_agent')
				AND material_effect.status IN ('pending','delivering'))
			ORDER BY e.created_at,e.sequence,e.id OFFSET $2 LIMIT 1`, directorBriefOpenQuestionsMarker, skipped).Scan(&eventID, &workspaceID, &runID, &fromSequence, &throughSequence, &stateVersion, &previouslyCovered)
		if errors.Is(err, pgx.ErrNoRows) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		triggerKey := "event:" + eventID
		if previouslyCovered {
			triggerKey = "event-frontier-repair:" + eventID
		}
		cycle, err := (directorBriefModule{store: s, compiler: contextCompilerModule{}}).Start(ctx, StartV6DirectorCycleInput{
			WorkspaceID: workspaceID, RunID: runID, TriggerKey: triggerKey,
			FromSequence: fromSequence, ThroughSequence: throughSequence, ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
		})
		if errors.Is(err, ErrWorkItemChanged) {
			// Keep the racing Run eligible for the next tick without letting it
			// block unrelated Runs behind it in the global trigger queue.
			skipped++
			continue
		}
		if err != nil {
			return processed, fmt.Errorf("start V6 Director event cycle: %w", err)
		}
		if cycle.Replayed {
			// A covered repair event can remain eligible after its idempotent
			// cycle terminates without producing the required material effect.
			// Do not repeatedly count that replay as fresh work in this batch.
			skipped++
			continue
		}
		processed++
	}
	return processed, nil
}

// ProcessV6IdleRuns wakes a running Run that has gone silent: no in-flight
// work, no pending outbox effects, no unapplied submissions, and no Run Event
// for a while. Without this, a Director cycle that ends in no_op leaves no
// future trigger and the Run stays "running" forever with nothing scheduled.
// The trigger key embeds the last event sequence so each stall state wakes the
// Director at most once, and a rolling cap bounds the cost of a Director that
// keeps answering no_op on an abandoned Run.
func (s *PostgresStore) ProcessV6IdleRuns(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT s.workspace_id::text,s.id::text,s.state_version,
			(SELECT max(e.sequence) FROM research_run_event e WHERE e.session_id=s.id)
		FROM research_session s
		JOIN research_director_assignment a ON a.id=s.current_director_assignment_id AND a.status='active'
		WHERE s.orchestrator_version='research-run-v6' AND s.status='running'
		AND EXISTS (SELECT 1 FROM research_run_event e WHERE e.session_id=s.id)
		AND (SELECT max(e.created_at) FROM research_run_event e WHERE e.session_id=s.id)<=now()-interval '10 minutes'
		AND NOT EXISTS (SELECT 1 FROM research_work_item w WHERE w.session_id=s.id
			AND w.status IN ('ready','dispatching','enqueued','running','awaiting_input'))
		AND NOT EXISTS (SELECT 1 FROM research_v6_outbox o WHERE o.session_id=s.id AND o.status IN ('pending','delivering'))
		AND NOT EXISTS (SELECT 1 FROM research_v6_work_submission sub WHERE sub.session_id=s.id
			AND sub.status IN ('received','processing'))
		AND (SELECT count(*) FROM research_work_item wake WHERE wake.session_id=s.id
			AND wake.client_key LIKE 'director-cycle:idle:%' AND wake.created_at>now()-interval '6 hours')<3
		ORDER BY s.created_at,s.id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type idleRun struct {
		workspaceID, runID string
		stateVersion       int64
		throughSequence    int64
	}
	candidates := []idleRun{}
	for rows.Next() {
		var run idleRun
		if err = rows.Scan(&run.workspaceID, &run.runID, &run.stateVersion, &run.throughSequence); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, run)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	woken := 0
	for _, run := range candidates {
		_, err = (directorBriefModule{store: s, compiler: contextCompilerModule{}}).Start(ctx, StartV6DirectorCycleInput{
			WorkspaceID: run.workspaceID, RunID: run.runID,
			TriggerKey:   fmt.Sprintf("idle:%d", run.throughSequence),
			FromSequence: run.throughSequence, ThroughSequence: run.throughSequence,
			ExpectedStateVersion: run.stateVersion, Now: time.Now().UTC(),
		})
		if errors.Is(err, ErrWorkItemChanged) || errors.Is(err, ErrV6DirectorUnavailable) {
			continue
		}
		if err != nil {
			return woken, fmt.Errorf("start V6 Director idle cycle: %w", err)
		}
		woken++
	}
	return woken, nil
}

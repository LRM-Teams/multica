package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type acceptedResultState struct {
	workspaceID  string
	attemptID    string
	task         Task
	run          Run
	stale        bool
	targetPlan   int
	verified     bool
	outputAccess ArtifactAccessLevel
}

func (s *PostgresStore) AcceptResult(ctx context.Context, in AcceptResultInput) (AcceptResultOutcome, error) {
	in.Hash = normalizeArtifactContentHash(in.Hash)
	tx, err := s.beginResearchTx(ctx, txOpResultAccept, pgx.TxOptions{})
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.SessionID, ""); err != nil {
		return AcceptResultOutcome{}, err
	}

	state, replay, err := lockResultAttempt(ctx, tx, in)
	if err != nil || replay != nil {
		if replay != nil {
			if err = s.commitResearchTx(ctx, txOpResultAccept, tx); err != nil {
				return AcceptResultOutcome{}, err
			}
			return *replay, nil
		}
		return AcceptResultOutcome{}, err
	}
	state.outputAccess = ArtifactAccessRaw

	artifactPassportEnabled, err := sessionArtifactPassportEnabled(ctx, tx, in.SessionID, state.workspaceID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	var acceptancePolicyWatermark int64
	if artifactPassportEnabled {
		_, acceptancePolicyWatermark, err = verifyAcceptanceManifestPolicyTx(ctx, tx, state.workspaceID, in.SessionID, in.AttemptID)
		if err != nil {
			return AcceptResultOutcome{}, err
		}
		if err = verifyAcceptanceManifestEntryEligibilityTx(ctx, tx, state.workspaceID, in.SessionID, in.AttemptID); err != nil {
			return AcceptResultOutcome{}, err
		}
		if err = verifyAcceptanceManifestHashTx(ctx, tx, state.workspaceID, in.SessionID, in.AttemptID); err != nil {
			return AcceptResultOutcome{}, err
		}
		if err = lockAcceptanceManifestAuthorizationTargetsTx(ctx, tx, state.workspaceID, in.SessionID, in.AttemptID); err != nil {
			return AcceptResultOutcome{}, err
		}
		state.outputAccess, err = deriveManifestOutputAccessTx(ctx, tx, state.workspaceID, in.SessionID, in.AttemptID)
		if err != nil {
			return AcceptResultOutcome{}, err
		}
	}

	if !state.stale && state.task.Kind == TaskKindReplan {
		state.targetPlan = state.run.PlanVersion + 1
		if err = prepareReplan(ctx, tx, state); err != nil {
			return AcceptResultOutcome{}, err
		}
	}
	measureGain := isEvidenceTask(state.task.Kind) && !state.stale
	var graphBefore researchGraphState
	if measureGain {
		graphBefore, err = s.loadResearchGraphState(ctx, tx, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
		if err != nil {
			return AcceptResultOutcome{}, err
		}
	}

	outcome := AcceptResultOutcome{
		TaskID:      state.task.ID,
		TaskKind:    state.task.Kind,
		GoalVersion: state.task.GoalVersion,
		PlanVersion: state.targetPlan,
	}
	if !state.stale && (state.task.Kind == TaskKindPlan || state.task.Kind == TaskKindReplan) && usesResearchMethodContract(state.run.OrchestratorVersion) {
		if in.Result.Plan == nil || in.Result.Plan.Method == nil {
			return AcceptResultOutcome{}, fmt.Errorf("%w: this orchestrator version requires a research method", ErrInvalidResult)
		}
		if err = materializeResearchMethod(ctx, tx, state, *in.Result.Plan, in.AgentID); err != nil {
			return AcceptResultOutcome{}, err
		}
	}
	questionIDs := map[string]string{}
	created := 0
	if !state.stale {
		questionIDs, created, err = materializeQuestions(ctx, tx, state, in.Result)
		if err != nil {
			return AcceptResultOutcome{}, err
		}
		outcome.QuestionsCreated = created
		created, err = materializeTasks(ctx, tx, state, in.Result, questionIDs)
		if err != nil {
			return AcceptResultOutcome{}, err
		}
		outcome.TasksCreated = created
	}

	sourceIDs, created, err := materializeSources(ctx, tx, state, in.Result)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	outcome.SourcesCreated = created
	observationIDs, created, err := materializeObservations(ctx, tx, state, in.Result, sourceIDs)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	outcome.ObservationsCreated = created
	claimIDs, created, err := materializeClaims(ctx, tx, state, in.Result, observationIDs)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	outcome.ClaimsCreated = created

	if in.Result.Report != nil && !state.stale {
		outcome.ReportID, err = materializeReport(ctx, tx, state, *in.Result.Report, claimIDs)
		if err != nil {
			return AcceptResultOutcome{}, err
		}
	}
	if in.Result.Evaluation != nil && !state.stale {
		if err = materializeEvaluation(ctx, tx, state, *in.Result.Evaluation); err != nil {
			return AcceptResultOutcome{}, err
		}
	}
	if err = updateQuestionProgress(ctx, tx, state, in.Result, claimIDs); err != nil {
		return AcceptResultOutcome{}, err
	}
	var gain informationGainBreakdown
	if measureGain {
		graphAfter, loadErr := s.loadResearchGraphState(ctx, tx, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
		if loadErr != nil {
			return AcceptResultOutcome{}, loadErr
		}
		gain = measuredInformationGain(graphBefore, graphAfter, state.task.Kind)
		if err = recordInformationGain(ctx, tx, state, in, graphBefore, graphAfter, gain); err != nil {
			return AcceptResultOutcome{}, err
		}
	}

	resultJSON, err := json.Marshal(in.Result)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE research_task_attempt
		SET client_request_id = $2, result_hash = $3, result = $4,
		    status = 'succeeded', result_submitted_at = now(), completed_at = now(),
		    updated_at = now()
		WHERE id = $1::uuid AND status IN ('dispatching', 'running')
	`, in.AttemptID, in.Result.ClientRequestID, in.Hash, resultJSON)
	if err != nil {
		return AcceptResultOutcome{}, classifyResultConstraint(err)
	}
	if command.RowsAffected() != 1 {
		return AcceptResultOutcome{}, fmt.Errorf("%w: attempt changed while accepting result", ErrInvalidTransition)
	}
	if err = settleAttemptCircuitSuccessTx(ctx, tx, in.AttemptID); err != nil {
		return AcceptResultOutcome{}, err
	}
	if artifactPassportEnabled {
		resultID, persistErr := persistAcceptedResultArtifactTx(
			ctx, tx, state.workspaceID, in.SessionID, in.AttemptID,
			state.run.OrchestratorVersion, in.Result, resultJSON, in.Hash, acceptancePolicyWatermark, state.outputAccess,
		)
		if persistErr != nil {
			return AcceptResultOutcome{}, classifyResultConstraint(persistErr)
		}
		resultVersionRowID, versionErr := loadManifestVersionRowIDTx(ctx, tx, state.workspaceID, in.SessionID, resultID)
		if versionErr != nil {
			return AcceptResultOutcome{}, versionErr
		}
		if err = persistResultArtifactInputReferencesTx(ctx, tx, state.workspaceID, in.SessionID, in.AttemptID, resultVersionRowID); err != nil {
			return AcceptResultOutcome{}, classifyResultConstraint(err)
		}
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_task
		SET status = 'succeeded', completed_at = now(), terminal_reason = '', updated_at = now()
		WHERE id = $1::uuid
	`, state.task.ID); err != nil {
		return AcceptResultOutcome{}, err
	}
	if !state.stale {
		if _, err = tx.Exec(ctx, `
			UPDATE research_task t
			SET status = 'ready', ready_at = now(), updated_at = now()
			WHERE t.session_id = $1::uuid
			  AND t.goal_version = $2 AND t.plan_version = $3
			  AND t.status = 'pending'
			  AND NOT EXISTS (
			    SELECT 1 FROM research_task_dependency d
			    JOIN research_task dependency ON dependency.id = d.depends_on_task_id
			    WHERE d.task_id = t.id AND dependency.status <> 'succeeded'
			  )
		`, in.SessionID, state.run.GoalVersion, state.targetPlan); err != nil {
			return AcceptResultOutcome{}, err
		}
	}
	stage := stageForTask(state.task.Kind)
	if _, err = tx.Exec(ctx, `
		UPDATE research_session
		SET current_stage = CASE WHEN $2 = '' THEN current_stage ELSE $2 END,
		    last_progress_at = now(), next_reconcile_at = now(), last_error = '',
		    run_stats = run_stats || jsonb_build_object(
		      'accepted_results', COALESCE((run_stats->>'accepted_results')::int, 0) + 1,
		      'evidence_batches', COALESCE((run_stats->>'evidence_batches')::int, 0) + CASE WHEN $5::boolean THEN 1 ELSE 0 END,
		      'low_gain_streak', CASE WHEN NOT $5::boolean THEN COALESCE((run_stats->>'low_gain_streak')::int, 0)
		                              WHEN $6::double precision < $7::double precision THEN COALESCE((run_stats->>'low_gain_streak')::int, 0) + 1 ELSE 0 END,
			  'last_coverage_delta', CASE WHEN $5::boolean THEN $3::double precision ELSE COALESCE((run_stats->>'last_coverage_delta')::double precision, 0) END,
			  'last_measured_gain', CASE WHEN $5::boolean THEN $6::double precision ELSE COALESCE((run_stats->>'last_measured_gain')::double precision, 0) END,
		      'last_confidence', $4::double precision,
		      'sources_created', COALESCE((run_stats->>'sources_created')::int, 0) + $8::int,
		      'observations_created', COALESCE((run_stats->>'observations_created')::int, 0) + $9::int,
		      'claims_created', COALESCE((run_stats->>'claims_created')::int, 0) + $10::int
		    ),
		    updated_at = now()
		WHERE id = $1::uuid
	`, in.SessionID, stage, in.Result.CoverageDelta, in.Result.Confidence,
		measureGain, gain.Score, state.run.Config.MarginalGainThreshold,
		outcome.SourcesCreated, outcome.ObservationsCreated, outcome.ClaimsCreated); err != nil {
		return AcceptResultOutcome{}, err
	}
	event, err := appendEvent(ctx, tx, state.workspaceID, in.SessionID, "task_result_accepted", "result:"+in.AttemptID, "agent", in.AgentID, map[string]any{
		"attempt_id":           in.AttemptID,
		"task_id":              state.task.ID,
		"task_kind":            state.task.Kind,
		"goal_version":         state.task.GoalVersion,
		"plan_version":         state.targetPlan,
		"stale":                state.stale,
		"summary":              truncateBytes(in.Result.Summary, 2048),
		"questions_created":    outcome.QuestionsCreated,
		"tasks_created":        outcome.TasksCreated,
		"sources_created":      outcome.SourcesCreated,
		"observations_created": outcome.ObservationsCreated,
		"claims_created":       outcome.ClaimsCreated,
		"report_id":            outcome.ReportID,
		"gain_measured":        measureGain,
		"measured_gain":        gain.Score,
		"gain_breakdown":       gain,
	})
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	outcome.Event = event
	if err = s.commitResearchTx(ctx, txOpResultAccept, tx); err != nil {
		return AcceptResultOutcome{}, err
	}
	return outcome, nil
}

func materializeResearchMethod(ctx context.Context, tx pgx.Tx, state acceptedResultState, plan PlanProposal, agentID string) error {
	if plan.Method == nil {
		return fmt.Errorf("%w: this orchestrator version requires a research method", ErrInvalidResult)
	}
	if err := validateMethodProposal(*plan.Method); err != nil {
		return err
	}
	if err := validateV3PlanMethodLists(plan); err != nil {
		return err
	}
	if usesEvidenceFitnessContract(state.run.OrchestratorVersion) {
		if _, err := validateEvidenceStandards(plan.Method.EvidenceStandards); err != nil {
			return err
		}
	}
	method := ResearchMethod{
		GoalVersion:             state.run.GoalVersion,
		PlanVersion:             state.targetPlan,
		DecisionQuestion:        strings.TrimSpace(plan.Method.DecisionQuestion),
		MethodRationale:         strings.TrimSpace(plan.Method.MethodRationale),
		AnalysisMethods:         plan.Method.AnalysisMethods,
		EvidenceRequirements:    plan.Method.EvidenceRequirements,
		EvidenceStandards:       plan.Method.EvidenceStandards,
		InclusionCriteria:       plan.InclusionCriteria,
		ExclusionCriteria:       plan.ExclusionCriteria,
		SourceStrategy:          plan.SourceStrategy,
		CounterevidenceStrategy: plan.Method.CounterevidenceStrategy,
		StoppingConditions:      plan.Method.StoppingConditions,
		Uncertainties:           plan.Uncertainties,
		PlanningRisks:           plan.PlanningRisks,
		CreatedByTaskID:         state.task.ID,
		CreatedByAgentID:        agentID,
	}
	outcome, err := json.Marshal(method)
	if err != nil {
		return err
	}
	inputs, err := json.Marshal(map[string]any{
		"attempt_id": state.attemptID,
		"task_id":    state.task.ID,
		"task_kind":  state.task.Kind,
	})
	if err != nil {
		return err
	}
	rationale := truncateBytes(method.MethodRationale, 8192)
	var decisionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO research_decision (
			workspace_id, session_id, decision_kind, actor_type, actor_id,
			goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, 'research_method', 'agent', $3::uuid,
		          $4, $5, $6, $7, $8)
		RETURNING id::text
	`, state.workspaceID, state.run.SessionID, agentID, state.run.GoalVersion,
		state.targetPlan, inputs, outcome, rationale).Scan(&decisionID)
	if err != nil {
		return err
	}
	kind := artifactKindForDecision("research_method")
	contentHash, err := ArtifactContentHash(kind, map[string]any{
		"decision_kind": "research_method",
		"actor_type":    "agent",
		"actor_id":      agentID,
		"goal_version":  state.run.GoalVersion,
		"plan_version":  state.targetPlan,
		"inputs":        json.RawMessage(inputs),
		"outcome":       json.RawMessage(outcome),
		"rationale":     rationale,
	})
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            state.workspaceID,
		SessionID:              state.run.SessionID,
		EntityID:               decisionID,
		Kind:                   kind,
		SourceCreatedAt:        timePtr(time.Now()),
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            int32Ptr(int32(state.run.GoalVersion)),
		PlanVersion:            int32Ptr(int32(state.targetPlan)),
		AccessLevel:            state.outputAccess,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
		ProducedByAttemptID:    state.attemptID,
	})
}

func isEvidenceTask(kind TaskKind) bool {
	switch kind {
	case TaskKindDiscover, TaskKindDeepRead, TaskKindVerify, TaskKindCounterSearch:
		return true
	default:
		return false
	}
}

func lockResultAttempt(ctx context.Context, tx pgx.Tx, in AcceptResultInput) (acceptedResultState, *AcceptResultOutcome, error) {
	var state acceptedResultState
	state.attemptID = in.AttemptID
	var attemptStatus AttemptStatus
	var assignedAgentID, assignedInboxTaskID, existingRequestID, existingHash string
	var runStatus RunStatus
	var configJSON []byte
	err := tx.QueryRow(ctx, `
		SELECT a.workspace_id::text, a.assigned_agent_id::text,
		       COALESCE(a.inbox_task_id::text, ''), a.status,
		       COALESCE(a.client_request_id, ''), COALESCE(a.result_hash, ''),
		       t.id::text, t.session_id::text, t.workspace_id::text,
		       COALESCE(t.question_id::text, ''), COALESCE(t.parent_task_id::text, ''),
		       t.client_key, t.kind, t.objective, t.required_capability,
		       t.expected_result, t.acceptance_criteria, t.priority, t.status,
		       COALESCE(t.assigned_agent_id::text, ''), t.goal_version, t.plan_version,
		       t.max_attempts, t.timeout_seconds,
		       (SELECT count(*)::int FROM research_task_attempt counted WHERE counted.task_id = t.id),
		       t.ready_at, t.started_at, t.completed_at, t.terminal_reason,
		       s.fleet_id::text, s.created_by::text, s.title, s.goal, s.status,
		       s.current_stage, s.depth_tier, s.goal_version, s.plan_version,
		       s.state_version, s.orchestrator_version, s.run_config,
		       s.run_initialized_at, s.last_progress_at, s.next_reconcile_at,
		       s.stop_reason, s.last_error
		FROM research_task_attempt a
		JOIN research_task t ON t.id = a.task_id
		JOIN research_session s ON s.id = a.session_id
		WHERE a.id = $1::uuid AND a.session_id = $2::uuid
		FOR UPDATE OF a, t, s
	`, in.AttemptID, in.SessionID).Scan(
		&state.workspaceID, &assignedAgentID, &assignedInboxTaskID, &attemptStatus, &existingRequestID, &existingHash,
		&state.task.ID, &state.task.SessionID, &state.task.WorkspaceID,
		&state.task.QuestionID, &state.task.ParentTaskID, &state.task.ClientKey,
		&state.task.Kind, &state.task.Objective, &state.task.RequiredCapability,
		&state.task.ExpectedResult, &state.task.AcceptanceCriteria, &state.task.Priority,
		&state.task.Status, &state.task.AssignedAgentID, &state.task.GoalVersion,
		&state.task.PlanVersion, &state.task.MaxAttempts, &state.task.TimeoutSeconds,
		&state.task.AttemptCount, &state.task.ReadyAt, &state.task.StartedAt,
		&state.task.CompletedAt, &state.task.TerminalReason,
		&state.run.FleetID, &state.run.CreatedBy, &state.run.Title, &state.run.Goal,
		&runStatus, &state.run.CurrentStage, &state.run.DepthTier,
		&state.run.GoalVersion, &state.run.PlanVersion, &state.run.StateVersion,
		&state.run.OrchestratorVersion, &configJSON, &state.run.InitializedAt,
		&state.run.LastProgressAt, &state.run.NextReconcileAt, &state.run.StopReason,
		&state.run.LastError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return acceptedResultState{}, nil, ErrRunNotFound
	}
	if err != nil {
		return acceptedResultState{}, nil, err
	}
	state.run.SessionID = in.SessionID
	state.run.WorkspaceID = state.workspaceID
	state.run.Status = runStatus
	state.run.Config = DefaultRunConfig(state.run.DepthTier)
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &state.run.Config); err != nil {
			return acceptedResultState{}, nil, fmt.Errorf("decode run config: %w", err)
		}
	}
	state.targetPlan = state.task.PlanVersion
	state.stale = state.task.GoalVersion != state.run.GoalVersion || state.task.PlanVersion != state.run.PlanVersion
	state.verified = verificationTask(state.task.Kind)
	if attemptStatus == AttemptStatusSucceeded {
		if existingRequestID != in.Result.ClientRequestID || existingHash != in.Hash ||
			assignedAgentID != in.AgentID || assignedInboxTaskID == "" || assignedInboxTaskID != in.InboxTaskID {
			return acceptedResultState{}, nil, ErrResultConflict
		}
		passportEnabled, replayErr := sessionArtifactPassportEnabled(ctx, tx, in.SessionID, state.workspaceID)
		if replayErr != nil {
			return acceptedResultState{}, nil, replayErr
		}
		if passportEnabled {
			if replayErr = verifyAcceptedResultReplayTx(ctx, tx, state.workspaceID, in); replayErr != nil {
				return acceptedResultState{}, nil, replayErr
			}
		}
		return state, &AcceptResultOutcome{Replayed: true, TaskID: state.task.ID, TaskKind: state.task.Kind, GoalVersion: state.task.GoalVersion, PlanVersion: state.task.PlanVersion}, nil
	}
	if assignedAgentID != in.AgentID || assignedInboxTaskID == "" || assignedInboxTaskID != in.InboxTaskID {
		return acceptedResultState{}, nil, ErrAttemptNotAssigned
	}
	if attemptStatus != AttemptStatusDispatching && attemptStatus != AttemptStatusRunning {
		return acceptedResultState{}, nil, fmt.Errorf("%w: attempt is %s", ErrInvalidTransition, attemptStatus)
	}
	if runStatus == RunStatusCancelled || runStatus == RunStatusArchived || runStatus == RunStatusCompleted {
		return acceptedResultState{}, nil, fmt.Errorf("%w: run is %s", ErrInvalidTransition, runStatus)
	}
	return state, nil, nil
}

func verifyAcceptedResultReplayTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID string,
	in AcceptResultInput,
) error {
	if err := verifyAcceptanceManifestHashTx(ctx, tx, workspaceID, in.SessionID, in.AttemptID); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			return fmt.Errorf("%w: accepted manifest hash changed", ErrResultConflict)
		}
		return err
	}

	var bindingMatches bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM research_result_artifact r
		  JOIN research_artifact_passport p
		    ON (p.workspace_id, p.session_id, p.id) = (r.workspace_id, r.session_id, r.id)
		  JOIN research_artifact_version rv
		    ON rv.workspace_id = p.workspace_id
		   AND rv.session_id = p.session_id
		   AND rv.artifact_id = p.id
		   AND rv.version = p.current_version
		  JOIN research_artifact_context_manifest m
		    ON m.workspace_id = r.workspace_id
		   AND m.session_id = r.session_id
		   AND m.attempt_id = r.attempt_id
		  WHERE r.workspace_id = $1::uuid
		    AND r.session_id = $2::uuid
		    AND r.attempt_id = $3::uuid
		    AND r.client_request_id = $4
		    AND r.content_hash = $5
		    AND p.entity_kind = 'result_artifact'
		    AND p.produced_by_attempt_id = r.attempt_id
		    AND rv.content_hash = r.content_hash
		    AND NOT EXISTS (
		      SELECT e.artifact_version_id
		      FROM research_artifact_context_entry e
		      WHERE (e.workspace_id, e.session_id, e.manifest_id) =
		            (m.workspace_id, m.session_id, m.id)
		      EXCEPT
		      SELECT ref.input_version_id
		      FROM research_artifact_input_reference ref
		      WHERE ref.workspace_id = r.workspace_id
		        AND ref.session_id = r.session_id
		        AND ref.consumer_version_id = rv.id
		        AND ref.relation = 'acceptance_input'
		        AND ref.manifest_id = m.id
		    )
		    AND NOT EXISTS (
		      SELECT ref.input_version_id
		      FROM research_artifact_input_reference ref
		      WHERE ref.workspace_id = r.workspace_id
		        AND ref.session_id = r.session_id
		        AND ref.consumer_version_id = rv.id
		        AND ref.relation = 'acceptance_input'
		        AND ref.manifest_id = m.id
		      EXCEPT
		      SELECT e.artifact_version_id
		      FROM research_artifact_context_entry e
		      WHERE (e.workspace_id, e.session_id, e.manifest_id) =
		            (m.workspace_id, m.session_id, m.id)
		    )
		    AND NOT EXISTS (
		      SELECT 1
		      FROM research_artifact_input_reference ref
		      WHERE ref.workspace_id = r.workspace_id
		        AND ref.session_id = r.session_id
		        AND ref.consumer_version_id = rv.id
		        AND ref.relation = 'acceptance_input'
		        AND ref.manifest_id IS DISTINCT FROM m.id
		    )
		)
	`, workspaceID, in.SessionID, in.AttemptID, in.Result.ClientRequestID, in.Hash).Scan(&bindingMatches)
	if err != nil {
		return err
	}
	if !bindingMatches {
		return fmt.Errorf("%w: accepted result manifest, resolved versions, or lineage changed", ErrResultConflict)
	}
	return nil
}

func prepareReplan(ctx context.Context, tx pgx.Tx, state acceptedResultState) error {
	if _, err := tx.Exec(ctx, `
		UPDATE research_task
		SET status = 'obsolete', terminal_reason = 'replanned', completed_at = now(), updated_at = now()
		WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		  AND id <> $4::uuid AND status IN ('pending', 'ready')
	`, state.run.SessionID, state.run.GoalVersion, state.run.PlanVersion, state.task.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE research_question
		SET status = 'obsolete', terminal_explanation = 'replanned', updated_at = now()
		WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		  AND client_key <> 'root' AND status IN ('open', 'in_progress')
	`, state.run.SessionID, state.run.GoalVersion, state.run.PlanVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE research_task SET plan_version = $2, updated_at = now() WHERE id = $1::uuid
	`, state.task.ID, state.targetPlan); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE research_session SET plan_version = $2, updated_at = now() WHERE id = $1::uuid`, state.run.SessionID, state.targetPlan)
	return err
}

func materializeQuestions(ctx context.Context, tx pgx.Tx, state acceptedResultState, result ResultEnvelope) (map[string]string, int, error) {
	questions := append([]QuestionProposal(nil), result.Questions...)
	if result.Plan != nil {
		questions = append(questions, result.Plan.Questions...)
	}
	ids, err := loadQuestionIDs(ctx, tx, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
	if err != nil {
		return nil, 0, err
	}
	created := 0
	for _, proposal := range questions {
		var id string
		_, existed := ids[proposal.ClientKey]
		err = tx.QueryRow(ctx, `
			INSERT INTO research_question (
				workspace_id, session_id, created_by_task_id, client_key, kind,
				question, required, status, priority, impact, uncertainty, novelty,
				goal_version, plan_version
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, 'open',
			          $8, $9, $10, $11, $12, $13)
			ON CONFLICT (session_id, goal_version, plan_version, client_key)
			DO UPDATE SET
			  question = EXCLUDED.question,
			  required = research_question.required OR EXCLUDED.required,
			  priority = GREATEST(research_question.priority, EXCLUDED.priority),
			  impact = GREATEST(research_question.impact, EXCLUDED.impact),
			  uncertainty = GREATEST(research_question.uncertainty, EXCLUDED.uncertainty),
			  novelty = GREATEST(research_question.novelty, EXCLUDED.novelty),
			  updated_at = now()
			WHERE research_question.question = EXCLUDED.question
			  AND research_question.kind = EXCLUDED.kind
			RETURNING id::text
		`, state.workspaceID, state.run.SessionID, state.task.ID, proposal.ClientKey,
			proposal.Kind, strings.TrimSpace(proposal.Text), proposal.Required,
			proposal.Priority, proposal.Impact, proposal.Uncertainty, proposal.Novelty,
			state.run.GoalVersion, state.targetPlan).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, fmt.Errorf("%w: question key %q was reused for different content", ErrResultConflict, proposal.ClientKey)
		}
		if err != nil {
			return nil, 0, err
		}
		if !existed {
			created++
			if err = ensureDomainArtifactPassportWithAccessTx(ctx, tx, ArtifactKindQuestion, state.workspaceID, state.run.SessionID, id, time.Now(), int32Ptr(int32(state.run.GoalVersion)), int32Ptr(int32(state.targetPlan)), state.outputAccess); err != nil {
				return nil, 0, err
			}
		}
		ids[proposal.ClientKey] = id
	}
	for _, proposal := range questions {
		if proposal.ParentClientKey == "" {
			continue
		}
		parentID, ok := ids[proposal.ParentClientKey]
		if !ok {
			return nil, 0, fmt.Errorf("%w: question %q references unknown parent %q", ErrInvalidResult, proposal.ClientKey, proposal.ParentClientKey)
		}
		if _, err = tx.Exec(ctx, `UPDATE research_question SET parent_question_id = $2::uuid, updated_at = now() WHERE id = $1::uuid`, ids[proposal.ClientKey], parentID); err != nil {
			return nil, 0, err
		}
	}
	if cyclic, err := questionGraphHasCycle(ctx, tx, state.run.SessionID); err != nil {
		return nil, 0, err
	} else if cyclic {
		return nil, 0, fmt.Errorf("%w: persisted question graph contains a cycle", ErrInvalidResult)
	}
	return ids, created, nil
}

func questionGraphHasCycle(ctx context.Context, tx pgx.Tx, sessionID string) (bool, error) {
	var cyclic bool
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE walk(question_id, parent_id, path, cycle) AS (
		  SELECT q.id, q.parent_question_id, ARRAY[q.id, q.parent_question_id], q.id = q.parent_question_id
		  FROM research_question q
		  WHERE q.session_id = $1::uuid AND q.parent_question_id IS NOT NULL
		  UNION ALL
		  SELECT w.question_id, q.parent_question_id, w.path || q.parent_question_id,
		         q.parent_question_id = ANY(w.path)
		  FROM walk w
		  JOIN research_question q ON q.id = w.parent_id
		  WHERE NOT w.cycle AND q.parent_question_id IS NOT NULL
		)
		SELECT EXISTS(SELECT 1 FROM walk WHERE cycle)
	`, sessionID).Scan(&cyclic)
	return cyclic, err
}

func loadQuestionIDs(ctx context.Context, tx pgx.Tx, sessionID string, goalVersion, planVersion int) (map[string]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT client_key, id::text FROM research_question
		WHERE session_id = $1::uuid AND goal_version = $2
		  AND (plan_version = $3 OR client_key = 'root')
		ORDER BY plan_version DESC
	`, sessionID, goalVersion, planVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]string{}
	for rows.Next() {
		var key, id string
		if err = rows.Scan(&key, &id); err != nil {
			return nil, err
		}
		if _, exists := ids[key]; !exists {
			ids[key] = id
		}
	}
	return ids, rows.Err()
}

func materializeTasks(ctx context.Context, tx pgx.Tx, state acceptedResultState, result ResultEnvelope, questionIDs map[string]string) (int, error) {
	proposals := append([]TaskProposal(nil), result.ProposedTasks...)
	if result.Plan != nil {
		proposals = append(proposals, result.Plan.Tasks...)
	}
	if len(proposals) == 0 {
		return 0, nil
	}
	taskIDs, err := loadTaskIDs(ctx, tx, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
	if err != nil {
		return 0, err
	}
	var existingCount int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid`, state.run.SessionID).Scan(&existingCount); err != nil {
		return 0, err
	}
	missing := 0
	for _, proposal := range proposals {
		if taskIDs[proposal.ClientKey] == "" {
			missing++
		}
	}
	if existingCount+missing > state.run.Config.MaxTasks {
		return 0, fmt.Errorf("%w: proposed tasks exceed remaining run budget", ErrInvalidResult)
	}
	created := 0
	for _, proposal := range proposals {
		questionID := ""
		if proposal.QuestionKey != "" {
			var ok bool
			questionID, ok = questionIDs[proposal.QuestionKey]
			if !ok {
				return 0, fmt.Errorf("%w: task %q references unknown question %q", ErrInvalidResult, proposal.ClientKey, proposal.QuestionKey)
			}
		}
		maxAttempts := proposal.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = state.run.Config.MaxAttemptsPerTask
		}
		timeout := proposal.TimeoutSeconds
		if timeout == 0 {
			timeout = state.run.Config.TaskTimeoutSeconds
		}
		criteria := normalizeJSON(proposal.AcceptanceCriteria, `{}`)
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO research_task (
				workspace_id, session_id, question_id, parent_task_id, client_key,
				kind, objective, required_capability, expected_result,
				acceptance_criteria, priority, status, goal_version, plan_version,
				max_attempts, timeout_seconds
			) VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4::uuid, $5,
			          $6, $7, $8, $9, $10, $11, 'pending', $12, $13,
			          $14, $15)
			ON CONFLICT (session_id, goal_version, plan_version, client_key)
			DO NOTHING
			RETURNING id::text
		`, state.workspaceID, state.run.SessionID, questionID, state.task.ID,
			proposal.ClientKey, proposal.Kind, strings.TrimSpace(proposal.Objective),
			strings.TrimSpace(proposal.RequiredCapability), strings.TrimSpace(proposal.ExpectedResult),
			criteria, proposal.Priority, state.run.GoalVersion, state.targetPlan,
			maxAttempts, timeout).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			var existingKind TaskKind
			var existingObjective, existingCapability, existingResult string
			err = tx.QueryRow(ctx, `
				SELECT id::text, kind, objective, required_capability, expected_result
				FROM research_task
				WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND client_key = $4
			`, state.run.SessionID, state.run.GoalVersion, state.targetPlan, proposal.ClientKey).Scan(
				&id, &existingKind, &existingObjective, &existingCapability, &existingResult,
			)
			if err == nil && (existingKind != proposal.Kind || existingObjective != strings.TrimSpace(proposal.Objective) || existingCapability != strings.TrimSpace(proposal.RequiredCapability) || existingResult != strings.TrimSpace(proposal.ExpectedResult)) {
				return 0, fmt.Errorf("%w: task key %q was reused for different content", ErrResultConflict, proposal.ClientKey)
			}
		} else if err == nil {
			created++
			taskIDs[proposal.ClientKey] = id
			if err = ensureDomainArtifactPassportWithAccessTx(ctx, tx, ArtifactKindTask, state.workspaceID, state.run.SessionID, id, time.Now(), int32Ptr(int32(state.run.GoalVersion)), int32Ptr(int32(state.targetPlan)), state.outputAccess); err != nil {
				return 0, err
			}
		}
		if err != nil {
			return 0, err
		}
	}
	for _, proposal := range proposals {
		for _, dependencyKey := range proposal.DependsOn {
			dependencyID, ok := taskIDs[dependencyKey]
			if !ok {
				return 0, fmt.Errorf("%w: task %q references unknown dependency %q", ErrInvalidResult, proposal.ClientKey, dependencyKey)
			}
			if _, err = tx.Exec(ctx, `
				INSERT INTO research_task_dependency (workspace_id, session_id, task_id, depends_on_task_id)
				VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid) ON CONFLICT DO NOTHING
			`, state.workspaceID, state.run.SessionID, taskIDs[proposal.ClientKey], dependencyID); err != nil {
				return 0, err
			}
		}
	}
	if usesEvidenceFitnessContract(state.run.OrchestratorVersion) {
		for _, proposal := range result.ProposedTasks {
			if _, err = tx.Exec(ctx, `
				INSERT INTO research_task_dependency (workspace_id, session_id, task_id, depends_on_task_id)
				VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid) ON CONFLICT DO NOTHING
			`, state.workspaceID, state.run.SessionID, taskIDs[proposal.ClientKey], state.task.ID); err != nil {
				return 0, err
			}
		}
	}
	if usesEvidenceFitnessContract(state.run.OrchestratorVersion) && isEvidenceTask(state.task.Kind) {
		if err = attachV4ProposedWorkToDelivery(ctx, tx, state, result.ProposedTasks, taskIDs); err != nil {
			return 0, err
		}
	}
	if cyclic, err := taskGraphHasCycle(ctx, tx, state.run.SessionID); err != nil {
		return 0, err
	} else if cyclic {
		return 0, fmt.Errorf("%w: persisted task dependency graph contains a cycle", ErrInvalidResult)
	}
	return created, nil
}

func attachV4ProposedWorkToDelivery(ctx context.Context, tx pgx.Tx, state acceptedResultState, proposals []TaskProposal, taskIDs map[string]string) error {
	blockingTaskIDs := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		if isEvidenceTask(proposal.Kind) || proposal.Kind == TaskKindReplan {
			blockingTaskIDs = append(blockingTaskIDs, taskIDs[proposal.ClientKey])
		}
	}
	if len(blockingTaskIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT synthesis.id::text
		FROM research_task synthesis
		JOIN research_task_dependency audit_dependency ON audit_dependency.depends_on_task_id = synthesis.id
		JOIN research_task audit ON audit.id = audit_dependency.task_id
		WHERE synthesis.session_id = $1::uuid
		  AND synthesis.goal_version = $2 AND synthesis.plan_version = $3
		  AND synthesis.kind = 'synthesize' AND synthesis.status IN ('pending', 'ready')
		  AND audit.kind IN ('quality_gate', 'citation_audit')
	`, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
	if err != nil {
		return err
	}
	deliveryTaskIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		deliveryTaskIDs = append(deliveryTaskIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, deliveryTaskID := range deliveryTaskIDs {
		for _, blockingTaskID := range blockingTaskIDs {
			if _, err = tx.Exec(ctx, `
				INSERT INTO research_task_dependency (workspace_id, session_id, task_id, depends_on_task_id)
				VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid) ON CONFLICT DO NOTHING
			`, state.workspaceID, state.run.SessionID, deliveryTaskID, blockingTaskID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `
			UPDATE research_task SET status = 'pending', ready_at = NULL, updated_at = now()
			WHERE id = $1::uuid AND status = 'ready'
		`, deliveryTaskID); err != nil {
			return err
		}
	}
	return nil
}

func loadTaskIDs(ctx context.Context, tx pgx.Tx, sessionID string, goalVersion, planVersion int) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT client_key, id::text FROM research_task WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3`, sessionID, goalVersion, planVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]string{}
	for rows.Next() {
		var key, id string
		if err = rows.Scan(&key, &id); err != nil {
			return nil, err
		}
		ids[key] = id
	}
	return ids, rows.Err()
}

func taskGraphHasCycle(ctx context.Context, tx pgx.Tx, sessionID string) (bool, error) {
	var cyclic bool
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE walk(task_id, dependency_id, path, cycle) AS (
		  SELECT d.task_id, d.depends_on_task_id, ARRAY[d.task_id, d.depends_on_task_id], d.task_id = d.depends_on_task_id
		  FROM research_task_dependency d
		  JOIN research_task t ON t.id = d.task_id
		  WHERE t.session_id = $1::uuid
		  UNION ALL
		  SELECT w.task_id, d.depends_on_task_id, w.path || d.depends_on_task_id,
		         d.depends_on_task_id = ANY(w.path)
		  FROM walk w
		  JOIN research_task_dependency d ON d.task_id = w.dependency_id
		  WHERE NOT w.cycle
		)
		SELECT EXISTS(SELECT 1 FROM walk WHERE cycle)
	`, sessionID).Scan(&cyclic)
	return cyclic, err
}

func materializeSources(ctx context.Context, tx pgx.Tx, state acceptedResultState, result ResultEnvelope) (map[string]string, int, error) {
	ids := map[string]string{}
	created := 0
	for _, source := range result.Sources {
		evidenceTraits := source.EvidenceTraits
		if evidenceTraits == nil {
			evidenceTraits = []string{}
		}
		canonical, err := CanonicalURL(source.URL)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: source %q: %v", ErrInvalidResult, source.ClientKey, err)
		}
		hash := sha256.Sum256([]byte(source.SnapshotText))
		contentHash := hex.EncodeToString(hash[:])
		verificationStatus := "pending"
		if state.verified {
			verificationStatus = "verified"
		}
		var id string
		var persistedRetrievedAt time.Time
		var existed bool
		if err = tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM research_source_snapshot
			  WHERE session_id = $1::uuid AND canonical_url = $2 AND content_hash = $3
			)
		`, state.run.SessionID, canonical, contentHash).Scan(&existed); err != nil {
			return nil, 0, err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO research_source_snapshot (
				workspace_id, session_id, produced_by_task_id, canonical_url,
				title, publisher, source_class, evidence_traits, independence_key, retrieved_at,
				snapshot_text, content_hash, metadata, verification_status
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::text[], $9, $10,
			          $11, $12, $13, $14)
			ON CONFLICT (session_id, canonical_url, content_hash)
			DO UPDATE SET
			  title = CASE WHEN research_source_snapshot.title = '' THEN EXCLUDED.title ELSE research_source_snapshot.title END,
			  evidence_traits = CASE
			    WHEN EXCLUDED.verification_status = 'verified' AND cardinality(EXCLUDED.evidence_traits) > 0 THEN EXCLUDED.evidence_traits
			    ELSE research_source_snapshot.evidence_traits
			  END,
			  verification_status = CASE WHEN EXCLUDED.verification_status = 'verified' THEN 'verified' ELSE research_source_snapshot.verification_status END
			RETURNING id::text, retrieved_at
		`, state.workspaceID, state.run.SessionID, state.task.ID, canonical,
			truncateBytes(source.Title, 4096), truncateBytes(source.Publisher, 1024),
			truncateBytes(source.SourceClass, 160), evidenceTraits, truncateBytes(source.IndependenceKey, 160),
			source.RetrievedAt, source.SnapshotText, contentHash,
			normalizeJSON(source.Metadata, `{}`), verificationStatus).Scan(&id, &persistedRetrievedAt)
		if err != nil {
			return nil, 0, err
		}
		ids[source.ClientKey] = id
		if !existed {
			created++
			metadata := normalizeJSON(source.Metadata, `{}`)
			versionHash, hashErr := ArtifactContentHash(ArtifactKindSourceSnapshot, map[string]any{
				"produced_by_task_id": state.task.ID,
				"canonical_url":       canonical,
				"title":               truncateBytes(source.Title, 4096),
				"publisher":           truncateBytes(source.Publisher, 1024),
				"source_class":        truncateBytes(source.SourceClass, 160),
				"evidence_traits":     evidenceTraits,
				"independence_key":    truncateBytes(source.IndependenceKey, 160),
				"retrieved_at":        persistedRetrievedAt,
				"snapshot_text":       source.SnapshotText,
				"content_hash":        contentHash,
				"metadata":            json.RawMessage(metadata),
				"verification_status": verificationStatus,
			})
			if hashErr != nil {
				return nil, 0, hashErr
			}
			if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
				WorkspaceID: state.workspaceID, SessionID: state.run.SessionID, EntityID: id,
				Kind: ArtifactKindSourceSnapshot, SourceCreatedAt: timePtr(time.Now()),
				ProvenanceCompleteness: ArtifactProvenanceComplete,
				AccessLevel:            state.outputAccess, HashOrigin: ArtifactHashOriginProduction,
				ContentHash: versionHash, ProducedByAttemptID: state.attemptID,
			}); err != nil {
				return nil, 0, err
			}
		}
		if err = recordVerificationPolicyMutationTx(ctx, tx, state.workspaceID, state.run.SessionID, id); err != nil {
			return nil, 0, err
		}
		payload, _ := json.Marshal(map[string]any{
			"snapshot_id": id, "publisher": source.Publisher,
			"independence_key": source.IndependenceKey, "retrieved_at": source.RetrievedAt,
			"verification_status": verificationStatus, "evidence_traits": evidenceTraits,
		})
		var legacySourceID string
		err = tx.QueryRow(ctx, `
			INSERT INTO research_source (
				workspace_id, session_id, url, title, source_class,
				credibility_weight, stance, relevance, summary, excerpt, payload,
				source_snapshot_id
			)
			SELECT $1::uuid, $2::uuid, $3, $4, $5, $6, '', 0.5, $7, $8, $9, $10::uuid
			WHERE NOT EXISTS (
			  SELECT 1 FROM research_source WHERE source_snapshot_id = $10::uuid
			)
			RETURNING id::text
		`, state.workspaceID, state.run.SessionID, canonical, truncateBytes(source.Title, 4096),
			truncateBytes(source.SourceClass, 160), sourceProjectionWeight(state.run.OrchestratorVersion, source.SourceClass),
			truncateBytes(result.Summary, 4096), truncateBytes(source.SnapshotText, 2000), payload, id).Scan(&legacySourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Legacy source projection already exists for this snapshot.
		} else if err != nil {
			return nil, 0, err
		} else if err = ensureDomainArtifactPassportWithAccessTx(ctx, tx, ArtifactKindLegacySource, state.workspaceID, state.run.SessionID, legacySourceID, time.Now(), nil, nil, state.outputAccess); err != nil {
			return nil, 0, err
		}
	}
	return ids, created, nil
}

func materializeObservations(ctx context.Context, tx pgx.Tx, state acceptedResultState, result ResultEnvelope, sourceIDs map[string]string) (map[string]string, int, error) {
	ids := map[string]string{}
	created := 0
	for _, observation := range result.Observations {
		sourceID, ok := sourceIDs[observation.SourceKey]
		if !ok {
			return nil, 0, fmt.Errorf("%w: unknown source %q", ErrInvalidResult, observation.SourceKey)
		}
		content, _ := json.Marshal(map[string]any{
			"quote": observation.Quote, "datum": json.RawMessage(observation.Datum),
			"locator": observation.Locator, "interpretation": observation.Interpretation,
		})
		hash := sha256.Sum256(content)
		verificationStatus := "pending"
		if state.verified {
			verificationStatus = "verified"
		}
		contentHash := hex.EncodeToString(hash[:])
		var id string
		var existed bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM research_observation
			  WHERE session_id = $1::uuid AND source_snapshot_id = $2::uuid AND content_hash = $3
			)
		`, state.run.SessionID, sourceID, contentHash).Scan(&existed); err != nil {
			return nil, 0, err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO research_observation (
				workspace_id, session_id, source_snapshot_id, produced_by_task_id,
				quote, datum, locator, interpretation, content_hash, verification_status
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (session_id, source_snapshot_id, content_hash)
			DO UPDATE SET
			  interpretation = CASE WHEN research_observation.interpretation = '' THEN EXCLUDED.interpretation ELSE research_observation.interpretation END,
			  verification_status = CASE WHEN EXCLUDED.verification_status = 'verified' THEN 'verified' ELSE research_observation.verification_status END
			RETURNING id::text
		`, state.workspaceID, state.run.SessionID, sourceID, state.task.ID,
			observation.Quote, normalizeJSON(observation.Datum, `{}`),
			truncateBytes(observation.Locator, 1024), truncateBytes(observation.Interpretation, 8192),
			contentHash, verificationStatus).Scan(&id)
		if err != nil {
			return nil, 0, err
		}
		ids[observation.ClientKey] = id
		if !existed {
			created++
			datum := normalizeJSON(observation.Datum, `{}`)
			versionHash, hashErr := ArtifactContentHash(ArtifactKindObservation, map[string]any{
				"source_snapshot_id":  sourceID,
				"produced_by_task_id": state.task.ID,
				"quote":               observation.Quote,
				"datum":               json.RawMessage(datum),
				"locator":             truncateBytes(observation.Locator, 1024),
				"interpretation":      truncateBytes(observation.Interpretation, 8192),
				"content_hash":        contentHash,
				"verification_status": verificationStatus,
			})
			if hashErr != nil {
				return nil, 0, hashErr
			}
			if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
				WorkspaceID: state.workspaceID, SessionID: state.run.SessionID, EntityID: id,
				Kind: ArtifactKindObservation, SourceCreatedAt: timePtr(time.Now()),
				ProvenanceCompleteness: ArtifactProvenanceComplete,
				AccessLevel:            state.outputAccess, HashOrigin: ArtifactHashOriginProduction,
				ContentHash: versionHash, ProducedByAttemptID: state.attemptID,
			}); err != nil {
				return nil, 0, err
			}
		}
		if err = recordVerificationPolicyMutationTx(ctx, tx, state.workspaceID, state.run.SessionID, id); err != nil {
			return nil, 0, err
		}
	}
	return ids, created, nil
}

func materializeClaims(ctx context.Context, tx pgx.Tx, state acceptedResultState, result ResultEnvelope, observationIDs map[string]string) (map[string]string, int, error) {
	ids := map[string]string{}
	created := 0
	standards := map[string]EvidenceStandard{}
	if usesEvidenceFitnessContract(state.run.OrchestratorVersion) && len(result.Claims) > 0 {
		method, err := loadResearchMethodVersion(ctx, tx, state.run.SessionID, state.task.GoalVersion, state.targetPlan)
		if err != nil {
			return nil, 0, err
		}
		for _, standard := range method.EvidenceStandards {
			standards[standard.ClientKey] = standard
		}
	}
	for _, claim := range result.Claims {
		if usesEvidenceFitnessContract(state.run.OrchestratorVersion) {
			if _, ok := standards[claim.EvidenceStandardKey]; !ok {
				return nil, 0, fmt.Errorf("%w: claim %q references unknown evidence standard %q", ErrInvalidResult, claim.ClientKey, claim.EvidenceStandardKey)
			}
		}
		status := claim.Status
		if status == "" {
			status = ClaimStatusProposed
		}
		adjudicated := (state.task.Kind == TaskKindVerify || state.task.Kind == TaskKindCounterSearch) && claim.Status != ""
		var id string
		var existed bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM research_claim
			  WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND client_key = $4
			)
		`, state.run.SessionID, state.task.GoalVersion, state.targetPlan, claim.ClientKey).Scan(&existed); err != nil {
			return nil, 0, err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO research_claim (
				workspace_id, session_id, produced_by_task_id, client_key, evidence_standard_key, claim_text,
				significance, confidence, status, goal_version, plan_version, resolution
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (session_id, goal_version, plan_version, client_key)
			DO UPDATE SET
			  evidence_standard_key = CASE WHEN research_claim.evidence_standard_key = '' THEN EXCLUDED.evidence_standard_key ELSE research_claim.evidence_standard_key END,
			  confidence = CASE WHEN $13::boolean THEN EXCLUDED.confidence ELSE GREATEST(research_claim.confidence, EXCLUDED.confidence) END,
			  status = CASE WHEN $13::boolean OR research_claim.status = 'proposed' THEN EXCLUDED.status ELSE research_claim.status END,
			  resolution = CASE WHEN $13::boolean AND EXCLUDED.resolution <> '' THEN EXCLUDED.resolution
			                    WHEN research_claim.resolution = '' THEN EXCLUDED.resolution ELSE research_claim.resolution END,
			  updated_at = now()
			WHERE research_claim.claim_text = EXCLUDED.claim_text
			  AND (research_claim.evidence_standard_key = '' OR EXCLUDED.evidence_standard_key = '' OR research_claim.evidence_standard_key = EXCLUDED.evidence_standard_key)
			RETURNING id::text
		`, state.workspaceID, state.run.SessionID, state.task.ID, claim.ClientKey,
			claim.EvidenceStandardKey, strings.TrimSpace(claim.Text), claim.Significance, claim.Confidence, status,
			state.task.GoalVersion, state.targetPlan, truncateBytes(claim.Resolution, 8192), adjudicated).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, fmt.Errorf("%w: claim key %q was reused for different text", ErrResultConflict, claim.ClientKey)
		}
		if err != nil {
			return nil, 0, err
		}
		ids[claim.ClientKey] = id
		if !existed {
			created++
			claimContent := map[string]any{
				"client_key":            claim.ClientKey,
				"evidence_standard_key": claim.EvidenceStandardKey,
				"claim_text":            strings.TrimSpace(claim.Text),
				"significance":          claim.Significance,
				"confidence":            claim.Confidence,
				"status":                string(status),
				"goal_version":          state.task.GoalVersion,
				"plan_version":          state.targetPlan,
				"resolution":            truncateBytes(claim.Resolution, 8192),
				"produced_by_task_id":   state.task.ID,
			}
			if err = ensureProducedDomainArtifactPassportWithAccessTx(
				ctx, tx, ArtifactKindClaim, state.workspaceID, state.run.SessionID, id,
				state.attemptID, time.Now(), int32Ptr(int32(state.task.GoalVersion)),
				int32Ptr(int32(state.targetPlan)), state.outputAccess, claimContent,
			); err != nil {
				return nil, 0, err
			}
		}
		for _, evidence := range claim.Evidence {
			observationID, ok := observationIDs[evidence.ObservationKey]
			if !ok {
				return nil, 0, fmt.Errorf("%w: unknown observation %q", ErrInvalidResult, evidence.ObservationKey)
			}
			verificationStatus := "pending"
			verifiedBy := ""
			if state.verified {
				verificationStatus = "verified"
				verifiedBy = state.task.ID
			}
			var evidenceID string
			if err = tx.QueryRow(ctx, `
				INSERT INTO research_claim_evidence (
					workspace_id, session_id, claim_id, observation_id, relation,
					strength, directness, method_fit, verification_status, verified_by_task_id, rationale
				) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9,
				          NULLIF($10, '')::uuid, $11)
				ON CONFLICT (claim_id, observation_id, relation)
				DO UPDATE SET
				  strength = CASE WHEN $12::boolean AND EXCLUDED.verification_status = 'verified' THEN EXCLUDED.strength ELSE GREATEST(research_claim_evidence.strength, EXCLUDED.strength) END,
				  directness = CASE WHEN $12::boolean AND EXCLUDED.verification_status = 'verified' THEN EXCLUDED.directness ELSE GREATEST(research_claim_evidence.directness, EXCLUDED.directness) END,
				  method_fit = CASE WHEN $12::boolean AND EXCLUDED.verification_status = 'verified' THEN EXCLUDED.method_fit ELSE GREATEST(research_claim_evidence.method_fit, EXCLUDED.method_fit) END,
				  verification_status = CASE WHEN EXCLUDED.verification_status = 'verified' THEN 'verified' ELSE research_claim_evidence.verification_status END,
				  verified_by_task_id = COALESCE(EXCLUDED.verified_by_task_id, research_claim_evidence.verified_by_task_id),
				  rationale = CASE WHEN EXCLUDED.rationale <> '' THEN EXCLUDED.rationale ELSE research_claim_evidence.rationale END,
				  updated_at = now()
				RETURNING id::text
			`, state.workspaceID, state.run.SessionID, id, observationID, evidence.Relation,
				evidence.Strength, evidence.Directness, evidence.MethodFit, verificationStatus, verifiedBy,
				truncateBytes(evidence.Rationale, 4096), usesEvidenceFitnessContract(state.run.OrchestratorVersion)).Scan(&evidenceID); err != nil {
				return nil, 0, err
			}
			evidenceContent := map[string]any{
				"claim_id":            id,
				"observation_id":      observationID,
				"relation":            evidence.Relation,
				"strength":            evidence.Strength,
				"directness":          evidence.Directness,
				"method_fit":          evidence.MethodFit,
				"verification_status": verificationStatus,
				"verified_by_task_id": verifiedBy,
				"rationale":           truncateBytes(evidence.Rationale, 4096),
			}
			if err = ensureProducedDomainArtifactPassportWithAccessTx(
				ctx, tx, ArtifactKindEvidenceLink, state.workspaceID, state.run.SessionID,
				evidenceID, state.attemptID, time.Now(), int32Ptr(int32(state.task.GoalVersion)),
				int32Ptr(int32(state.targetPlan)), state.outputAccess, evidenceContent,
			); err != nil {
				return nil, 0, err
			}
			if err = recordVerificationPolicyMutationTx(ctx, tx, state.workspaceID, state.run.SessionID, evidenceID); err != nil {
				return nil, 0, err
			}
		}
	}
	return ids, created, nil
}

func loadResearchMethodVersion(ctx context.Context, tx pgx.Tx, sessionID string, goalVersion, planVersion int) (ResearchMethod, error) {
	var outcome []byte
	err := tx.QueryRow(ctx, `
		SELECT outcome
		FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'research_method'
		  AND goal_version = $2 AND plan_version = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, goalVersion, planVersion).Scan(&outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResearchMethod{}, fmt.Errorf("%w: current plan has no accepted research method", ErrInvalidResult)
	}
	if err != nil {
		return ResearchMethod{}, err
	}
	var method ResearchMethod
	if err = json.Unmarshal(outcome, &method); err != nil {
		return ResearchMethod{}, fmt.Errorf("decode research method: %w", err)
	}
	return method, nil
}

func materializeReport(ctx context.Context, tx pgx.Tx, state acceptedResultState, report ReportProposal, resultClaimIDs map[string]string) (string, error) {
	var structuredReport reportStructuredV1
	var err error
	if usesStructuredResultContract(state.run.OrchestratorVersion) {
		structuredReport, err = validateStructuredReportV2(report, reportPolicyForDepth(state.run.DepthTier))
		if err != nil {
			return "", err
		}
	}
	var revision int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision), 0) + 1 FROM research_report WHERE session_id = $1::uuid`, state.run.SessionID).Scan(&revision); err != nil {
		return "", err
	}
	structuredJSON := normalizeJSON(report.Structured, `{}`)
	var reportID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_report (
			workspace_id, session_id, revision, content_md, structured,
			goal_version, plan_version, produced_by_task_id,
			produced_by_attempt_id, author_agent_id
		)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8::uuid, $9::uuid, NULLIF($10, '')::uuid)
		RETURNING id::text
	`, state.workspaceID, state.run.SessionID, revision, report.ContentMD, structuredJSON,
		state.run.GoalVersion, state.targetPlan, state.task.ID, state.attemptID,
		state.task.AssignedAgentID).Scan(&reportID); err != nil {
		return "", err
	}
	reportHash, err := ArtifactContentHash(ArtifactKindReportRevision, map[string]any{
		"revision":               revision,
		"content_md":             report.ContentMD,
		"structured":             json.RawMessage(structuredJSON),
		"goal_version":           state.run.GoalVersion,
		"plan_version":           state.targetPlan,
		"produced_by_task_id":    state.task.ID,
		"produced_by_attempt_id": state.attemptID,
		"author_agent_id":        state.task.AssignedAgentID,
	})
	if err != nil {
		return "", err
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: state.workspaceID, SessionID: state.run.SessionID, EntityID: reportID,
		Kind: ArtifactKindReportRevision, SourceCreatedAt: timePtr(time.Now()),
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            int32Ptr(int32(state.run.GoalVersion)), PlanVersion: int32Ptr(int32(state.targetPlan)),
		AccessLevel: state.outputAccess, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: reportHash, ProducedByAttemptID: state.attemptID,
	}); err != nil {
		return "", err
	}
	sections := map[string]reportStructuredSection{}
	citations := map[string]reportStructuredCitation{}
	if usesStructuredResultContract(state.run.OrchestratorVersion) {
		for _, section := range structuredReport.Sections {
			sections[section.ID] = section
		}
		for _, citation := range structuredReport.Citations {
			citations[citation.ID] = citation
		}
	}
	for _, link := range report.Claims {
		claimID := resultClaimIDs[link.ClaimKey]
		if claimID == "" {
			if err := tx.QueryRow(ctx, `
				SELECT id::text FROM research_claim
				WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND client_key = $4
				LIMIT 1
			`, state.run.SessionID, state.run.GoalVersion, state.targetPlan, link.ClaimKey).Scan(&claimID); errors.Is(err, pgx.ErrNoRows) {
				return "", fmt.Errorf("%w: report references unknown claim %q", ErrInvalidResult, link.ClaimKey)
			} else if err != nil {
				return "", err
			}
		}
		if usesStructuredResultContract(state.run.OrchestratorVersion) {
			section := sections[link.SectionID]
			sourceIDs := make([]string, 0, len(section.CitationIDs))
			for _, citationID := range section.CitationIDs {
				sourceIDs = append(sourceIDs, citations[citationID].SourceID)
			}
			var supportedByCitedSource bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
				  SELECT 1
				  FROM research_claim_evidence evidence
				  JOIN research_observation observation ON observation.id = evidence.observation_id
				  JOIN research_source source ON source.source_snapshot_id = observation.source_snapshot_id
				  JOIN research_source_snapshot snapshot ON snapshot.id = observation.source_snapshot_id
				  WHERE evidence.claim_id = $1::uuid
				    AND evidence.relation = 'supports'
				    AND evidence.verification_status = 'verified'
				    AND observation.verification_status = 'verified'
				    AND snapshot.verification_status = 'verified'
				    AND source.id::text = ANY($2::text[])
				)
			`, claimID, sourceIDs).Scan(&supportedByCitedSource); err != nil {
				return "", err
			}
			if !supportedByCitedSource {
				return "", fmt.Errorf("%w: report claim %q lacks a cited verified supporting source", ErrInvalidResult, link.ClaimKey)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO research_report_claim (
				workspace_id, session_id, report_id, claim_id, section_id, anchor_quote
			)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6) ON CONFLICT DO NOTHING
		`, state.workspaceID, state.run.SessionID, reportID, claimID,
			truncateBytes(link.SectionID, 160), truncateBytes(link.AnchorQuote, 8192)); err != nil {
			return "", err
		}
	}
	return reportID, nil
}

func materializeEvaluation(ctx context.Context, tx pgx.Tx, state acceptedResultState, evaluation EvaluationProposal) error {
	outcome, err := json.Marshal(evaluation)
	if err != nil {
		return err
	}
	var reportID, reportAuthorID string
	var structuredRaw json.RawMessage
	if err := tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(author_agent_id::text, ''), structured
		FROM research_report
		WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3
		ORDER BY revision DESC LIMIT 1
	`, state.run.SessionID, state.run.GoalVersion, state.targetPlan).Scan(&reportID, &reportAuthorID, &structuredRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: evaluation requires a report", ErrInvalidResult)
		}
		return err
	}
	if usesStructuredResultContract(state.run.OrchestratorVersion) {
		if reportAuthorID == "" || reportAuthorID == state.task.AssignedAgentID {
			return fmt.Errorf("%w: report evaluation must be submitted by an agent other than the report author", ErrInvalidResult)
		}
		var structured reportStructuredV1
		if err := json.Unmarshal(structuredRaw, &structured); err != nil {
			return fmt.Errorf("%w: latest report structure is invalid: %v", ErrInvalidResult, err)
		}
		claimKeys, err := loadReportClaimKeys(ctx, tx, reportID)
		if err != nil {
			return err
		}
		sectionIDs := make([]string, 0, len(structured.Sections))
		for _, section := range structured.Sections {
			sectionIDs = append(sectionIDs, section.ID)
		}
		if !sameUniqueStringSet(evaluation.ReviewedClaimKeys, claimKeys) || !sameUniqueStringSet(evaluation.ReviewedSectionIDs, sectionIDs) {
			return fmt.Errorf("%w: evaluation review coverage does not match the latest report", ErrInvalidResult)
		}
		if state.run.OrchestratorVersion == OrchestratorVersionV5 {
			if err := validateEvaluationDefectsAgainstReport(evaluation, claimKeys, sectionIDs, minimumEvaluationScoreForDepth(state.run.DepthTier)); err != nil {
				return err
			}
		}
	}
	inputs, err := json.Marshal(map[string]any{"task_id": state.task.ID, "task_kind": state.task.Kind, "report_id": reportID})
	if err != nil {
		return err
	}
	rationale := truncateBytes(strings.Join(evaluation.Findings, "\n"), 8192)
	var decisionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO research_decision (
			workspace_id, session_id, decision_kind, actor_type, actor_id,
			goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, $3, 'agent', $4::uuid, $5, $6, $7, $8, $9)
		RETURNING id::text
	`, state.workspaceID, state.run.SessionID, state.task.Kind, state.task.AssignedAgentID,
		state.run.GoalVersion, state.targetPlan, inputs, outcome,
		rationale).Scan(&decisionID)
	if err != nil {
		return err
	}
	kind := artifactKindForDecision(string(state.task.Kind))
	contentHash, err := ArtifactContentHash(kind, evaluationDecisionArtifactContent(
		state.task.Kind, state.task.AssignedAgentID, state.run.GoalVersion,
		state.targetPlan, inputs, outcome, rationale,
	))
	if err != nil {
		return err
	}
	return registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID: state.workspaceID, SessionID: state.run.SessionID, EntityID: decisionID,
		Kind: kind, SourceCreatedAt: timePtr(time.Now()),
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            int32Ptr(int32(state.run.GoalVersion)), PlanVersion: int32Ptr(int32(state.targetPlan)),
		AccessLevel: state.outputAccess, HashOrigin: ArtifactHashOriginProduction,
		ContentHash: contentHash, ProducedByAttemptID: state.attemptID,
	})
}

func evaluationDecisionArtifactContent(
	decisionKind TaskKind,
	actorID string,
	goalVersion, planVersion int,
	inputs, outcome []byte,
	rationale string,
) map[string]any {
	return map[string]any{
		"decision_kind": string(decisionKind),
		"actor_type":    "agent",
		"actor_id":      actorID,
		"goal_version":  goalVersion,
		"plan_version":  planVersion,
		"inputs":        json.RawMessage(inputs),
		"outcome":       json.RawMessage(outcome),
		"rationale":     rationale,
	}
}

func loadReportClaimKeys(ctx context.Context, tx pgx.Tx, reportID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT claim.client_key
		FROM research_report_claim link
		JOIN research_claim claim ON claim.id = link.claim_id
		WHERE link.report_id = $1::uuid
		ORDER BY claim.client_key
	`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func updateQuestionProgress(ctx context.Context, tx pgx.Tx, state acceptedResultState, result ResultEnvelope, claimIDs map[string]string) error {
	if state.task.QuestionID == "" || state.stale {
		return nil
	}
	answerClaimID := ""
	if usesStructuredResultContract(state.run.OrchestratorVersion) {
		answerClaimID = claimIDs[result.AnswerClaimKey]
	} else {
		for _, claim := range result.Claims {
			if id := claimIDs[claim.ClientKey]; id != "" {
				answerClaimID = id
				break
			}
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE research_question
		SET coverage = LEAST(1, coverage + $2),
		    status = CASE
		      WHEN LEAST(1, coverage + $2) >= 0.8 AND NULLIF($3, '')::uuid IS NOT NULL THEN 'answered'
		      ELSE 'in_progress'
		    END,
		    answer_claim_id = COALESCE(NULLIF($3, '')::uuid, answer_claim_id),
		    updated_at = now()
		WHERE id = $1::uuid
	`, state.task.QuestionID, result.CoverageDelta, answerClaimID)
	return err
}

func verificationTask(kind TaskKind) bool {
	switch kind {
	case TaskKindVerify, TaskKindCounterSearch, TaskKindQualityGate, TaskKindCitationAudit:
		return true
	default:
		return false
	}
}

func stageForTask(kind TaskKind) string {
	switch kind {
	case TaskKindPlan, TaskKindReplan:
		return "s2_sources"
	case TaskKindDiscover, TaskKindDeepRead:
		return "s2_sources"
	case TaskKindVerify, TaskKindCounterSearch:
		return "s3_validation"
	case TaskKindSynthesize, TaskKindQualityGate, TaskKindCitationAudit:
		return "s4_delivery"
	default:
		return ""
	}
}

func sourceClassWeight(class string) float64 {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "primary", "official", "paper", "dataset", "legal":
		return 0.9
	case "secondary", "industry", "news":
		return 0.7
	case "community", "social":
		return 0.4
	default:
		return 0.5
	}
}

func sourceProjectionWeight(orchestratorVersion, class string) float64 {
	if usesEvidenceFitnessContract(orchestratorVersion) {
		return 0.5
	}
	return sourceClassWeight(class)
}

func classifyResultConstraint(err error) error {
	if strings.Contains(err.Error(), "research_task_attempt_client_request_id_key") {
		return fmt.Errorf("%w: client_request_id has already been used", ErrResultConflict)
	}
	return err
}

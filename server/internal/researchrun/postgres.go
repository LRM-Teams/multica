package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool        *pgxpool.Pool
	txFaultHook researchTxFaultHook
}

var (
	_ projectionEventStore  = (*PostgresStore)(nil)
	_ resultAcceptanceStore = (*PostgresStore)(nil)
	_ executionStore        = (*PostgresStore)(nil)
	_ failureStore          = (*PostgresStore)(nil)
	_ deliveryGateStore     = (*PostgresStore)(nil)
)

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func prepareRunInitialization(in StartInput, cfg RunConfig) (json.RawMessage, []byte, error) {
	if err := validateRunConfig(cfg); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.Goal) == "" || len(strings.TrimSpace(in.Goal)) > 32<<10 || len(strings.TrimSpace(in.Title)) > 1024 || len(strings.TrimSpace(in.Language)) > 64 {
		return nil, nil, fmt.Errorf("%w: invalid start contract text", ErrInvalidContract)
	}
	sourcePolicy, err := resolveContractObject(json.RawMessage(`{}`), in.SourcePolicy, "source_policy")
	if err != nil {
		return nil, nil, err
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, err
	}
	return sourcePolicy, configJSON, nil
}

func (s *PostgresStore) CreateRun(ctx context.Context, in StartInput, cfg RunConfig) (Run, RunEvent, error) {
	if strings.TrimSpace(in.SessionID) != "" {
		return Run{}, RunEvent{}, fmt.Errorf("%w: create run assigns the session id", ErrInvalidContract)
	}
	sourcePolicy, configJSON, err := prepareRunInitialization(in, cfg)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	productRound := in.ProductRound
	if productRound <= 0 {
		productRound = 1
	}
	productRoundBudget := in.ProductRoundBudget
	if productRoundBudget <= 0 {
		switch in.DepthTier {
		case "shallow":
			productRoundBudget = 2
		case "deep":
			productRoundBudget = 10
		default:
			productRoundBudget = 5
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, fleet_id, created_by, title, goal, status, current_stage,
			depth_tier, product_round, product_round_budget
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'running', 's1_plan', $6, $7, $8)
		RETURNING id::text
	`, in.WorkspaceID, in.FleetID, in.CreatedBy, strings.TrimSpace(in.Title),
		strings.TrimSpace(in.Goal), in.DepthTier, productRound, productRoundBudget).Scan(&in.SessionID); err != nil {
		return Run{}, RunEvent{}, err
	}
	event, err := initializeRunTx(ctx, tx, in, cfg, sourcePolicy, configJSON)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	run, err := loadRun(ctx, tx, in.SessionID, in.WorkspaceID, false)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, err
	}
	return run, event, nil
}

func (s *PostgresStore) InitializeRun(ctx context.Context, in StartInput, cfg RunConfig) (Run, RunEvent, error) {
	sourcePolicy, configJSON, err := prepareRunInitialization(in, cfg)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)

	run, err := loadRunForUpdate(ctx, tx, in.SessionID, in.WorkspaceID)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if run.InitializedAt != nil {
		return run, RunEvent{}, tx.Commit(ctx)
	}
	event, err := initializeRunTx(ctx, tx, in, cfg, sourcePolicy, configJSON)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	run, err = loadRun(ctx, tx, in.SessionID, in.WorkspaceID, false)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, err
	}
	return run, event, nil
}

func initializeRunTx(ctx context.Context, tx pgx.Tx, in StartInput, cfg RunConfig, sourcePolicy json.RawMessage, configJSON []byte) (RunEvent, error) {
	var err error
	if _, err = tx.Exec(ctx, `
		UPDATE research_session
		SET orchestrator_version = $2,
		    run_config = $3,
		    run_initialized_at = now(),
		    last_progress_at = now(),
		    next_reconcile_at = now(),
		    stop_reason = '',
		    last_error = '',
		    updated_at = now()
		WHERE id = $1::uuid
	`, in.SessionID, OrchestratorVersion, configJSON); err != nil {
		return RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_contract_revision (
			workspace_id, session_id, goal_version, goal, language,
			source_policy, run_limits, authored_by, reason
		) VALUES ($1::uuid, $2::uuid, 1, $3, $4, $5, $6, $7::uuid, 'run_started')
		ON CONFLICT (session_id, goal_version) DO NOTHING
	`, in.WorkspaceID, in.SessionID, strings.TrimSpace(in.Goal), strings.TrimSpace(in.Language),
		sourcePolicy, configJSON, in.CreatedBy); err != nil {
		return RunEvent{}, err
	}
	var rootQuestionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO research_question (
			workspace_id, session_id, client_key, kind, question, required,
			status, priority, impact, uncertainty, novelty, coverage,
			goal_version, plan_version
		) VALUES ($1::uuid, $2::uuid, 'root', 'dimension', $3, false,
		          'in_progress', 1, 1, 0.8, 1, 0, 1, 1)
		ON CONFLICT (session_id, goal_version, plan_version, client_key)
		DO UPDATE SET question = EXCLUDED.question
		RETURNING id::text
	`, in.WorkspaceID, in.SessionID, strings.TrimSpace(in.Goal)).Scan(&rootQuestionID)
	if err != nil {
		return RunEvent{}, err
	}
	objective := buildPlanningObjective(in.Goal, in.Language)
	expectedResult := expectedResultForTaskVersion(OrchestratorVersion, TaskKindPlan)
	acceptanceCriteria, _ := json.Marshal(map[string]any{"schema_version": resultSchemaVersionForOrchestrator(OrchestratorVersion)})
	var planTaskID string
	err = tx.QueryRow(ctx, `
		INSERT INTO research_task (
			workspace_id, session_id, question_id, client_key, kind, objective,
			required_capability, expected_result, acceptance_criteria, priority,
			status, assigned_agent_id, goal_version, plan_version, max_attempts,
			timeout_seconds, ready_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'plan:1', 'plan', $4,
		          'lead', $5, $6, 1,
		          'ready', NULLIF($7, '')::uuid, 1, 1, $8, $9, now())
		ON CONFLICT (session_id, goal_version, plan_version, client_key)
		DO UPDATE SET objective = EXCLUDED.objective
		RETURNING id::text
	`, in.WorkspaceID, in.SessionID, rootQuestionID, objective, expectedResult, acceptanceCriteria, in.LeadAgentID,
		cfg.MaxAttemptsPerTask, cfg.TaskTimeoutSeconds).Scan(&planTaskID)
	if err != nil {
		return RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "run_started", "run-started", "user", in.CreatedBy, map[string]any{
		"goal_version":         1,
		"plan_version":         1,
		"root_question_id":     rootQuestionID,
		"plan_task_id":         planTaskID,
		"orchestrator_version": OrchestratorVersion,
	})
	if err != nil {
		return RunEvent{}, err
	}
	return event, nil
}

func (s *PostgresStore) GetRun(ctx context.Context, sessionID, workspaceID string) (Run, error) {
	return loadRun(ctx, s.pool, sessionID, workspaceID, false)
}

func (s *PostgresStore) GetCurrentContract(ctx context.Context, sessionID, workspaceID string) (ResearchContract, error) {
	var contract ResearchContract
	err := s.pool.QueryRow(ctx, `
		SELECT revision.goal_version, revision.goal, revision.scope,
		       revision.audience, revision.freshness, revision.language,
		       revision.source_policy, revision.run_limits, revision.reason,
		       revision.created_at
		FROM research_contract_revision revision
		JOIN research_session session ON session.id = revision.session_id
		WHERE revision.session_id = $1::uuid AND session.workspace_id = $2::uuid
		ORDER BY revision.goal_version DESC
		LIMIT 1
	`, sessionID, workspaceID).Scan(
		&contract.GoalVersion, &contract.Goal, &contract.Scope,
		&contract.Audience, &contract.Freshness, &contract.Language,
		&contract.SourcePolicy, &contract.RunLimits, &contract.Reason,
		&contract.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResearchContract{}, ErrRunNotFound
	}
	return contract, err
}

func (s *PostgresStore) GetCurrentMethod(ctx context.Context, sessionID, workspaceID string) (*ResearchMethod, error) {
	var outcome []byte
	var createdAt time.Time
	var goalVersion, planVersion int
	var actorID, taskID string
	err := s.pool.QueryRow(ctx, `
		SELECT decision.outcome, decision.goal_version, decision.plan_version,
		       COALESCE(decision.actor_id::text, ''), COALESCE(decision.inputs->>'task_id', ''),
		       decision.created_at
		FROM research_decision decision
		JOIN research_session session ON session.id = decision.session_id
		WHERE decision.session_id = $1::uuid
		  AND decision.workspace_id = $2::uuid
		  AND decision.decision_kind = 'research_method'
		  AND decision.goal_version = session.goal_version
		  AND decision.plan_version = session.plan_version
		ORDER BY decision.created_at DESC, decision.id DESC
		LIMIT 1
	`, sessionID, workspaceID).Scan(&outcome, &goalVersion, &planVersion, &actorID, &taskID, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var method ResearchMethod
	if err = json.Unmarshal(outcome, &method); err != nil {
		return nil, fmt.Errorf("decode current research method: %w", err)
	}
	method.GoalVersion = goalVersion
	method.PlanVersion = planVersion
	method.CreatedByAgentID = actorID
	method.CreatedByTaskID = taskID
	method.CreatedAt = createdAt
	return &method, nil
}

func loadRunForUpdate(ctx context.Context, tx pgx.Tx, sessionID, workspaceID string) (Run, error) {
	if err := lockRunForMutation(ctx, tx, sessionID, workspaceID); err != nil {
		return Run{}, err
	}
	return loadRun(ctx, tx, sessionID, workspaceID, false)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadRun(ctx context.Context, q rowQuerier, sessionID, workspaceID string, forUpdate bool) (Run, error) {
	query := `
		SELECT id::text, workspace_id::text, fleet_id::text, created_by::text,
		       title, goal, status, current_stage, depth_tier,
		       goal_version, plan_version, state_version, orchestrator_version,
		       run_config, run_stats, run_initialized_at, last_progress_at, next_reconcile_at,
		       stop_reason, last_error
		FROM research_session
		WHERE id = $1::uuid AND workspace_id = $2::uuid`
	if forUpdate {
		query += " FOR UPDATE"
	}
	var run Run
	var configJSON, statsJSON []byte
	if err := q.QueryRow(ctx, query, sessionID, workspaceID).Scan(
		&run.SessionID, &run.WorkspaceID, &run.FleetID, &run.CreatedBy,
		&run.Title, &run.Goal, &run.Status, &run.CurrentStage, &run.DepthTier,
		&run.GoalVersion, &run.PlanVersion, &run.StateVersion, &run.OrchestratorVersion,
		&configJSON, &statsJSON, &run.InitializedAt, &run.LastProgressAt, &run.NextReconcileAt,
		&run.StopReason, &run.LastError,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, ErrRunNotFound
		}
		return Run{}, err
	}
	run.Config = DefaultRunConfig(run.DepthTier)
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &run.Config); err != nil {
			return Run{}, fmt.Errorf("decode run config: %w", err)
		}
	}
	if len(statsJSON) > 0 {
		if err := json.Unmarshal(statsJSON, &run.Stats); err != nil {
			return Run{}, fmt.Errorf("decode run stats: %w", err)
		}
	}
	return run, nil
}

func (s *PostgresStore) ListQuestions(ctx context.Context, sessionID string) ([]Question, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, session_id::text, COALESCE(parent_question_id::text, ''),
		       COALESCE(created_by_task_id::text, ''), client_key, kind, question,
		       required, status, priority, impact, uncertainty, novelty, coverage,
		       goal_version, plan_version, COALESCE(answer_claim_id::text, ''), terminal_explanation
		FROM research_question WHERE session_id = $1::uuid
		ORDER BY required DESC, priority DESC, created_at, id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		var item Question
		if err := rows.Scan(&item.ID, &item.SessionID, &item.ParentQuestionID, &item.CreatedByTaskID,
			&item.ClientKey, &item.Kind, &item.Question, &item.Required, &item.Status,
			&item.Priority, &item.Impact, &item.Uncertainty, &item.Novelty, &item.Coverage,
			&item.GoalVersion, &item.PlanVersion, &item.AnswerClaimID, &item.TerminalExplanation); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListTasks(ctx context.Context, sessionID string) ([]Task, error) {
	rows, err := s.pool.Query(ctx, taskSelectSQL+` WHERE t.session_id = $1::uuid ORDER BY t.priority DESC, t.created_at, t.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListAttempts(ctx context.Context, sessionID string) ([]Attempt, error) {
	rows, err := s.pool.Query(ctx, attemptSelectSQL+` WHERE a.session_id = $1::uuid ORDER BY a.created_at, a.id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		item, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListPendingCancellations(ctx context.Context, sessionID string) ([]PendingCancellation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(inbox_task_id::text, ''), dispatch_key, status,
		       dispatched_at, cancellation_requested_at
		FROM research_task_attempt
		WHERE session_id = $1::uuid
		  AND status IN ('cancelling', 'cancelled')
		  AND cancellation_completed_at IS NULL
		ORDER BY dispatched_at, id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingCancellation{}
	for rows.Next() {
		var item PendingCancellation
		if err = rows.Scan(&item.AttemptID, &item.InboxTaskID, &item.DispatchKey, &item.Status,
			&item.DispatchedAt, &item.CancellationRequestedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) MarkCancellationsRequested(ctx context.Context, sessionID string, requests []CancellationRequest) error {
	if len(requests) == 0 {
		return nil
	}
	tx, err := s.beginResearchTx(ctx, txOpAttemptCancelRequest, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return err
	}
	for _, request := range requests {
		command, updateErr := tx.Exec(ctx, `
			UPDATE research_task_attempt
			SET inbox_task_id = COALESCE(inbox_task_id, $3::uuid),
			    cancellation_requested_at = COALESCE(cancellation_requested_at, now()),
			    updated_at = now()
			WHERE session_id = $1::uuid
			  AND id = $2::uuid
			  AND status IN ('cancelling', 'cancelled')
			  AND cancellation_completed_at IS NULL
			  AND (inbox_task_id IS NULL OR inbox_task_id = $3::uuid)
		`, sessionID, request.AttemptID, request.InboxTaskID)
		if updateErr != nil {
			return updateErr
		}
		if command.RowsAffected() != 1 {
			var status, existingInboxID string
			var completedAt *time.Time
			stateErr := tx.QueryRow(ctx, `
				SELECT status, COALESCE(inbox_task_id::text, ''), cancellation_completed_at
				FROM research_task_attempt
				WHERE session_id = $1::uuid AND id = $2::uuid
				FOR UPDATE
			`, sessionID, request.AttemptID).Scan(&status, &existingInboxID, &completedAt)
			if stateErr != nil {
				return fmt.Errorf("%w: cancellation request changed concurrently", ErrInvalidTransition)
			}
			if existingInboxID == "" || request.InboxTaskID == "" || existingInboxID != request.InboxTaskID {
				return fmt.Errorf("%w: cancellation request Inbox identity does not match", ErrInvalidTransition)
			}
			// Another cancellation actor may have completed the same durable
			// fact after this actor's external Cancel call. Only a settled
			// cancellation terminal state with the exact Inbox identity is an
			// idempotent replay; a marker on any other state is corruption.
			if completedAt != nil && (status == string(AttemptStatusFailed) || status == string(AttemptStatusCancelled)) {
				continue
			}
			return fmt.Errorf("%w: cancellation request changed concurrently (status %s)", ErrInvalidTransition, status)
		}
	}
	return s.commitResearchTx(ctx, txOpAttemptCancelRequest, tx)
}

func (s *PostgresStore) CompleteCancellations(ctx context.Context, sessionID string, attemptIDs []string) ([]RunEvent, error) {
	if len(attemptIDs) == 0 {
		return nil, nil
	}
	tx, err := s.beginResearchTx(ctx, txOpAttemptCancelComplete, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, status, pending_failure_class,
		       pending_failure_diagnostics, pending_failure_retryable,
		       cancellation_completed_at
		FROM research_task_attempt
		WHERE session_id = $1::uuid
		  AND id::text = ANY($2::text[])
		ORDER BY id
		FOR UPDATE
	`, sessionID, attemptIDs)
	if err != nil {
		return nil, err
	}
	type completion struct {
		id, status, failureClass, diagnostics string
		retryable                             bool
		completedAt                           *time.Time
	}
	items := make([]completion, 0, len(attemptIDs))
	for rows.Next() {
		var item completion
		if err = rows.Scan(&item.id, &item.status, &item.failureClass, &item.diagnostics, &item.retryable, &item.completedAt); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(items) != len(attemptIDs) {
		return nil, fmt.Errorf("%w: cancellation attempts changed concurrently", ErrInvalidTransition)
	}
	events := make([]RunEvent, 0, len(items))
	for _, item := range items {
		if item.completedAt != nil {
			if item.status == string(AttemptStatusFailed) || item.status == string(AttemptStatusCancelled) {
				continue
			}
			return nil, fmt.Errorf("%w: attempt %s has cancellation completion in non-terminal status %s", ErrInvalidTransition, item.id, item.status)
		}
		if item.status != string(AttemptStatusCancelling) && item.status != string(AttemptStatusCancelled) {
			return nil, fmt.Errorf("%w: attempt %s is %s, not awaiting cancellation", ErrInvalidTransition, item.id, item.status)
		}
		if item.status == string(AttemptStatusCancelled) {
			if err = abandonAttemptCircuitProbesTx(ctx, tx, item.id); err != nil {
				return nil, err
			}
			command, updateErr := tx.Exec(ctx, `
				UPDATE research_task_attempt
				SET cancellation_completed_at = now(), updated_at = now()
				WHERE id = $1::uuid AND status = 'cancelled' AND cancellation_completed_at IS NULL
			`, item.id)
			if updateErr != nil {
				return nil, updateErr
			}
			if command.RowsAffected() != 1 {
				return nil, fmt.Errorf("%w: cancelled attempt changed concurrently", ErrInvalidTransition)
			}
			continue
		}
		event, failErr := failAttemptTx(ctx, tx, AttemptFailure{
			AttemptID: item.id, FailureClass: item.failureClass,
			Diagnostics: item.diagnostics, Retryable: item.retryable,
		})
		if failErr != nil {
			return nil, failErr
		}
		events = append(events, event)
	}
	if err = s.commitResearchTx(ctx, txOpAttemptCancelComplete, tx); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *PostgresStore) ListFleetMembers(ctx context.Context, sessionID, workspaceID string) ([]FleetMember, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.agent_id::text, m.role, m.status, m.is_lead,
		       COALESCE(agent.runtime_id::text, ''), COALESCE(runtime.provider, ''),
		       COALESCE(agent.model, ''), agent.runtime_mode,
		       COALESCE(runtime.pinned_version, ''),
		       COALESCE(runtime_state.provider_config_fingerprint, ''),
		       COALESCE(agent.runtime_config::text, ''), COALESCE(agent.custom_env::text, ''),
		       COALESCE(agent.custom_args::text, ''), COALESCE(agent.mcp_config::text, ''),
		       COALESCE(agent.thinking_level, ''),
		       COALESCE(agent.provider_block_detail, ''), agent.provider_blocked_until
		FROM research_fleet_member m
		JOIN research_session s ON s.fleet_id = m.fleet_id
		JOIN agent ON agent.id = m.agent_id AND agent.workspace_id = m.workspace_id
		LEFT JOIN agent_runtime runtime ON runtime.id = agent.runtime_id AND runtime.workspace_id = m.workspace_id
		LEFT JOIN agent_runtime_state runtime_state
		  ON runtime_state.agent_id = agent.id AND runtime_state.runtime_id = agent.runtime_id
		WHERE s.id = $1::uuid AND s.workspace_id = $2::uuid AND m.workspace_id = s.workspace_id
		ORDER BY m.is_lead DESC, m.created_at, m.id
	`, sessionID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FleetMember{}
	for rows.Next() {
		var item FleetMember
		var runtimeMode, pinnedVersion, providerFingerprint string
		var runtimeConfig, customEnv, customArgs, mcpConfig, thinkingLevel string
		var blockedUntil pgtype.Timestamptz
		item.ExecutionTarget.Adapter = "agent_inbox"
		if err := rows.Scan(&item.AgentID, &item.Role, &item.Status, &item.IsLead,
			&item.ExecutionTarget.RuntimeID, &item.ExecutionTarget.Provider,
			&item.ExecutionTarget.Model, &runtimeMode, &pinnedVersion,
			&providerFingerprint, &runtimeConfig, &customEnv, &customArgs, &mcpConfig,
			&thinkingLevel, &item.ProviderBlockDetail, &blockedUntil); err != nil {
			return nil, err
		}
		item.ExecutionTarget.AgentID = item.AgentID
		item.ExecutionTarget = FingerprintExecutionTarget(item.ExecutionTarget, ExecutionTargetConfigIdentity{
			RuntimeMode: runtimeMode, RuntimePinnedVersion: pinnedVersion,
			ProviderStateFingerprint: providerFingerprint, RuntimeConfig: runtimeConfig,
			CustomEnv: customEnv, CustomArgs: customArgs, MCPConfig: mcpConfig,
			ThinkingLevel: thinkingLevel,
		})
		if blockedUntil.Valid {
			until := blockedUntil.Time
			item.ProviderBlockedUntil = &until
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetTask(ctx context.Context, taskID, sessionID string) (Task, error) {
	row := s.pool.QueryRow(ctx, taskSelectSQL+` WHERE t.id = $1::uuid AND t.session_id = $2::uuid`, taskID, sessionID)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrRunNotFound
	}
	return task, err
}

func (s *PostgresStore) TaskContext(ctx context.Context, taskID, workspaceID string) (RunSnapshot, error) {
	var sessionID string
	if err := s.pool.QueryRow(ctx, `SELECT session_id::text FROM research_task WHERE id = $1::uuid AND workspace_id = $2::uuid`, taskID, workspaceID).Scan(&sessionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunSnapshot{}, ErrRunNotFound
		}
		return RunSnapshot{}, err
	}
	run, err := s.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	contract, err := s.GetCurrentContract(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	method, err := s.GetCurrentMethod(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	questions, err := s.ListQuestions(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	tasks, err := s.ListTasks(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	attempts, err := s.ListAttempts(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	sources, err := s.ListSourceSnapshots(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	observations, err := s.ListObservations(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	claims, err := s.ListClaims(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	gate, err := s.EvaluateGate(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	return RunSnapshot{
		Run: run, Contract: contract, Method: method, Questions: questions, Tasks: tasks, Attempts: attempts,
		Sources: sources, Observations: observations, Claims: claims, Gate: gate,
	}, nil
}

func (s *PostgresStore) ClaimRun(ctx context.Context, sessionID, token string, duration time.Duration) (Run, RunLease, bool, error) {
	if duration <= 0 {
		return Run{}, RunLease{}, false, fmt.Errorf("%w: reconcile lease duration must be positive", ErrInvalidTransition)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunLease{}, false, err
	}
	defer tx.Rollback(ctx)
	var workspaceID string
	lease := RunLease{SessionID: sessionID, Token: token}
	err = tx.QueryRow(ctx, `
		UPDATE research_session
		SET reconcile_lease_token = $2::uuid,
		    reconcile_lease_expires_at = now() + $3::interval,
		    reconcile_lease_generation = reconcile_lease_generation + 1,
		    updated_at = now()
		WHERE id = $1::uuid
		  AND (
		    status = 'running'
		    OR EXISTS (
		      SELECT 1 FROM research_task_attempt attempt
		      WHERE attempt.session_id = research_session.id
		        AND attempt.status IN ('cancelling', 'cancelled')
		        AND attempt.cancellation_completed_at IS NULL
		    )
		    OR EXISTS (
		      SELECT 1 FROM research_run_event event
		      WHERE event.session_id = research_session.id
		        AND event.projected_at IS NULL
		        AND event.next_projection_at <= now()
		    )
		  )
		  AND (reconcile_lease_expires_at IS NULL OR reconcile_lease_expires_at <= now())
		RETURNING workspace_id::text, reconcile_lease_generation, reconcile_lease_expires_at
	`, sessionID, token, duration.String()).Scan(&workspaceID, &lease.Generation, &lease.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, RunLease{}, false, nil
	}
	if err != nil {
		return Run{}, RunLease{}, false, err
	}
	run, err := loadRun(ctx, tx, sessionID, workspaceID, false)
	if err != nil {
		return Run{}, RunLease{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunLease{}, false, err
	}
	return run, lease, true, nil
}

func (s *PostgresStore) RenewRunLease(ctx context.Context, lease RunLease, duration time.Duration) (RunLease, error) {
	if duration <= 0 {
		return RunLease{}, fmt.Errorf("%w: reconcile lease duration must be positive", ErrInvalidTransition)
	}
	renewed := lease
	err := s.pool.QueryRow(ctx, `
		UPDATE research_session
		SET reconcile_lease_expires_at = now() + $4::interval,
		    updated_at = now()
		WHERE id = $1::uuid
		  AND reconcile_lease_token = $2::uuid
		  AND reconcile_lease_generation = $3
		  AND reconcile_lease_expires_at > now()
		RETURNING reconcile_lease_expires_at
	`, lease.SessionID, lease.Token, lease.Generation, duration.String()).Scan(&renewed.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunLease{}, ErrRunLeaseLost
	}
	return renewed, err
}

func (s *PostgresStore) ReleaseRun(ctx context.Context, lease RunLease, next time.Time) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE research_session
		SET reconcile_lease_token = NULL, reconcile_lease_expires_at = NULL,
		    next_reconcile_at = $4, updated_at = now()
		WHERE id = $1::uuid
		  AND reconcile_lease_token = $2::uuid
		  AND reconcile_lease_generation = $3
		  AND reconcile_lease_expires_at > now()
	`, lease.SessionID, lease.Token, lease.Generation, next)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrRunLeaseLost
	}
	return nil
}

func (s *PostgresStore) ListDueRunIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text FROM research_session
		WHERE run_initialized_at IS NOT NULL
		  AND (
		    (status = 'running' AND next_reconcile_at <= now())
		    OR EXISTS (
		      SELECT 1 FROM research_task_attempt attempt
		      WHERE attempt.session_id = research_session.id
		        AND attempt.status IN ('cancelling', 'cancelled')
		        AND attempt.cancellation_completed_at IS NULL
		    )
		    OR EXISTS (
		      SELECT 1 FROM research_run_event event
		      WHERE event.session_id = research_session.id
		        AND event.projected_at IS NULL
		        AND event.next_projection_at <= now()
		    )
		  )
		ORDER BY LEAST(
		  CASE WHEN status = 'running' THEN next_reconcile_at ELSE 'infinity'::timestamptz END,
		  COALESCE((
		    SELECT min(event.next_projection_at)
		    FROM research_run_event event
		    WHERE event.session_id = research_session.id AND event.projected_at IS NULL
		  ), 'infinity'::timestamptz)
		), id LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const taskSelectSQL = `
	SELECT t.id::text, t.session_id::text, t.workspace_id::text,
	       COALESCE(t.question_id::text, ''), COALESCE(t.parent_task_id::text, ''),
	       t.client_key, t.kind, t.objective, t.required_capability,
	       t.expected_result, t.acceptance_criteria, t.priority, t.status,
	       COALESCE(t.assigned_agent_id::text, ''), t.goal_version, t.plan_version,
	       t.max_attempts, t.timeout_seconds,
	       (SELECT count(*)::int FROM research_task_attempt a WHERE a.task_id = t.id),
	       t.ready_at, t.started_at, t.completed_at, t.terminal_reason
	FROM research_task t`

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (Task, error) {
	var item Task
	err := row.Scan(&item.ID, &item.SessionID, &item.WorkspaceID, &item.QuestionID, &item.ParentTaskID,
		&item.ClientKey, &item.Kind, &item.Objective, &item.RequiredCapability,
		&item.ExpectedResult, &item.AcceptanceCriteria, &item.Priority, &item.Status,
		&item.AssignedAgentID, &item.GoalVersion, &item.PlanVersion, &item.MaxAttempts,
		&item.TimeoutSeconds, &item.AttemptCount, &item.ReadyAt, &item.StartedAt,
		&item.CompletedAt, &item.TerminalReason)
	return item, err
}

const attemptSelectSQL = `
	SELECT a.id::text, a.session_id::text, a.workspace_id::text, a.task_id::text,
	       a.attempt_number, a.assigned_agent_id::text,
	       a.execution_adapter, COALESCE(a.runtime_id::text, ''), a.provider,
	       a.model, a.target_config_fingerprint, a.agent_config_fingerprint,
	       a.runtime_config_fingerprint, a.provider_config_fingerprint,
	       COALESCE(a.inbox_task_id::text, ''), a.dispatch_key,
	       COALESCE(a.client_request_id, ''), a.status, COALESCE(a.result_hash, ''),
	       a.failure_class, a.source_failure_reason, a.diagnostics, a.dispatched_at, a.started_at,
	       a.runtime_started_at, a.runtime_last_observed_at, a.runtime_lease_expires_at,
	       a.cancellation_requested_at, a.cancellation_completed_at,
	       a.pending_failure_class, a.pending_failure_diagnostics,
	       a.pending_failure_retryable, a.result_submitted_at, a.completed_at
	FROM research_task_attempt a`

func scanAttempt(row scanner) (Attempt, error) {
	var item Attempt
	err := row.Scan(&item.ID, &item.SessionID, &item.WorkspaceID, &item.TaskID,
		&item.AttemptNumber, &item.AssignedAgentID, &item.ExecutionTarget.Adapter,
		&item.ExecutionTarget.RuntimeID, &item.ExecutionTarget.Provider,
		&item.ExecutionTarget.Model, &item.ExecutionTarget.ConfigFingerprint,
		&item.ExecutionTarget.AgentConfigFingerprint,
		&item.ExecutionTarget.RuntimeConfigFingerprint,
		&item.ExecutionTarget.ProviderConfigFingerprint,
		&item.InboxTaskID, &item.DispatchKey,
		&item.ClientRequestID, &item.Status, &item.ResultHash, &item.FailureClass,
		&item.SourceFailureReason, &item.Diagnostics, &item.DispatchedAt, &item.StartedAt, &item.RuntimeStartedAt,
		&item.RuntimeObservedAt, &item.RuntimeLeaseUntil, &item.CancelRequestedAt,
		&item.CancelCompletedAt, &item.PendingFailure, &item.PendingDiagnostics,
		&item.PendingRetryable, &item.ResultSubmittedAt,
		&item.CompletedAt)
	item.ExecutionTarget.AgentID = item.AssignedAgentID
	return item, err
}

func appendEvent(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, eventType, key, actorType, actorID string, payload any) (RunEvent, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return RunEvent{}, err
	}
	if err = lockRunForMutation(ctx, tx, sessionID, workspaceID); err != nil {
		return RunEvent{}, err
	}
	var existing RunEvent
	err = tx.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, sequence,
		       event_type, idempotency_key, actor_type, COALESCE(actor_id::text, ''),
		       payload, projection_attempts, created_at
		FROM research_run_event
		WHERE session_id = $1::uuid AND idempotency_key = $2
	`, sessionID, key).Scan(
		&existing.ID, &existing.WorkspaceID, &existing.SessionID, &existing.Sequence,
		&existing.Type, &existing.IdempotencyKey, &existing.ActorType, &existing.ActorID,
		&existing.Payload, &existing.ProjectionAttempts, &existing.CreatedAt,
	)
	if err == nil {
		if existing.Type != eventType || existing.ActorType != actorType || existing.ActorID != actorID || !semanticJSONEqual(existing.Payload, encoded) {
			return RunEvent{}, fmt.Errorf("%w: event idempotency key %q was reused for different content", ErrResultConflict, key)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, err
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `
		UPDATE research_session SET state_version = state_version + 1, updated_at = now()
		WHERE id = $1::uuid AND workspace_id = $2::uuid
		RETURNING state_version
	`, sessionID, workspaceID).Scan(&sequence); err != nil {
		return RunEvent{}, err
	}
	var event RunEvent
	err = tx.QueryRow(ctx, `
		INSERT INTO research_run_event (
			workspace_id, session_id, sequence, event_type, idempotency_key,
			actor_type, actor_id, payload
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, NULLIF($7, '')::uuid, $8)
		RETURNING id::text, workspace_id::text, session_id::text, sequence,
		          event_type, idempotency_key, actor_type, COALESCE(actor_id::text, ''),
		          payload, created_at
	`, workspaceID, sessionID, sequence, eventType, key, actorType, actorID, encoded).Scan(
		&event.ID, &event.WorkspaceID, &event.SessionID, &event.Sequence, &event.Type,
		&event.IdempotencyKey, &event.ActorType, &event.ActorID, &event.Payload, &event.CreatedAt,
	)
	return event, err
}

func semanticJSONEqual(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func normalizeJSON(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(fallback)
	}
	return raw
}

func buildPlanningObjective(goal, language string) string {
	if strings.TrimSpace(language) == "" {
		language = "follow the user's language"
	}
	return fmt.Sprintf("Create an adaptive, evidence-oriented research plan for this goal:\n\n%s\n\nDelivery language: %s. Start wide, identify independent perspectives, define required questions, source strategy, inclusion/exclusion criteria, uncertainties, and a dependency-safe task graph. Return only through the structured Research Run result command.", strings.TrimSpace(goal), strings.TrimSpace(language))
}

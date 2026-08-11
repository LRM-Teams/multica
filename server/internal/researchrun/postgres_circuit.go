package researchrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const circuitColumnsSQL = `
	id::text, workspace_id::text, scope, target_key, target_label,
	config_fingerprint, state, generation, consecutive_failures,
	window_started_at, opened_at, next_probe_at,
	COALESCE(probe_token::text, ''), probe_lease_expires_at,
	last_failure_class, last_source_reason, last_diagnostics,
	COALESCE(last_attempt_id::text, ''), COALESCE(last_session_id::text, ''),
	last_failed_at, last_succeeded_at`

const circuitSelectSQL = `SELECT ` + circuitColumnsSQL + ` FROM research_execution_circuit`

func (s *PostgresStore) RecordCircuitFailure(ctx context.Context, in CircuitFailureInput) (ExecutionCircuit, []CircuitTransition, error) {
	policy, ok := policyForCircuitFailure(in.Disposition)
	if !ok || strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.AttemptID) == "" {
		return ExecutionCircuit{}, nil, fmt.Errorf("%w: invalid circuit failure", ErrInvalidTransition)
	}
	target, err := CircuitTargetForExecution(in.Target, in.Disposition.CircuitScope)
	if err != nil {
		return ExecutionCircuit{}, nil, err
	}
	tx, err := s.beginResearchTx(ctx, txOpCircuitFailure, pgx.TxOptions{})
	if err != nil {
		return ExecutionCircuit{}, nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.SessionID, in.WorkspaceID); err != nil {
		return ExecutionCircuit{}, nil, err
	}
	if err = assertAttemptCircuitTarget(ctx, tx, in, target); err != nil {
		return ExecutionCircuit{}, nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_execution_circuit (
		  workspace_id, scope, target_key, target_label, config_fingerprint
		) VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, scope, target_key) DO NOTHING
	`, in.WorkspaceID, target.Scope, target.Key, truncateBytes(target.Label, 240), target.ConfigFingerprint); err != nil {
		return ExecutionCircuit{}, nil, err
	}
	circuit, err := loadCircuitForUpdate(ctx, tx, in.WorkspaceID, target)
	if err != nil {
		return ExecutionCircuit{}, nil, err
	}
	if existing, found, findErr := findAttemptCircuitTransition(ctx, tx, circuit.ID, in.AttemptID, "failure_observed"); findErr != nil {
		return ExecutionCircuit{}, nil, findErr
	} else if found {
		return circuit, []CircuitTransition{existing}, s.commitResearchTx(ctx, txOpCircuitFailure, tx)
	}
	transitions := []CircuitTransition{}
	if target.ConfigFingerprint != "" && circuit.Target.ConfigFingerprint != target.ConfigFingerprint {
		transition, reset, resetErr := resetCircuitForConfiguration(ctx, tx, circuit, in.SessionID, in.AttemptID, target.ConfigFingerprint)
		if resetErr != nil {
			return ExecutionCircuit{}, nil, resetErr
		}
		transitions = append(transitions, transition)
		circuit = reset
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return ExecutionCircuit{}, nil, err
	}
	from := circuit.State
	windowStarted := databaseNow
	failures := 1
	if circuit.WindowStartedAt != nil && circuit.LastFailedAt != nil && databaseNow.Sub(*circuit.WindowStartedAt) <= policy.Window {
		windowStarted = *circuit.WindowStartedAt
		failures = circuit.ConsecutiveFailures + 1
	}
	to := from
	var openedAt, nextProbeAt *time.Time
	if circuit.OpenedAt != nil {
		value := *circuit.OpenedAt
		openedAt = &value
	}
	if circuit.NextProbeAt != nil {
		value := *circuit.NextProbeAt
		nextProbeAt = &value
	}
	shouldOpen := in.Disposition.ImmediateOpen || failures >= policy.Threshold || from == CircuitOpen || from == CircuitHalfOpen
	if shouldOpen {
		to = CircuitOpen
		if openedAt == nil || from == CircuitClosed {
			value := databaseNow
			openedAt = &value
		}
		candidate := databaseNow.Add(policy.OpenDuration)
		if nextProbeAt == nil || !nextProbeAt.After(databaseNow) {
			nextProbeAt = &candidate
		}
	} else {
		openedAt = nil
		nextProbeAt = nil
	}
	generation := circuit.Generation + 1
	err = tx.QueryRow(ctx, `
		UPDATE research_execution_circuit
		SET target_label = $2, config_fingerprint = $3, state = $4,
		    generation = $5, consecutive_failures = $6, window_started_at = $7,
		    opened_at = $8, next_probe_at = $9,
		    probe_token = NULL, probe_lease_expires_at = NULL,
		    last_failure_class = $10, last_source_reason = $11,
		    last_diagnostics = $12, last_attempt_id = $13::uuid,
		    last_session_id = $14::uuid, last_failed_at = $15, updated_at = $15
		WHERE id = $1::uuid
		RETURNING `+circuitColumnsSQL,
		circuit.ID, truncateBytes(target.Label, 240), target.ConfigFingerprint, to,
		generation, failures, windowStarted, openedAt, nextProbeAt,
		truncateBytes(string(in.Disposition.Class), 160), truncateBytes(in.SourceReason, 160),
		truncateBytes(in.Diagnostics, 4096), in.AttemptID, in.SessionID, databaseNow,
	).Scan(circuitScanDestinations(&circuit)...)
	if err != nil {
		return ExecutionCircuit{}, nil, err
	}
	transition, err := recordCircuitTransitionTx(ctx, tx, circuit, in.SessionID, in.AttemptID, from, to,
		"failure_observed", in.Disposition.Class, in.SourceReason, in.Diagnostics)
	if err != nil {
		return ExecutionCircuit{}, nil, err
	}
	transitions = append(transitions, transition)
	if err = s.commitResearchTx(ctx, txOpCircuitFailure, tx); err != nil {
		return ExecutionCircuit{}, nil, err
	}
	return circuit, transitions, nil
}

// RecordCircuitSuccess clears a closed circuit's transient failure window. It
// deliberately cannot close open or half-open state: recovery from those
// states is fenced by ResolveCircuitProbe and its unique probe lease.
func (s *PostgresStore) RecordCircuitSuccess(ctx context.Context, in CircuitSuccessInput) (ExecutionCircuit, CircuitTransition, bool, error) {
	if strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.AttemptID) == "" {
		return ExecutionCircuit{}, CircuitTransition{}, false, fmt.Errorf("%w: invalid circuit success", ErrInvalidTransition)
	}
	target, err := CircuitTargetForExecution(in.Target, in.Scope)
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	tx, err := s.beginResearchTx(ctx, txOpCircuitSuccess, pgx.TxOptions{})
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.SessionID, in.WorkspaceID); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	if err = assertFrozenAttemptCircuitTarget(ctx, tx, in.WorkspaceID, in.SessionID, in.AttemptID, target); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	circuit, err := loadCircuitForUpdate(ctx, tx, in.WorkspaceID, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionCircuit{}, CircuitTransition{}, false, s.commitResearchTx(ctx, txOpCircuitSuccess, tx)
	}
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	if existing, found, findErr := findAttemptCircuitTransition(ctx, tx, circuit.ID, in.AttemptID, "success_observed"); findErr != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, findErr
	} else if found {
		return circuit, existing, false, s.commitResearchTx(ctx, txOpCircuitSuccess, tx)
	}
	if target.ConfigFingerprint != "" && circuit.Target.ConfigFingerprint != target.ConfigFingerprint {
		transition, reset, resetErr := resetCircuitForConfiguration(ctx, tx, circuit, in.SessionID, in.AttemptID, target.ConfigFingerprint)
		if resetErr != nil {
			return ExecutionCircuit{}, CircuitTransition{}, false, resetErr
		}
		if err = s.commitResearchTx(ctx, txOpCircuitSuccess, tx); err != nil {
			return ExecutionCircuit{}, CircuitTransition{}, false, err
		}
		return reset, transition, true, nil
	}
	if circuit.State != CircuitClosed || (circuit.ConsecutiveFailures == 0 && circuit.WindowStartedAt == nil) {
		return circuit, CircuitTransition{}, false, s.commitResearchTx(ctx, txOpCircuitSuccess, tx)
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	from := circuit.State
	circuit.Generation++
	err = tx.QueryRow(ctx, `
		UPDATE research_execution_circuit
		SET generation = $2, consecutive_failures = 0, window_started_at = NULL,
		    last_attempt_id = $3::uuid, last_session_id = $4::uuid,
		    last_succeeded_at = $5, updated_at = $5
		WHERE id = $1::uuid
		RETURNING `+circuitColumnsSQL,
		circuit.ID, circuit.Generation, in.AttemptID, in.SessionID, databaseNow,
	).Scan(circuitScanDestinations(&circuit)...)
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	transition, err := recordCircuitTransitionTx(ctx, tx, circuit, in.SessionID, in.AttemptID,
		from, CircuitClosed, "success_observed", "", "", "")
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	if err = s.commitResearchTx(ctx, txOpCircuitSuccess, tx); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, false, err
	}
	return circuit, transition, true, nil
}

func (s *PostgresStore) ClaimCircuitProbe(ctx context.Context, workspaceID, sessionID string, target CircuitTarget, token string, leaseDuration time.Duration) (CircuitProbeLease, bool, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(token) == "" || leaseDuration <= 0 {
		return CircuitProbeLease{}, false, fmt.Errorf("%w: invalid circuit probe claim", ErrInvalidTransition)
	}
	tx, err := s.beginResearchTx(ctx, txOpCircuitProbeClaim, pgx.TxOptions{})
	if err != nil {
		return CircuitProbeLease{}, false, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, sessionID, workspaceID); err != nil {
		return CircuitProbeLease{}, false, err
	}
	circuit, err := loadCircuitForUpdate(ctx, tx, workspaceID, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return CircuitProbeLease{}, false, s.commitResearchTx(ctx, txOpCircuitProbeClaim, tx)
	}
	if err != nil {
		return CircuitProbeLease{}, false, err
	}
	if target.ConfigFingerprint != "" && circuit.Target.ConfigFingerprint != target.ConfigFingerprint {
		// A probe claim is not proof that the caller's target snapshot is the
		// current configuration. Attempt-bound success/failure observations
		// perform the audited reset after matching the immutable target.
		return CircuitProbeLease{}, false, s.commitResearchTx(ctx, txOpCircuitProbeClaim, tx)
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return CircuitProbeLease{}, false, err
	}
	claimable := circuit.State == CircuitOpen && circuit.NextProbeAt != nil && !circuit.NextProbeAt.After(databaseNow)
	if circuit.State == CircuitHalfOpen && circuit.ProbeLeaseExpiresAt != nil && !circuit.ProbeLeaseExpiresAt.After(databaseNow) {
		claimable = true
	}
	if !claimable {
		return CircuitProbeLease{}, false, s.commitResearchTx(ctx, txOpCircuitProbeClaim, tx)
	}
	from := circuit.State
	generation := circuit.Generation + 1
	expiresAt := databaseNow.Add(leaseDuration)
	if _, err = tx.Exec(ctx, `
		UPDATE research_execution_circuit
		SET state = 'half_open', generation = $2, probe_token = $3::uuid,
		    probe_lease_expires_at = $4, updated_at = $5
		WHERE id = $1::uuid
	`, circuit.ID, generation, token, expiresAt, databaseNow); err != nil {
		return CircuitProbeLease{}, false, err
	}
	circuit.State, circuit.Generation, circuit.ProbeToken, circuit.ProbeLeaseExpiresAt = CircuitHalfOpen, generation, token, &expiresAt
	if _, err = recordCircuitTransitionTx(ctx, tx, circuit, sessionID, "", from, CircuitHalfOpen,
		"probe_claimed", "", "", ""); err != nil {
		return CircuitProbeLease{}, false, err
	}
	if err = s.commitResearchTx(ctx, txOpCircuitProbeClaim, tx); err != nil {
		return CircuitProbeLease{}, false, err
	}
	return CircuitProbeLease{CircuitID: circuit.ID, WorkspaceID: workspaceID, SessionID: sessionID,
		Target: target, Token: token, Generation: generation, ExpiresAt: expiresAt}, true, nil
}

func (s *PostgresStore) ResolveCircuitProbe(ctx context.Context, lease CircuitProbeLease, success bool, disposition FailureDisposition, sourceReason, diagnostics string) (ExecutionCircuit, CircuitTransition, error) {
	if lease.CircuitID == "" || lease.WorkspaceID == "" || lease.SessionID == "" || lease.Token == "" {
		return ExecutionCircuit{}, CircuitTransition{}, fmt.Errorf("%w: invalid circuit probe result", ErrInvalidTransition)
	}
	policy, policyOK := policyForCircuitFailure(disposition)
	if !success && (!policyOK || disposition.CircuitScope != lease.Target.Scope) {
		return ExecutionCircuit{}, CircuitTransition{}, fmt.Errorf("%w: invalid failed probe policy", ErrInvalidTransition)
	}
	tx, err := s.beginResearchTx(ctx, txOpCircuitProbeResolve, pgx.TxOptions{})
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, lease.SessionID, lease.WorkspaceID); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	row := tx.QueryRow(ctx, circuitSelectSQL+` WHERE id = $1::uuid FOR UPDATE`, lease.CircuitID)
	circuit, err := scanCircuit(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionCircuit{}, CircuitTransition{}, ErrCircuitProbeLeaseLost
	}
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	if circuit.WorkspaceID != lease.WorkspaceID || circuit.Target.Scope != lease.Target.Scope ||
		circuit.Target.Key != lease.Target.Key || circuit.Target.ConfigFingerprint != lease.Target.ConfigFingerprint ||
		circuit.State != CircuitHalfOpen || circuit.Generation != lease.Generation ||
		circuit.ProbeToken != lease.Token || circuit.ProbeLeaseExpiresAt == nil || !circuit.ProbeLeaseExpiresAt.After(databaseNow) {
		return ExecutionCircuit{}, CircuitTransition{}, ErrCircuitProbeLeaseLost
	}
	from := circuit.State
	to, cause := CircuitClosed, "probe_succeeded"
	failures := 0
	var windowStartedAt, openedAt, nextProbeAt *time.Time
	lastClass, lastSource, lastDiagnostics := circuit.LastFailureClass, circuit.LastSourceReason, circuit.LastDiagnostics
	lastFailedAt, lastSucceededAt := circuit.LastFailedAt, &databaseNow
	if !success {
		to, cause, failures = CircuitOpen, "probe_failed", circuit.ConsecutiveFailures+1
		if circuit.WindowStartedAt != nil {
			value := *circuit.WindowStartedAt
			windowStartedAt = &value
		} else {
			windowStartedAt = &databaseNow
		}
		if circuit.OpenedAt != nil {
			value := *circuit.OpenedAt
			openedAt = &value
		} else {
			openedAt = &databaseNow
		}
		value := databaseNow.Add(policy.OpenDuration)
		nextProbeAt = &value
		lastClass, lastSource, lastDiagnostics = disposition.Class, sourceReason, diagnostics
		lastFailedAt, lastSucceededAt = &databaseNow, circuit.LastSucceededAt
	}
	generation := circuit.Generation + 1
	err = tx.QueryRow(ctx, `
		UPDATE research_execution_circuit
		SET state = $2, generation = $3, consecutive_failures = $4,
		    window_started_at = $5, opened_at = $6, next_probe_at = $7,
		    probe_token = NULL, probe_lease_expires_at = NULL,
		    last_failure_class = $8, last_source_reason = $9, last_diagnostics = $10,
		    last_failed_at = $11, last_succeeded_at = $12, updated_at = $13
		WHERE id = $1::uuid
		RETURNING `+circuitColumnsSQL,
		circuit.ID, to, generation, failures, windowStartedAt, openedAt, nextProbeAt,
		truncateBytes(string(lastClass), 160), truncateBytes(lastSource, 160), truncateBytes(lastDiagnostics, 4096),
		lastFailedAt, lastSucceededAt, databaseNow,
	).Scan(circuitScanDestinations(&circuit)...)
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	transition, err := recordCircuitTransitionTx(ctx, tx, circuit, lease.SessionID, "", from, to,
		cause, lastClass, lastSource, lastDiagnostics)
	if err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	if err = s.commitResearchTx(ctx, txOpCircuitProbeResolve, tx); err != nil {
		return ExecutionCircuit{}, CircuitTransition{}, err
	}
	return circuit, transition, nil
}

func (s *PostgresStore) GetExecutionCircuit(ctx context.Context, workspaceID string, target CircuitTarget) (ExecutionCircuit, error) {
	row := s.pool.QueryRow(ctx, circuitSelectSQL+` WHERE workspace_id = $1::uuid AND scope = $2 AND target_key = $3`, workspaceID, target.Scope, target.Key)
	return scanCircuit(row)
}

func loadCircuitForUpdate(ctx context.Context, tx pgx.Tx, workspaceID string, target CircuitTarget) (ExecutionCircuit, error) {
	row := tx.QueryRow(ctx, circuitSelectSQL+` WHERE workspace_id = $1::uuid AND scope = $2 AND target_key = $3 FOR UPDATE`, workspaceID, target.Scope, target.Key)
	return scanCircuit(row)
}

func scanCircuit(row scanner) (ExecutionCircuit, error) {
	var circuit ExecutionCircuit
	var window, opened, nextProbe, probeExpiry, failed, succeeded pgtype.Timestamptz
	err := row.Scan(
		&circuit.ID, &circuit.WorkspaceID, &circuit.Target.Scope, &circuit.Target.Key,
		&circuit.Target.Label, &circuit.Target.ConfigFingerprint, &circuit.State,
		&circuit.Generation, &circuit.ConsecutiveFailures, &window, &opened, &nextProbe,
		&circuit.ProbeToken, &probeExpiry, &circuit.LastFailureClass,
		&circuit.LastSourceReason, &circuit.LastDiagnostics, &circuit.LastAttemptID,
		&circuit.LastSessionID, &failed, &succeeded,
	)
	if err != nil {
		return ExecutionCircuit{}, err
	}
	assignTimestamp := func(value pgtype.Timestamptz, destination **time.Time) {
		if value.Valid {
			copy := value.Time
			*destination = &copy
		}
	}
	assignTimestamp(window, &circuit.WindowStartedAt)
	assignTimestamp(opened, &circuit.OpenedAt)
	assignTimestamp(nextProbe, &circuit.NextProbeAt)
	assignTimestamp(probeExpiry, &circuit.ProbeLeaseExpiresAt)
	assignTimestamp(failed, &circuit.LastFailedAt)
	assignTimestamp(succeeded, &circuit.LastSucceededAt)
	return circuit, nil
}

func circuitScanDestinations(circuit *ExecutionCircuit) []any {
	// UPDATE ... RETURNING uses the same column order as circuitSelectSQL. Scan
	// through a small adapter so nullable timestamps are decoded consistently.
	return []any{
		&circuit.ID, &circuit.WorkspaceID, &circuit.Target.Scope, &circuit.Target.Key,
		&circuit.Target.Label, &circuit.Target.ConfigFingerprint, &circuit.State,
		&circuit.Generation, &circuit.ConsecutiveFailures, timestampDestination(&circuit.WindowStartedAt),
		timestampDestination(&circuit.OpenedAt), timestampDestination(&circuit.NextProbeAt),
		&circuit.ProbeToken, timestampDestination(&circuit.ProbeLeaseExpiresAt),
		&circuit.LastFailureClass, &circuit.LastSourceReason, &circuit.LastDiagnostics,
		&circuit.LastAttemptID, &circuit.LastSessionID, timestampDestination(&circuit.LastFailedAt),
		timestampDestination(&circuit.LastSucceededAt),
	}
}

type nullableTimeScanner struct{ destination **time.Time }

func timestampDestination(destination **time.Time) *nullableTimeScanner {
	return &nullableTimeScanner{destination: destination}
}

func (scanner *nullableTimeScanner) Scan(value any) error {
	if value == nil {
		*scanner.destination = nil
		return nil
	}
	parsed, ok := value.(time.Time)
	if !ok {
		return fmt.Errorf("scan circuit timestamp from %T", value)
	}
	*scanner.destination = &parsed
	return nil
}

func assertAttemptCircuitTarget(ctx context.Context, tx pgx.Tx, in CircuitFailureInput, target CircuitTarget) error {
	return assertFrozenAttemptCircuitTarget(ctx, tx, in.WorkspaceID, in.SessionID, in.AttemptID, target)
}

func assertFrozenAttemptCircuitTarget(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, attemptID string, target CircuitTarget) error {
	var frozen ExecutionTarget
	err := tx.QueryRow(ctx, `
		SELECT assigned_agent_id::text, execution_adapter, COALESCE(runtime_id::text, ''),
		       provider, model, target_config_fingerprint, agent_config_fingerprint,
		       runtime_config_fingerprint, provider_config_fingerprint
		FROM research_task_attempt
		WHERE id = $1::uuid AND session_id = $2::uuid AND workspace_id = $3::uuid
		FOR SHARE
	`, attemptID, sessionID, workspaceID).Scan(
		&frozen.AgentID, &frozen.Adapter, &frozen.RuntimeID, &frozen.Provider, &frozen.Model,
		&frozen.ConfigFingerprint, &frozen.AgentConfigFingerprint,
		&frozen.RuntimeConfigFingerprint, &frozen.ProviderConfigFingerprint,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRunNotFound
	}
	if err != nil {
		return err
	}
	frozenTarget, err := CircuitTargetForExecution(frozen, target.Scope)
	if err != nil || frozenTarget.Key != target.Key || frozenTarget.ConfigFingerprint != target.ConfigFingerprint {
		return fmt.Errorf("%w: circuit target does not match frozen attempt", ErrInvalidTransition)
	}
	return nil
}

func resetCircuitForConfiguration(ctx context.Context, tx pgx.Tx, circuit ExecutionCircuit, sessionID, attemptID, fingerprint string) (CircuitTransition, ExecutionCircuit, error) {
	from := circuit.State
	generation := circuit.Generation + 1
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return CircuitTransition{}, ExecutionCircuit{}, err
	}
	err := tx.QueryRow(ctx, `
		UPDATE research_execution_circuit
		SET config_fingerprint = $2, state = 'closed', generation = $3,
		    consecutive_failures = 0, window_started_at = NULL, opened_at = NULL,
		    next_probe_at = NULL, probe_token = NULL, probe_lease_expires_at = NULL,
		    last_failure_class = '', last_source_reason = '', last_diagnostics = '',
		    last_attempt_id = NULLIF($4, '')::uuid, last_session_id = $5::uuid,
		    last_succeeded_at = $6, updated_at = $6
		WHERE id = $1::uuid
		RETURNING `+circuitColumnsSQL,
		circuit.ID, fingerprint, generation, attemptID, sessionID, databaseNow,
	).Scan(circuitScanDestinations(&circuit)...)
	if err != nil {
		return CircuitTransition{}, ExecutionCircuit{}, err
	}
	transition, err := recordCircuitTransitionTx(ctx, tx, circuit, sessionID, attemptID, from, CircuitClosed,
		"configuration_changed", "", "", "")
	return transition, circuit, err
}

func recordCircuitTransitionTx(ctx context.Context, tx pgx.Tx, circuit ExecutionCircuit, sessionID, attemptID string,
	from, to CircuitState, cause string, class FailureClass, sourceReason, diagnostics string,
) (CircuitTransition, error) {
	var transition CircuitTransition
	err := tx.QueryRow(ctx, `
		INSERT INTO research_execution_circuit_transition (
		  workspace_id, circuit_id, session_id, attempt_id, generation,
		  from_state, to_state, cause, failure_class, source_reason,
		  diagnostics, config_fingerprint
		) VALUES (
		  $1::uuid, $2::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5,
		  $6, $7, $8, $9, $10, $11, $12
		)
		RETURNING id::text, created_at
	`, circuit.WorkspaceID, circuit.ID, sessionID, attemptID, circuit.Generation,
		from, to, cause, truncateBytes(string(class), 160), truncateBytes(sourceReason, 160),
		truncateBytes(diagnostics, 4096), circuit.Target.ConfigFingerprint,
	).Scan(&transition.ID, &transition.CreatedAt)
	if err != nil {
		return CircuitTransition{}, err
	}
	transition.WorkspaceID, transition.CircuitID = circuit.WorkspaceID, circuit.ID
	transition.SessionID, transition.AttemptID, transition.Generation = sessionID, attemptID, circuit.Generation
	transition.FromState, transition.ToState, transition.Cause = from, to, cause
	transition.FailureClass, transition.SourceReason = class, sourceReason
	transition.Diagnostics, transition.ConfigFingerprint = truncateBytes(diagnostics, 4096), circuit.Target.ConfigFingerprint
	if sessionID != "" {
		_, err = appendEvent(ctx, tx, circuit.WorkspaceID, sessionID, "execution_circuit_transition",
			fmt.Sprintf("circuit:%s:generation:%d", circuit.ID, circuit.Generation), "system", "", map[string]any{
				"circuit_id": circuit.ID, "scope": circuit.Target.Scope, "target_key": circuit.Target.Key,
				"target_label": circuit.Target.Label, "generation": circuit.Generation,
				"config_fingerprint": circuit.Target.ConfigFingerprint,
				"from_state":         from, "to_state": to, "cause": cause,
				"failure_class": class, "source_failure_reason": sourceReason,
				"diagnostics": truncateBytes(diagnostics, 4096), "attempt_id": attemptID,
				"consecutive_failures": circuit.ConsecutiveFailures,
				"window_started_at":    circuit.WindowStartedAt, "opened_at": circuit.OpenedAt,
				"next_probe_at":          circuit.NextProbeAt,
				"probe_lease_expires_at": circuit.ProbeLeaseExpiresAt,
			})
	}
	return transition, err
}

func findAttemptCircuitTransition(ctx context.Context, tx pgx.Tx, circuitID, attemptID, cause string) (CircuitTransition, bool, error) {
	var transition CircuitTransition
	err := tx.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, circuit_id::text,
		       COALESCE(session_id::text, ''), COALESCE(attempt_id::text, ''),
		       generation, from_state, to_state, cause, failure_class,
		       source_reason, diagnostics, config_fingerprint, created_at
		FROM research_execution_circuit_transition
		WHERE circuit_id = $1::uuid AND attempt_id = $2::uuid AND cause = $3
	`, circuitID, attemptID, cause).Scan(
		&transition.ID, &transition.WorkspaceID, &transition.CircuitID,
		&transition.SessionID, &transition.AttemptID, &transition.Generation,
		&transition.FromState, &transition.ToState, &transition.Cause,
		&transition.FailureClass, &transition.SourceReason, &transition.Diagnostics,
		&transition.ConfigFingerprint, &transition.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CircuitTransition{}, false, nil
	}
	return transition, err == nil, err
}

package researchrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func normalizeAttemptProbeTargets(executionTarget ExecutionTarget, requested []CircuitTarget) ([]CircuitTarget, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	result := make([]CircuitTarget, 0, len(requested))
	seen := make(map[CircuitScope]struct{}, len(requested))
	for _, candidate := range requested {
		if _, exists := seen[candidate.Scope]; exists {
			return nil, fmt.Errorf("%w: duplicate %s probe target", ErrInvalidTransition, candidate.Scope)
		}
		frozen, err := CircuitTargetForExecution(executionTarget, candidate.Scope)
		if err != nil {
			return nil, err
		}
		if frozen.Key != candidate.Key || frozen.ConfigFingerprint != candidate.ConfigFingerprint {
			return nil, fmt.Errorf("%w: probe target does not match frozen execution target", ErrInvalidTransition)
		}
		seen[candidate.Scope] = struct{}{}
		result = append(result, frozen)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Scope == result[j].Scope {
			return result[i].Key < result[j].Key
		}
		return result[i].Scope < result[j].Scope
	})
	return result, nil
}

func claimCircuitProbeForAttemptTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
	target CircuitTarget,
	leaseDuration time.Duration,
) (AttemptCircuitProbe, error) {
	if leaseDuration <= 0 {
		return AttemptCircuitProbe{}, fmt.Errorf("%w: probe lease duration must be positive", ErrInvalidTransition)
	}
	circuit, err := loadCircuitForUpdate(ctx, tx, workspaceID, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptCircuitProbe{}, ErrCircuitUnavailable
	}
	if err != nil {
		return AttemptCircuitProbe{}, err
	}
	if circuit.Target.ConfigFingerprint != target.ConfigFingerprint {
		return AttemptCircuitProbe{}, ErrCircuitUnavailable
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return AttemptCircuitProbe{}, err
	}
	claimable := circuit.State == CircuitOpen && circuit.NextProbeAt != nil && !circuit.NextProbeAt.After(databaseNow)
	if circuit.State == CircuitHalfOpen && circuit.ProbeLeaseExpiresAt != nil && !circuit.ProbeLeaseExpiresAt.After(databaseNow) {
		claimable = true
	}
	if !claimable {
		return AttemptCircuitProbe{}, ErrCircuitUnavailable
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_attempt_circuit_probe
		SET status = 'lost', resolved_at = $2, updated_at = $2,
		    diagnostics = CASE WHEN diagnostics = '' THEN 'probe lease superseded after expiry' ELSE diagnostics END
		WHERE circuit_id = $1::uuid AND status = 'active'
	`, circuit.ID, databaseNow); err != nil {
		return AttemptCircuitProbe{}, err
	}
	from := circuit.State
	token := uuid.NewString()
	generation := circuit.Generation + 1
	expiresAt := databaseNow.Add(leaseDuration)
	if _, err = tx.Exec(ctx, `
		UPDATE research_execution_circuit
		SET state = 'half_open', generation = $2, probe_token = $3::uuid,
		    probe_lease_expires_at = $4, updated_at = $5
		WHERE id = $1::uuid
	`, circuit.ID, generation, token, expiresAt, databaseNow); err != nil {
		return AttemptCircuitProbe{}, err
	}
	circuit.State = CircuitHalfOpen
	circuit.Generation = generation
	circuit.ProbeToken = token
	circuit.ProbeLeaseExpiresAt = &expiresAt
	if _, err = recordCircuitTransitionTx(ctx, tx, circuit, sessionID, attemptID, from, CircuitHalfOpen,
		"probe_claimed", "", "", ""); err != nil {
		return AttemptCircuitProbe{}, err
	}
	var binding AttemptCircuitProbe
	err = tx.QueryRow(ctx, `
		INSERT INTO research_attempt_circuit_probe (
		  workspace_id, session_id, attempt_id, circuit_id, scope,
		  probe_token, generation, config_fingerprint, lease_expires_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5,
		  $6::uuid, $7, $8, $9
		)
		RETURNING id::text
	`, workspaceID, sessionID, attemptID, circuit.ID, target.Scope,
		token, generation, target.ConfigFingerprint, expiresAt).Scan(&binding.ID)
	if err != nil {
		return AttemptCircuitProbe{}, err
	}
	binding.WorkspaceID = workspaceID
	binding.SessionID = sessionID
	binding.AttemptID = attemptID
	binding.CircuitID = circuit.ID
	binding.Target = target
	binding.Token = token
	binding.Generation = generation
	binding.LeaseExpiresAt = expiresAt
	binding.Status = "active"
	return binding, nil
}

type attemptProbeRef struct {
	CircuitID string
}

type lockedAttemptProbe struct {
	Binding AttemptCircuitProbe
	Circuit ExecutionCircuit
}

func listAttemptProbeRefsTx(ctx context.Context, tx pgx.Tx, attemptID string) ([]attemptProbeRef, error) {
	rows, err := tx.Query(ctx, `
		SELECT binding.circuit_id::text
		FROM research_attempt_circuit_probe binding
		JOIN research_execution_circuit circuit ON circuit.id = binding.circuit_id
		WHERE binding.attempt_id = $1::uuid AND binding.status = 'active'
		ORDER BY circuit.scope, circuit.target_key, circuit.id
	`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []attemptProbeRef{}
	for rows.Next() {
		var ref attemptProbeRef
		if err = rows.Scan(&ref.CircuitID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func lockAttemptProbeTx(ctx context.Context, tx pgx.Tx, attemptID, circuitID string) (lockedAttemptProbe, bool, error) {
	circuit, err := scanCircuit(tx.QueryRow(ctx, circuitSelectSQL+` WHERE id = $1::uuid FOR UPDATE`, circuitID))
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedAttemptProbe{}, false, nil
	}
	if err != nil {
		return lockedAttemptProbe{}, false, err
	}
	var probe lockedAttemptProbe
	probe.Circuit = circuit
	var resolvedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, attempt_id::text,
		       circuit_id::text, scope, probe_token::text, generation,
		       config_fingerprint, lease_expires_at, status, failure_class,
		       source_reason, diagnostics, resolved_at
		FROM research_attempt_circuit_probe
		WHERE attempt_id = $1::uuid AND circuit_id = $2::uuid
		FOR UPDATE
	`, attemptID, circuitID).Scan(
		&probe.Binding.ID, &probe.Binding.WorkspaceID, &probe.Binding.SessionID,
		&probe.Binding.AttemptID, &probe.Binding.CircuitID, &probe.Binding.Target.Scope,
		&probe.Binding.Token, &probe.Binding.Generation, &probe.Binding.Target.ConfigFingerprint,
		&probe.Binding.LeaseExpiresAt, &probe.Binding.Status, &probe.Binding.FailureClass,
		&probe.Binding.SourceReason, &probe.Binding.Diagnostics, &resolvedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedAttemptProbe{}, false, nil
	}
	if err != nil {
		return lockedAttemptProbe{}, false, err
	}
	probe.Binding.ResolvedAt = resolvedAt
	probe.Binding.Target.Key = circuit.Target.Key
	probe.Binding.Target.Label = circuit.Target.Label
	return probe, true, nil
}

func probeOwnsCircuit(probe lockedAttemptProbe, databaseNow time.Time) bool {
	return probe.Binding.Status == "active" &&
		probe.Binding.Generation == probe.Circuit.Generation &&
		probe.Binding.Token == probe.Circuit.ProbeToken &&
		probe.Binding.Target.Scope == probe.Circuit.Target.Scope &&
		probe.Binding.Target.ConfigFingerprint == probe.Circuit.Target.ConfigFingerprint &&
		probe.Circuit.State == CircuitHalfOpen &&
		probe.Circuit.ProbeLeaseExpiresAt != nil && probe.Circuit.ProbeLeaseExpiresAt.After(databaseNow)
}

func resolveAttemptProbeBindingTx(ctx context.Context, tx pgx.Tx, bindingID, status string, class FailureClass, sourceReason, diagnostics string, databaseNow time.Time) error {
	command, err := tx.Exec(ctx, `
		UPDATE research_attempt_circuit_probe
		SET status = $2, failure_class = $3, source_reason = $4,
		    diagnostics = $5, resolved_at = $6, updated_at = $6
		WHERE id = $1::uuid AND status = 'active'
	`, bindingID, status, truncateBytes(string(class), 160), truncateBytes(sourceReason, 160),
		truncateBytes(diagnostics, 4096), databaseNow)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrCircuitProbeLeaseLost
	}
	return nil
}

func markAttemptProbeLostTx(ctx context.Context, tx pgx.Tx, bindingID string, databaseNow time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE research_attempt_circuit_probe
		SET status = 'lost', resolved_at = $2, updated_at = $2,
		    diagnostics = CASE WHEN diagnostics = '' THEN 'probe ownership changed before settlement' ELSE diagnostics END
		WHERE id = $1::uuid AND status = 'active'
	`, bindingID, databaseNow)
	return err
}

func loadFrozenAttemptTargetTx(ctx context.Context, tx pgx.Tx, attemptID string) (ExecutionTarget, string, string, error) {
	var target ExecutionTarget
	var workspaceID, sessionID string
	err := tx.QueryRow(ctx, `
		SELECT workspace_id::text, session_id::text, assigned_agent_id::text,
		       execution_adapter, COALESCE(runtime_id::text, ''), provider, model,
		       target_config_fingerprint, agent_config_fingerprint,
		       runtime_config_fingerprint, provider_config_fingerprint
		FROM research_task_attempt WHERE id = $1::uuid
	`, attemptID).Scan(
		&workspaceID, &sessionID, &target.AgentID, &target.Adapter, &target.RuntimeID,
		&target.Provider, &target.Model, &target.ConfigFingerprint,
		&target.AgentConfigFingerprint, &target.RuntimeConfigFingerprint,
		&target.ProviderConfigFingerprint,
	)
	return target, workspaceID, sessionID, err
}

func lockExecutionTargetCircuitsTx(ctx context.Context, tx pgx.Tx, workspaceID string, executionTarget ExecutionTarget) error {
	targets := make([]CircuitTarget, 0, 4)
	for _, scope := range []CircuitScope{CircuitAgent, CircuitRuntime, CircuitProvider, CircuitAdapter} {
		target, err := CircuitTargetForExecution(executionTarget, scope)
		if err != nil {
			continue
		}
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Scope == targets[j].Scope {
			return targets[i].Key < targets[j].Key
		}
		return targets[i].Scope < targets[j].Scope
	})
	for _, target := range targets {
		_, err := loadCircuitForUpdate(ctx, tx, workspaceID, target)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func settleAttemptCircuitSuccessTx(ctx context.Context, tx pgx.Tx, attemptID string) error {
	target, workspaceID, sessionID, err := loadFrozenAttemptTargetTx(ctx, tx, attemptID)
	if err != nil {
		return err
	}
	if err = lockExecutionTargetCircuitsTx(ctx, tx, workspaceID, target); err != nil {
		return err
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return err
	}
	refs, err := listAttemptProbeRefsTx(ctx, tx, attemptID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		probe, found, lockErr := lockAttemptProbeTx(ctx, tx, attemptID, ref.CircuitID)
		if lockErr != nil {
			return lockErr
		}
		if !found || probe.Binding.Status != "active" {
			continue
		}
		if !probeOwnsCircuit(probe, databaseNow) {
			if err = markAttemptProbeLostTx(ctx, tx, probe.Binding.ID, databaseNow); err != nil {
				return err
			}
			continue
		}
		from := probe.Circuit.State
		probe.Circuit.Generation++
		err = tx.QueryRow(ctx, `
			UPDATE research_execution_circuit
			SET state = 'closed', generation = $2, consecutive_failures = 0,
			    window_started_at = NULL, opened_at = NULL, next_probe_at = NULL,
			    probe_token = NULL, probe_lease_expires_at = NULL,
			    last_attempt_id = $3::uuid, last_session_id = $4::uuid,
			    last_succeeded_at = $5, updated_at = $5
			WHERE id = $1::uuid
			RETURNING `+circuitColumnsSQL,
			probe.Circuit.ID, probe.Circuit.Generation, attemptID, sessionID, databaseNow,
		).Scan(circuitScanDestinations(&probe.Circuit)...)
		if err != nil {
			return err
		}
		if _, err = recordCircuitTransitionTx(ctx, tx, probe.Circuit, sessionID, attemptID,
			from, CircuitClosed, "probe_succeeded", "", "", ""); err != nil {
			return err
		}
		if err = resolveAttemptProbeBindingTx(ctx, tx, probe.Binding.ID, "succeeded", "", "", "", databaseNow); err != nil {
			return err
		}
	}
	for _, scope := range []CircuitScope{CircuitAdapter, CircuitAgent, CircuitProvider, CircuitRuntime} {
		if err = recordClosedCircuitSuccessTx(ctx, tx, workspaceID, sessionID, attemptID, target, scope, databaseNow); err != nil {
			return err
		}
	}
	return nil
}

func recordClosedCircuitSuccessTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, attemptID string, executionTarget ExecutionTarget, scope CircuitScope, databaseNow time.Time) error {
	target, err := CircuitTargetForExecution(executionTarget, scope)
	if err != nil {
		return nil
	}
	circuit, err := loadCircuitForUpdate(ctx, tx, workspaceID, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing, found, findErr := findAttemptCircuitTransition(ctx, tx, circuit.ID, attemptID, "success_observed"); findErr != nil {
		return findErr
	} else if found && existing.ID != "" {
		return nil
	}
	if target.ConfigFingerprint != "" && circuit.Target.ConfigFingerprint != target.ConfigFingerprint {
		_, _, err = resetCircuitForConfiguration(ctx, tx, circuit, sessionID, attemptID, target.ConfigFingerprint)
		return err
	}
	if circuit.State != CircuitClosed || (circuit.ConsecutiveFailures == 0 && circuit.WindowStartedAt == nil) {
		return nil
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
		circuit.ID, circuit.Generation, attemptID, sessionID, databaseNow,
	).Scan(circuitScanDestinations(&circuit)...)
	if err != nil {
		return err
	}
	_, err = recordCircuitTransitionTx(ctx, tx, circuit, sessionID, attemptID,
		from, CircuitClosed, "success_observed", "", "", "")
	return err
}

func settleAttemptCircuitFailureTx(ctx context.Context, tx pgx.Tx, in AttemptFailure) error {
	target, workspaceID, sessionID, err := loadFrozenAttemptTargetTx(ctx, tx, in.AttemptID)
	if err != nil {
		return err
	}
	if err = lockExecutionTargetCircuitsTx(ctx, tx, workspaceID, target); err != nil {
		return err
	}
	disposition := failureDisposition(FailureClass(in.FailureClass))
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return err
	}
	hadMatchingProbe := false
	if disposition.CircuitScope != CircuitNone {
		if err = tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM research_attempt_circuit_probe
			  WHERE attempt_id = $1::uuid AND scope = $2
			)
		`, in.AttemptID, disposition.CircuitScope).Scan(&hadMatchingProbe); err != nil {
			return err
		}
	}
	refs, err := listAttemptProbeRefsTx(ctx, tx, in.AttemptID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		probe, found, lockErr := lockAttemptProbeTx(ctx, tx, in.AttemptID, ref.CircuitID)
		if lockErr != nil {
			return lockErr
		}
		if !found || probe.Binding.Status != "active" {
			continue
		}
		matching := disposition.CircuitScope != CircuitNone && probe.Binding.Target.Scope == disposition.CircuitScope
		if !probeOwnsCircuit(probe, databaseNow) {
			if err = markAttemptProbeLostTx(ctx, tx, probe.Binding.ID, databaseNow); err != nil {
				return err
			}
			continue
		}
		if matching {
			if err = failOwnedAttemptProbeTx(ctx, tx, probe, sessionID, in, disposition, databaseNow); err != nil {
				return err
			}
			continue
		}
		if err = releaseOwnedAttemptProbeTx(ctx, tx, probe, sessionID, in.AttemptID, "probe_inconclusive", "inconclusive", databaseNow); err != nil {
			return err
		}
	}
	if disposition.CircuitScope != CircuitNone && !hadMatchingProbe {
		return recordCircuitFailureTx(ctx, tx, workspaceID, sessionID, in, target, disposition, databaseNow)
	}
	return nil
}

func failOwnedAttemptProbeTx(ctx context.Context, tx pgx.Tx, probe lockedAttemptProbe, sessionID string, in AttemptFailure, disposition FailureDisposition, databaseNow time.Time) error {
	policy, ok := policyForCircuitFailure(disposition)
	if !ok {
		return fmt.Errorf("%w: failed probe lacks circuit policy", ErrInvalidTransition)
	}
	from := probe.Circuit.State
	windowStarted := databaseNow
	if probe.Circuit.WindowStartedAt != nil {
		windowStarted = *probe.Circuit.WindowStartedAt
	}
	openedAt := databaseNow
	if probe.Circuit.OpenedAt != nil {
		openedAt = *probe.Circuit.OpenedAt
	}
	nextProbeAt := databaseNow.Add(policy.OpenDuration)
	probe.Circuit.Generation++
	err := tx.QueryRow(ctx, `
		UPDATE research_execution_circuit
		SET state = 'open', generation = $2,
		    consecutive_failures = consecutive_failures + 1,
		    window_started_at = $3, opened_at = $4, next_probe_at = $5,
		    probe_token = NULL, probe_lease_expires_at = NULL,
		    last_failure_class = $6, last_source_reason = $7,
		    last_diagnostics = $8, last_attempt_id = $9::uuid,
		    last_session_id = $10::uuid, last_failed_at = $11, updated_at = $11
		WHERE id = $1::uuid
		RETURNING `+circuitColumnsSQL,
		probe.Circuit.ID, probe.Circuit.Generation, windowStarted, openedAt, nextProbeAt,
		disposition.Class, truncateBytes(in.SourceReason, 160), truncateBytes(in.Diagnostics, 4096),
		in.AttemptID, sessionID, databaseNow,
	).Scan(circuitScanDestinations(&probe.Circuit)...)
	if err != nil {
		return err
	}
	if _, err = recordCircuitTransitionTx(ctx, tx, probe.Circuit, sessionID, in.AttemptID,
		from, CircuitOpen, "probe_failed", disposition.Class, in.SourceReason, in.Diagnostics); err != nil {
		return err
	}
	return resolveAttemptProbeBindingTx(ctx, tx, probe.Binding.ID, "failed", disposition.Class,
		in.SourceReason, in.Diagnostics, databaseNow)
}

func releaseOwnedAttemptProbeTx(ctx context.Context, tx pgx.Tx, probe lockedAttemptProbe, sessionID, attemptID, cause, bindingStatus string, databaseNow time.Time) error {
	from := probe.Circuit.State
	probe.Circuit.Generation++
	err := tx.QueryRow(ctx, `
		UPDATE research_execution_circuit
		SET state = 'open', generation = $2, next_probe_at = $3,
		    probe_token = NULL, probe_lease_expires_at = NULL,
		    last_attempt_id = $4::uuid, last_session_id = $5::uuid, updated_at = $3
		WHERE id = $1::uuid
		RETURNING `+circuitColumnsSQL,
		probe.Circuit.ID, probe.Circuit.Generation, databaseNow, attemptID, sessionID,
	).Scan(circuitScanDestinations(&probe.Circuit)...)
	if err != nil {
		return err
	}
	diagnostics := "probe attempt ended without evidence for this circuit scope"
	if _, err = recordCircuitTransitionTx(ctx, tx, probe.Circuit, sessionID, attemptID,
		from, CircuitOpen, cause, "", "", diagnostics); err != nil {
		return err
	}
	return resolveAttemptProbeBindingTx(ctx, tx, probe.Binding.ID, bindingStatus, "", "", diagnostics, databaseNow)
}

func recordCircuitFailureTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, in AttemptFailure, executionTarget ExecutionTarget, disposition FailureDisposition, databaseNow time.Time) error {
	policy, ok := policyForCircuitFailure(disposition)
	if !ok {
		return nil
	}
	target, err := CircuitTargetForExecution(executionTarget, disposition.CircuitScope)
	if err != nil {
		return nil
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_execution_circuit (
		  workspace_id, scope, target_key, target_label, config_fingerprint
		) VALUES ($1::uuid, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, scope, target_key) DO NOTHING
	`, workspaceID, target.Scope, target.Key, truncateBytes(target.Label, 240), target.ConfigFingerprint); err != nil {
		return err
	}
	circuit, err := loadCircuitForUpdate(ctx, tx, workspaceID, target)
	if err != nil {
		return err
	}
	if _, found, findErr := findAttemptCircuitTransition(ctx, tx, circuit.ID, in.AttemptID, "failure_observed"); findErr != nil {
		return findErr
	} else if found {
		return nil
	}
	if target.ConfigFingerprint != "" && circuit.Target.ConfigFingerprint != target.ConfigFingerprint {
		_, reset, resetErr := resetCircuitForConfiguration(ctx, tx, circuit, sessionID, in.AttemptID, target.ConfigFingerprint)
		if resetErr != nil {
			return resetErr
		}
		circuit = reset
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
	if disposition.ImmediateOpen || failures >= policy.Threshold || from != CircuitClosed {
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
		openedAt, nextProbeAt = nil, nil
	}
	circuit.Generation++
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
		circuit.Generation, failures, windowStarted, openedAt, nextProbeAt,
		disposition.Class, truncateBytes(in.SourceReason, 160), truncateBytes(in.Diagnostics, 4096),
		in.AttemptID, sessionID, databaseNow,
	).Scan(circuitScanDestinations(&circuit)...)
	if err != nil {
		return err
	}
	_, err = recordCircuitTransitionTx(ctx, tx, circuit, sessionID, in.AttemptID, from, to,
		"failure_observed", disposition.Class, in.SourceReason, in.Diagnostics)
	return err
}

func abandonAttemptCircuitProbesTx(ctx context.Context, tx pgx.Tx, attemptID string) error {
	_, _, sessionID, err := loadFrozenAttemptTargetTx(ctx, tx, attemptID)
	if err != nil {
		return err
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return err
	}
	refs, err := listAttemptProbeRefsTx(ctx, tx, attemptID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		probe, found, lockErr := lockAttemptProbeTx(ctx, tx, attemptID, ref.CircuitID)
		if lockErr != nil {
			return lockErr
		}
		if !found || probe.Binding.Status != "active" {
			continue
		}
		if !probeOwnsCircuit(probe, databaseNow) {
			if err = markAttemptProbeLostTx(ctx, tx, probe.Binding.ID, databaseNow); err != nil {
				return err
			}
			continue
		}
		if err = releaseOwnedAttemptProbeTx(ctx, tx, probe, sessionID, attemptID, "probe_abandoned", "abandoned", databaseNow); err != nil {
			return err
		}
	}
	return nil
}

func abandonSessionCircuitProbesTx(ctx context.Context, tx pgx.Tx, sessionID string) error {
	rows, err := tx.Query(ctx, `
		SELECT binding.attempt_id::text, binding.circuit_id::text
		FROM research_attempt_circuit_probe binding
		JOIN research_task_attempt attempt ON attempt.id = binding.attempt_id
		JOIN research_execution_circuit circuit ON circuit.id = binding.circuit_id
		WHERE attempt.session_id = $1::uuid
		  AND attempt.status IN ('dispatching', 'running', 'cancelling')
		  AND binding.status = 'active'
		ORDER BY circuit.scope, circuit.target_key, circuit.id, binding.id
	`, sessionID)
	if err != nil {
		return err
	}
	type probeRef struct{ attemptID, circuitID string }
	refs := []probeRef{}
	for rows.Next() {
		var ref probeRef
		if err = rows.Scan(&ref.attemptID, &ref.circuitID); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return err
	}
	for _, ref := range refs {
		probe, found, lockErr := lockAttemptProbeTx(ctx, tx, ref.attemptID, ref.circuitID)
		if lockErr != nil {
			return lockErr
		}
		if !found || probe.Binding.Status != "active" {
			continue
		}
		if !probeOwnsCircuit(probe, databaseNow) {
			if err = markAttemptProbeLostTx(ctx, tx, probe.Binding.ID, databaseNow); err != nil {
				return err
			}
			continue
		}
		if err = releaseOwnedAttemptProbeTx(ctx, tx, probe, sessionID, ref.attemptID, "probe_abandoned", "abandoned", databaseNow); err != nil {
			return err
		}
	}
	return nil
}

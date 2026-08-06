package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) EvaluateExecutionTargets(ctx context.Context, workspaceID string, members []FleetMember) (map[string]ExecutionTargetHealth, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("%w: workspace is required for circuit evaluation", ErrInvalidTransition)
	}
	targetsByAgent := make(map[string][]CircuitTarget, len(members))
	targetKeys := make([]string, 0, len(members)*4)
	seenKeys := map[string]struct{}{}
	for _, member := range members {
		targets := make([]CircuitTarget, 0, 4)
		for _, scope := range []CircuitScope{CircuitAgent, CircuitRuntime, CircuitProvider, CircuitAdapter} {
			target, err := CircuitTargetForExecution(member.ExecutionTarget, scope)
			if err != nil {
				continue
			}
			targets = append(targets, target)
			if _, exists := seenKeys[target.Key]; !exists {
				seenKeys[target.Key] = struct{}{}
				targetKeys = append(targetKeys, target.Key)
			}
		}
		targetsByAgent[member.AgentID] = targets
	}
	var databaseNow time.Time
	if err := s.pool.QueryRow(ctx, `SELECT now()`).Scan(&databaseNow); err != nil {
		return nil, err
	}
	circuits := map[string]ExecutionCircuit{}
	if len(targetKeys) > 0 {
		rows, err := s.pool.Query(ctx, circuitSelectSQL+`
			WHERE workspace_id = $1::uuid AND target_key = ANY($2::text[])
		`, workspaceID, targetKeys)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			circuit, scanErr := scanCircuit(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			circuits[circuitLookupKey(circuit.Target)] = circuit
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	health := make(map[string]ExecutionTargetHealth, len(members))
	for _, member := range members {
		item := ExecutionTargetHealth{AgentID: member.AgentID, Dispatchable: true}
		if strings.TrimSpace(member.ProviderBlockDetail) != "" &&
			(member.ProviderBlockedUntil == nil || member.ProviderBlockedUntil.After(databaseNow)) {
			item.Dispatchable = false
			item.BlockedReason = "provider_blocked"
			if member.ProviderBlockedUntil != nil {
				value := *member.ProviderBlockedUntil
				item.RetryAt = &value
			}
		}
		for _, target := range targetsByAgent[member.AgentID] {
			circuit, exists := circuits[circuitLookupKey(target)]
			if !exists || circuit.State == CircuitClosed ||
				(target.ConfigFingerprint != "" && circuit.Target.ConfigFingerprint != target.ConfigFingerprint) {
				continue
			}
			var retryAt *time.Time
			switch circuit.State {
			case CircuitOpen:
				retryAt = circuit.NextProbeAt
			case CircuitHalfOpen:
				retryAt = circuit.ProbeLeaseExpiresAt
			}
			if retryAt != nil && !retryAt.After(databaseNow) {
				item.ProbeTargets = append(item.ProbeTargets, target)
				continue
			}
			item.Dispatchable = false
			item.Blocking = append(item.Blocking, CircuitBlock{
				CircuitID: circuit.ID, Scope: target.Scope, State: circuit.State,
				Generation: circuit.Generation, RetryAt: retryAt,
			})
			item.RetryAt = earlierTime(item.RetryAt, retryAt)
		}
		if !item.Dispatchable {
			item.ProbeTargets = nil
		}
		health[member.AgentID] = item
	}
	return health, nil
}

func circuitLookupKey(target CircuitTarget) string {
	return string(target.Scope) + "\x00" + target.Key
}

func earlierTime(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return current
	}
	if current == nil || candidate.Before(*current) {
		value := *candidate
		return &value
	}
	return current
}

func (s *PostgresStore) DeferTaskForExecutionTarget(ctx context.Context, sessionID, taskID string, retryAt *time.Time, health []ExecutionTargetHealth) (RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, sessionID, ""); err != nil {
		return RunEvent{}, err
	}
	var workspaceID string
	var status TaskStatus
	if err = tx.QueryRow(ctx, `
		SELECT workspace_id::text, status FROM research_task
		WHERE id = $1::uuid AND session_id = $2::uuid FOR UPDATE
	`, taskID, sessionID).Scan(&workspaceID, &status); err != nil {
		return RunEvent{}, err
	}
	if status != TaskStatusReady {
		return RunEvent{}, tx.Commit(ctx)
	}
	if retryAt != nil {
		if _, err = tx.Exec(ctx, `
			UPDATE research_task SET ready_at = $2, updated_at = now()
			WHERE id = $1::uuid
		`, taskID, retryAt); err != nil {
			return RunEvent{}, err
		}
	}
	sort.Slice(health, func(i, j int) bool { return health[i].AgentID < health[j].AgentID })
	payloadHealth, _ := json.Marshal(health)
	fingerprint := ExecutionTargetFingerprint(string(payloadHealth))
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "task_waiting_for_execution_target",
		"task-waiting-target:"+taskID+":"+fingerprint, "system", "", map[string]any{
			"task_id": taskID, "retry_at": retryAt, "targets": health,
		})
	if err != nil {
		return RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RunEvent{}, err
	}
	return event, nil
}

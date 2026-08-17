package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QueueV6SteeringMessageTx binds a user utterance and its visible refs to one
// durable Director trigger. Non-V6 Runs are deliberately left on their pinned
// orchestration path.
func QueueV6SteeringMessageTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, messageID, clientRequestID string, selectedRefs json.RawMessage) error {
	var orchestrator, userID string
	if err := tx.QueryRow(ctx, `SELECT s.orchestrator_version,m.sender_id::text FROM research_session s
		JOIN research_message m ON m.workspace_id=s.workspace_id AND m.session_id=s.id
		WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND m.id=$3::uuid AND m.sender_type='user'`, workspaceID, runID, messageID).Scan(&orchestrator, &userID); err != nil {
		return err
	}
	if orchestrator != OrchestratorVersionV6 {
		return nil
	}
	if _, err := uuid.Parse(clientRequestID); err != nil {
		return fmt.Errorf("%w: steering client request ID", ErrInvalidContract)
	}
	selectedRefs = normalizedV6JSON(selectedRefs, `[]`)
	event, err := appendEvent(ctx, tx, workspaceID, runID, "v6_steering_message_received", "v6-steering-message:"+clientRequestID,
		"user", userID, map[string]any{"message_id": messageID, "selected_refs": json.RawMessage(selectedRefs)})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO research_v6_steering_trigger(workspace_id,session_id,research_message_id,client_request_id,selected_refs,event_sequence)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::jsonb,$6)
		ON CONFLICT (workspace_id,session_id,client_request_id) DO NOTHING`, workspaceID, runID, messageID, clientRequestID, selectedRefs, event.Sequence)
	return err
}

func (s *PostgresStore) ProcessV6SteeringTriggers(ctx context.Context, limit int) (int, error) {
	processed := 0
	for processed < limit {
		var id, workspaceID, runID, messageID string
		var eventSequence, throughSequence, stateVersion int64
		tx, err := s.beginResearchTx(ctx, txOpV6SteeringTriggerClaim, pgx.TxOptions{})
		if err != nil {
			return processed, err
		}
		err = tx.QueryRow(ctx, `SELECT t.id::text,t.workspace_id::text,t.session_id::text,t.research_message_id::text,t.event_sequence,
			COALESCE((SELECT max(sequence) FROM research_run_event e WHERE e.session_id=t.session_id),t.event_sequence),s.state_version
			FROM research_v6_steering_trigger t JOIN research_session s ON s.workspace_id=t.workspace_id AND s.id=t.session_id
			WHERE t.status IN ('pending','processing') AND t.next_attempt_at<=now() AND (t.lease_expires_at IS NULL OR t.lease_expires_at<now())
			ORDER BY t.created_at,t.id FOR UPDATE OF t SKIP LOCKED LIMIT 1`).Scan(&id, &workspaceID, &runID, &messageID, &eventSequence, &throughSequence, &stateVersion)
		if err == pgx.ErrNoRows {
			_ = tx.Rollback(ctx)
			return processed, nil
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return processed, err
		}
		token := uuid.NewString()
		_, err = tx.Exec(ctx, `UPDATE research_v6_steering_trigger SET status='processing',lease_token=$2::uuid,lease_expires_at=now()+interval '45 seconds',
			delivery_attempts=delivery_attempts+1,updated_at=now() WHERE id=$1::uuid`, id, token)
		if err == nil {
			err = s.commitResearchTx(ctx, txOpV6SteeringTriggerClaim, tx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if err != nil {
			return processed, err
		}
		cycle, cycleErr := (directorBriefModule{store: s, compiler: contextCompilerModule{}}).Start(ctx, StartV6DirectorCycleInput{
			WorkspaceID: workspaceID, RunID: runID, TriggerKey: "steering:" + messageID, FromSequence: eventSequence,
			ThroughSequence: throughSequence, ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
		})
		if cycleErr != nil {
			_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE research_v6_steering_trigger SET status='pending',lease_token=NULL,lease_expires_at=NULL,
				next_attempt_at=now()+interval '1 minute',last_error=$3,updated_at=now() WHERE id=$1::uuid AND lease_token=$2::uuid`, id, token, cycleErr.Error())
			continue
		}
		command, completeErr := s.pool.Exec(context.WithoutCancel(ctx), `UPDATE research_v6_steering_trigger SET status='triggered',director_cycle_id=$3::uuid,
			lease_token=NULL,lease_expires_at=NULL,last_error='',updated_at=now() WHERE id=$1::uuid AND lease_token=$2::uuid`, id, token, cycle.ID)
		if completeErr != nil {
			return processed, completeErr
		}
		if command.RowsAffected() != 1 {
			return processed, ErrWorkItemChanged
		}
		processed++
	}
	return processed, nil
}

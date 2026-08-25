// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// envCheckpointLaneQueries is the narrow generated-query surface the lane
// repository needs. *db.Queries satisfies it in production; tests substitute a
// fake that honours the same claim semantics.
type envCheckpointLaneQueries interface {
	ClaimEnvCheckpointLane(ctx context.Context, arg db.ClaimEnvCheckpointLaneParams) (db.EnvCheckpointLane, error)
	GetEnvCheckpointLane(ctx context.Context, arg db.GetEnvCheckpointLaneParams) (db.EnvCheckpointLane, error)
	ListEnvCheckpointLanes(ctx context.Context, arg db.ListEnvCheckpointLanesParams) ([]db.EnvCheckpointLane, error)
	UpdateEnvCheckpointLaneStep(ctx context.Context, arg db.UpdateEnvCheckpointLaneStepParams) (db.EnvCheckpointLane, error)
	MarkEnvCheckpointLaneReady(ctx context.Context, arg db.MarkEnvCheckpointLaneReadyParams) (db.EnvCheckpointLane, error)
	MarkEnvCheckpointLaneFailed(ctx context.Context, arg db.MarkEnvCheckpointLaneFailedParams) (db.EnvCheckpointLane, error)
	CountProvisioningEnvCheckpointLanes(ctx context.Context, arg db.CountProvisioningEnvCheckpointLanesParams) (int64, error)
}

// savepointReaderQueries is the narrow surface the savepoint reader needs.
type savepointReaderQueries interface {
	ListSandboxSnapshotsForCheckpoint(ctx context.Context, arg db.ListSandboxSnapshotsForCheckpointParams) ([]db.SandboxSnapshot, error)
	MarkSandboxSnapshotFailed(ctx context.Context, arg db.MarkSandboxSnapshotFailedParams) (db.SandboxSnapshot, error)
}

var (
	_ envCheckpointLaneQueries = (*db.Queries)(nil)
	_ savepointReaderQueries   = (*db.Queries)(nil)
)

type envCheckpointLaneRepo struct {
	q envCheckpointLaneQueries
}

// NewEnvCheckpointLaneRepository constructs the production lane repository over
// the generated queries.
func NewEnvCheckpointLaneRepository(q envCheckpointLaneQueries) EnvCheckpointLaneRepository {
	return &envCheckpointLaneRepo{q: q}
}

// ClaimLane inserts the lane row, or reports the claim lost when the unique
// index rejected it. Losing is the ordinary idempotent case, not a failure: the
// caller re-reads the existing row and continues from its status.
func (r *envCheckpointLaneRepo) ClaimLane(ctx context.Context, checkpointID, workspaceID, laneKey string) (EnvCheckpointLane, bool, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return EnvCheckpointLane{}, false, err
	}
	row, err := r.q.ClaimEnvCheckpointLane(ctx, db.ClaimEnvCheckpointLaneParams{
		CheckpointID: cpUUID,
		LaneKey:      laneKey,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnvCheckpointLane{}, false, nil
		}
		return EnvCheckpointLane{}, false, fmt.Errorf("claim lane %q: %w", laneKey, err)
	}
	// This is the one lane query that does not take workspace_id: it derives it
	// from the owning checkpoint, which is what keeps a lane in its checkpoint's
	// workspace. A disagreement means the caller loaded a checkpoint from another
	// tenant, so refuse rather than materialize into it.
	if row.WorkspaceID != wsUUID {
		return EnvCheckpointLane{}, false, fmt.Errorf(
			"lane %q derived workspace %s, caller passed %s", laneKey, uuidText(row.WorkspaceID), workspaceID)
	}
	return envCheckpointLaneFromRow(row), true, nil
}

func (r *envCheckpointLaneRepo) GetLane(ctx context.Context, checkpointID, workspaceID, laneKey string) (EnvCheckpointLane, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return EnvCheckpointLane{}, err
	}
	row, err := r.q.GetEnvCheckpointLane(ctx, db.GetEnvCheckpointLaneParams{
		CheckpointID: cpUUID,
		LaneKey:      laneKey,
		WorkspaceID:  wsUUID,
	})
	if err != nil {
		return EnvCheckpointLane{}, fmt.Errorf("get lane %q: %w", laneKey, err)
	}
	return envCheckpointLaneFromRow(row), nil
}

func (r *envCheckpointLaneRepo) ListLanes(ctx context.Context, checkpointID, workspaceID string) ([]EnvCheckpointLane, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListEnvCheckpointLanes(ctx, db.ListEnvCheckpointLanesParams{
		CheckpointID: cpUUID,
		WorkspaceID:  wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list lanes: %w", err)
	}
	out := make([]EnvCheckpointLane, 0, len(rows))
	for _, row := range rows {
		out = append(out, envCheckpointLaneFromRow(row))
	}
	return out, nil
}

// RecordLaneStep writes only the ids the step carries. The query COALESCEs, so
// every unset id must go down as NULL: sending a zero UUID instead would
// overwrite an id an earlier step recorded, and the interrupted-lane recovery
// depends on those ids surviving.
func (r *envCheckpointLaneRepo) RecordLaneStep(ctx context.Context, laneID, workspaceID string, step LaneStep) (EnvCheckpointLane, error) {
	laneUUID, wsUUID, err := parseLaneAndWorkspace(laneID, workspaceID)
	if err != nil {
		return EnvCheckpointLane{}, err
	}
	arg := db.UpdateEnvCheckpointLaneStepParams{ID: laneUUID, WorkspaceID: wsUUID}
	for _, field := range []struct {
		name  string
		value string
		dst   *pgtype.UUID
	}{
		{"instance_id", step.InstanceID, &arg.InstanceID},
		{"project_id", step.ProjectID, &arg.ProjectID},
		{"runtime_id", step.RuntimeID, &arg.RuntimeID},
		{"task_id", step.TaskID, &arg.TaskID},
		{"env_id", step.EnvID, &arg.EnvID},
		{"channel_id", step.ChannelID, &arg.ChannelID},
		{"chat_session_id", step.ChatSessionID, &arg.ChatSessionID},
		{"source_message_id", step.SourceMessageID, &arg.SourceMessageID},
	} {
		if field.value == "" {
			continue
		}
		parsed, err := util.ParseUUID(field.value)
		if err != nil {
			return EnvCheckpointLane{}, fmt.Errorf("parse lane step %s: %w", field.name, err)
		}
		*field.dst = parsed
	}
	row, err := r.q.UpdateEnvCheckpointLaneStep(ctx, arg)
	if err != nil {
		return EnvCheckpointLane{}, fmt.Errorf("record lane step: %w", err)
	}
	return envCheckpointLaneFromRow(row), nil
}

func (r *envCheckpointLaneRepo) MarkLaneReady(ctx context.Context, laneID, workspaceID string) (EnvCheckpointLane, error) {
	laneUUID, wsUUID, err := parseLaneAndWorkspace(laneID, workspaceID)
	if err != nil {
		return EnvCheckpointLane{}, err
	}
	row, err := r.q.MarkEnvCheckpointLaneReady(ctx, db.MarkEnvCheckpointLaneReadyParams{
		ID:          laneUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return EnvCheckpointLane{}, fmt.Errorf("mark lane ready: %w", err)
	}
	return envCheckpointLaneFromRow(row), nil
}

func (r *envCheckpointLaneRepo) MarkLaneFailed(ctx context.Context, laneID, workspaceID, reason string) (EnvCheckpointLane, error) {
	laneUUID, wsUUID, err := parseLaneAndWorkspace(laneID, workspaceID)
	if err != nil {
		return EnvCheckpointLane{}, err
	}
	row, err := r.q.MarkEnvCheckpointLaneFailed(ctx, db.MarkEnvCheckpointLaneFailedParams{
		ID:          laneUUID,
		WorkspaceID: wsUUID,
		Error:       textOrNull(reason),
	})
	if err != nil {
		return EnvCheckpointLane{}, fmt.Errorf("mark lane failed: %w", err)
	}
	return envCheckpointLaneFromRow(row), nil
}

func (r *envCheckpointLaneRepo) CountProvisioningLanes(ctx context.Context, checkpointID, workspaceID string) (int, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return 0, err
	}
	n, err := r.q.CountProvisioningEnvCheckpointLanes(ctx, db.CountProvisioningEnvCheckpointLanesParams{
		CheckpointID: cpUUID,
		WorkspaceID:  wsUUID,
	})
	if err != nil {
		return 0, fmt.Errorf("count provisioning lanes: %w", err)
	}
	return int(n), nil
}

func parseLaneAndWorkspace(laneID, workspaceID string) (pgtype.UUID, pgtype.UUID, error) {
	laneUUID, err := util.ParseUUID(laneID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse lane_id: %w", err)
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	return laneUUID, wsUUID, nil
}

func envCheckpointLaneFromRow(row db.EnvCheckpointLane) EnvCheckpointLane {
	lane := EnvCheckpointLane{
		ID:              uuidText(row.ID),
		CheckpointID:    uuidText(row.CheckpointID),
		WorkspaceID:     uuidText(row.WorkspaceID),
		LaneKey:         row.LaneKey,
		Status:          row.Status,
		InstanceID:      uuidText(row.InstanceID),
		ProjectID:       uuidText(row.ProjectID),
		RuntimeID:       uuidText(row.RuntimeID),
		TaskID:          uuidText(row.TaskID),
		EnvID:           uuidText(row.EnvID),
		ChannelID:       uuidText(row.ChannelID),
		ChatSessionID:   uuidText(row.ChatSessionID),
		SourceMessageID: uuidText(row.SourceMessageID),
	}
	if row.Error.Valid {
		lane.Error = row.Error.String
	}
	return lane
}

type savepointReader struct {
	q savepointReaderQueries
}

// NewSavepointReader constructs the production savepoint reader: it lists the
// savepoints a checkpoint owns, which is how fan-out resume finds the templates
// to build lanes from.
func NewSavepointReader(q savepointReaderQueries) SavepointReader {
	return &savepointReader{q: q}
}

func (r *savepointReader) ListSavepoints(ctx context.Context, checkpointID, workspaceID string) ([]Savepoint, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListSandboxSnapshotsForCheckpoint(ctx, db.ListSandboxSnapshotsForCheckpointParams{
		CheckpointID: cpUUID,
		WorkspaceID:  wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list savepoints for checkpoint %s: %w", checkpointID, err)
	}
	out := make([]Savepoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, Savepoint{
			SnapshotID:     uuidText(row.ID),
			CubeSnapshotID: row.CubeSnapshotID,
			InstanceID:     uuidText(row.InstanceID),
			Status:         row.Status,
		})
	}
	return out, nil
}

func (r *savepointReader) MarkSavepointFailed(ctx context.Context, snapshotID, workspaceID, reason string) error {
	snapUUID, err := util.ParseUUID(snapshotID)
	if err != nil {
		return fmt.Errorf("parse snapshot_id: %w", err)
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	if _, err := r.q.MarkSandboxSnapshotFailed(ctx, db.MarkSandboxSnapshotFailedParams{
		ID:          snapUUID,
		WorkspaceID: wsUUID,
		Error:       textOrNull(reason),
	}); err != nil {
		return fmt.Errorf("mark savepoint %s failed: %w", snapshotID, err)
	}
	return nil
}

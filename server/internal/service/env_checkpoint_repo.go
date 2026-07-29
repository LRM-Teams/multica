// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// envCheckpointQueries is the narrow generated-query surface the checkpoint
// repository needs. *db.Queries satisfies it in production; tests substitute an
// in-memory fake, which is what lets this adapter be covered without a database.
type envCheckpointQueries interface {
	CreateEnvCheckpoint(ctx context.Context, arg db.CreateEnvCheckpointParams) (db.EnvCheckpoint, error)
	GetEnvCheckpointForWorkspace(ctx context.Context, arg db.GetEnvCheckpointForWorkspaceParams) (db.EnvCheckpoint, error)
	ListEnvCheckpointsForProject(ctx context.Context, arg db.ListEnvCheckpointsForProjectParams) ([]db.EnvCheckpoint, error)
	UpdateEnvCheckpointSaveStatus(ctx context.Context, arg db.UpdateEnvCheckpointSaveStatusParams) (db.EnvCheckpoint, error)
	DeleteEnvCheckpoint(ctx context.Context, arg db.DeleteEnvCheckpointParams) error
}

// The production queries must keep satisfying the narrow surface: the fake in the
// tests would otherwise drift from *db.Queries and the tests would pass against
// an interface nothing real implements.
var _ envCheckpointQueries = (*db.Queries)(nil)

type envCheckpointRepo struct {
	q envCheckpointQueries
}

// NewEnvCheckpointRepository constructs the production checkpoint repository
// over the generated queries.
func NewEnvCheckpointRepository(q envCheckpointQueries) EnvCheckpointRepository {
	return &envCheckpointRepo{q: q}
}

func (r *envCheckpointRepo) CreateCheckpoint(ctx context.Context, in EnvCheckpointCreateInput, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error) {
	wsUUID, err := util.ParseUUID(in.WorkspaceID)
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	projUUID, err := util.ParseUUID(in.ProjectID)
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("parse project_id: %w", err)
	}
	envIDMap, err := jsonOrEmptyObject(in.EnvIDMap)
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("encode env_id_map: %w", err)
	}
	sandboxRefs, err := jsonOrEmptyArray(in.SandboxRefs)
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("encode sandbox_refs: %w", err)
	}
	// save_mode is NOT NULL with a pause_in_place default, and the CHECK
	// constraint rejects the empty string, so an unset mode resolves here rather
	// than failing at the database.
	mode := in.SaveMode
	if mode == "" {
		mode = SaveModePauseInPlace
	}
	entropy := pgtype.Float8{}
	if in.EntropyScore != nil {
		entropy = pgtype.Float8{Float64: *in.EntropyScore, Valid: true}
	}
	row, err := r.q.CreateEnvCheckpoint(ctx, db.CreateEnvCheckpointParams{
		WorkspaceID:    wsUUID,
		ProjectID:      projUUID,
		EventRef:       in.EventRef,
		CheckpointKind: in.Kind,
		EnvIDMap:       envIDMap,
		SandboxRefs:    sandboxRefs,
		DbSnapshot:     jsonOrNullLiteral(in.DBSnapshot),
		EntropyScore:   entropy,
		SaveTimeoutMs:  int32(in.SaveTimeout.Milliseconds()),
		SaveStatus:     string(status),
		SaveError:      textOrNull(saveErr),
		ResumeTrigger:  nullableJSON(in.ResumeTrigger),
		SaveMode:       string(mode),
	})
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("create env checkpoint: %w", err)
	}
	return envCheckpointFromRow(row)
}

func (r *envCheckpointRepo) UpdateCheckpointSaveStatus(ctx context.Context, checkpointID, workspaceID string, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return EnvCheckpoint{}, err
	}
	row, err := r.q.UpdateEnvCheckpointSaveStatus(ctx, db.UpdateEnvCheckpointSaveStatusParams{
		SaveStatus:  string(status),
		SaveError:   textOrNull(saveErr),
		ID:          cpUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("update env checkpoint save status: %w", err)
	}
	return envCheckpointFromRow(row)
}

func (r *envCheckpointRepo) GetCheckpoint(ctx context.Context, checkpointID, workspaceID string) (EnvCheckpoint, error) {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return EnvCheckpoint{}, err
	}
	row, err := r.q.GetEnvCheckpointForWorkspace(ctx, db.GetEnvCheckpointForWorkspaceParams{
		ID:          cpUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("get env checkpoint: %w", err)
	}
	return envCheckpointFromRow(row)
}

func (r *envCheckpointRepo) DeleteCheckpoint(ctx context.Context, checkpointID, workspaceID string) error {
	cpUUID, wsUUID, err := parseCheckpointAndWorkspace(checkpointID, workspaceID)
	if err != nil {
		return err
	}
	if err := r.q.DeleteEnvCheckpoint(ctx, db.DeleteEnvCheckpointParams{
		ID:          cpUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		return fmt.Errorf("delete env checkpoint: %w", err)
	}
	return nil
}

func (r *envCheckpointRepo) ListCheckpoints(ctx context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("parse workspace_id: %w", err)
	}
	projUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return nil, fmt.Errorf("parse project_id: %w", err)
	}
	rows, err := r.q.ListEnvCheckpointsForProject(ctx, db.ListEnvCheckpointsForProjectParams{
		WorkspaceID: wsUUID,
		ProjectID:   projUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list env checkpoints: %w", err)
	}
	out := make([]EnvCheckpoint, 0, len(rows))
	for _, row := range rows {
		cp, err := envCheckpointFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

func parseCheckpointAndWorkspace(checkpointID, workspaceID string) (pgtype.UUID, pgtype.UUID, error) {
	cpUUID, err := util.ParseUUID(checkpointID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse checkpoint_id: %w", err)
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	return cpUUID, wsUUID, nil
}

func envCheckpointFromRow(row db.EnvCheckpoint) (EnvCheckpoint, error) {
	cp := EnvCheckpoint{
		ID:            uuidText(row.ID),
		WorkspaceID:   uuidText(row.WorkspaceID),
		ProjectID:     uuidText(row.ProjectID),
		EventRef:      row.EventRef,
		Kind:          row.CheckpointKind,
		SaveMode:      EnvCheckpointSaveMode(row.SaveMode),
		SaveTimeoutMs: int(row.SaveTimeoutMs),
		SaveStatus:    EnvCheckpointStatus(row.SaveStatus),
	}
	// A row written before migration 246 resolves to pause_in_place, matching the
	// column default. Leaving it empty would make ResumeFromCheckpoint see an
	// unknown mode and reject a checkpoint that is perfectly resumable in place.
	if cp.SaveMode == "" {
		cp.SaveMode = SaveModePauseInPlace
	}
	if row.SaveError.Valid {
		cp.SaveError = row.SaveError.String
	}
	if row.EntropyScore.Valid {
		score := row.EntropyScore.Float64
		cp.EntropyScore = &score
	}
	if row.CreatedAt.Valid {
		cp.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		cp.UpdatedAt = row.UpdatedAt.Time
	}
	if len(row.EnvIDMap) > 0 {
		if err := json.Unmarshal(row.EnvIDMap, &cp.EnvIDMap); err != nil {
			return EnvCheckpoint{}, fmt.Errorf("decode env_id_map for checkpoint %s: %w", cp.ID, err)
		}
	}
	if len(row.SandboxRefs) > 0 {
		if err := json.Unmarshal(row.SandboxRefs, &cp.SandboxRefs); err != nil {
			return EnvCheckpoint{}, fmt.Errorf("decode sandbox_refs for checkpoint %s: %w", cp.ID, err)
		}
	}
	if len(row.DbSnapshot) > 0 {
		cp.DBSnapshot = json.RawMessage(row.DbSnapshot)
	}
	if len(row.ResumeTrigger) > 0 {
		cp.ResumeTrigger = json.RawMessage(row.ResumeTrigger)
	}
	return cp, nil
}

func uuidText(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return util.UUIDToString(u)
}

func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// nullableJSON keeps an absent value NULL rather than storing the four bytes
// "null", so `resume_trigger IS NULL` stays the way to ask whether a checkpoint
// recorded a trigger.
func nullableJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// jsonOrNullLiteral is for the NOT NULL JSONB columns, where an absent value has
// to be stored as a JSON null rather than a SQL NULL.
func jsonOrNullLiteral(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`null`)
	}
	return raw
}

func jsonOrEmptyObject(v map[string]string) ([]byte, error) {
	if v == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(v)
}

func jsonOrEmptyArray(v []SandboxInstanceRef) ([]byte, error) {
	if v == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(v)
}

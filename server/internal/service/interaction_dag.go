// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EdgeType values for interaction_dag_edge.type (CHECK-constrained).
const (
	EdgeTypeDelegation = "delegation"
	EdgeTypeMention    = "mention"
	EdgeTypeCompletion = "completion"
)

// ErrInvalidEdgeType is returned by AddEdge when edgeType is not one of the
// three allowed EdgeType values.
var ErrInvalidEdgeType = errors.New("interaction_dag: invalid edge type")

var validEdgeTypes = map[string]bool{
	EdgeTypeDelegation: true,
	EdgeTypeMention:    true,
	EdgeTypeCompletion: true,
}

// InteractionDAGStore is the DB seam the InteractionDAGService records
// through. *db.Queries satisfies it; tests inject a fake.
type InteractionDAGStore interface {
	UpsertInteractionDAGSessionRun(ctx context.Context, arg db.UpsertInteractionDAGSessionRunParams) error
	GetInteractionDAGSessionRun(ctx context.Context, sessionID string) (db.InteractionDAGSessionRun, error)
	InsertInteractionDAGSegmentWithSnapshot(ctx context.Context, arg db.InsertInteractionDAGSegmentWithSnapshotParams) error
	InsertInteractionDAGEdge(ctx context.Context, arg db.InsertInteractionDAGEdgeParams) error
}

// Compile-time guarantee that *db.Queries satisfies InteractionDAGStore, so a
// generated-method signature drift fails at compile time now, not at U7.2 wiring.
var _ InteractionDAGStore = (*db.Queries)(nil)

// ArealSegmentClient is the areal RL bridge seam for closing+exporting a
// segment. *arealrl.Client satisfies it; tests inject a fake. The client
// authenticates ExportTrajectory with its internally-held admin key, so the
// service does not take a separate admin-key parameter.
type ArealSegmentClient interface {
	CloseSegment(ctx context.Context, proxyKey string) (int, error)
	ExportTrajectory(ctx context.Context, sessionID string, trajectoryID int) (json.RawMessage, error)
}

var _ ArealSegmentClient = (*arealrl.Client)(nil)

// InteractionDAGService records communication-bounded segments + typed edges
// during trained rollouts. It is the multica-side recorder consumed by U8's
// AssembledDag assembly. Behind the INTERACTION_DAG_ENABLED gate (composed
// with the training gate at the hook layer, U7.2): a disabled service is a
// no-op and touches neither the DB nor the bridge.
//
// Per the plan, a recording error is returned to the caller; the hook layer
// (U7.2) decides to log-and-continue so a recording failure never breaks a
// trained run. The service itself does not panic or swallow errors.
type InteractionDAGService struct {
	store   InteractionDAGStore
	client  ArealSegmentClient
	enabled bool
}

// NewInteractionDAGService constructs the recorder. When enabled is false the
// service is a no-op (RecordSessionAgentRun/AddEdge/CloseSegmentForEvent
// return nil without touching the DB or bridge).
func NewInteractionDAGService(store InteractionDAGStore, client ArealSegmentClient, enabled bool) *InteractionDAGService {
	return &InteractionDAGService{store: store, client: client, enabled: enabled}
}

// Enabled reports whether segment-DAG recording is active.
func (s *InteractionDAGService) Enabled() bool { return s.enabled }

// RecordSessionAgentRun upserts the {session_id -> agent_run_id, issue_id}
// mapping (D8: agent_run_id = task.ID, the multica agent_task_queue PK). Called
// once at session-open (maybeOpenTrainingSession, U7.2 wiring). Idempotent on
// session_id: a retry that re-opens a session re-binds it to the latest run.
//
// Note: the plan's 3-param signature is extended to 4 to also store issue_id,
// which is known at session-open and avoids a later task lookup at close time.
func (s *InteractionDAGService) RecordSessionAgentRun(ctx context.Context, projectID, sessionID, agentRunID, issueID string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	if projectID == "" || sessionID == "" || agentRunID == "" {
		return errors.New("interaction_dag: RecordSessionAgentRun requires project_id, session_id, agent_run_id")
	}
	return s.store.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
		SessionID:  sessionID,
		ProjectID:  projectID,
		AgentRunID: agentRunID,
		IssueID:    pgText(issueID),
	})
}

// CloseSegmentForEvent closes the session's current segment via the arealrl
// bridge, exports its trajectory, decodes tensor_ref, and records a segment row
// + env snapshot. closingEvent is "" for a leaf (root-completion) segment,
// which stores closing_event as NULL. Returns the new segment_id.
//
// Flow: (a) look up agent_run_id+issue_id by sessionID from
// interaction_dag_session_run; (b) arealrl.CloseSegment(proxyKey) ->
// trajectoryID; (c) arealrl.ExportTrajectory(sessionID, trajectoryID) -> raw
// traj JSON; (d) decode tensor_ref; (e) insert interaction_dag_segment +
// interaction_dag_env_snapshot.
//
// adminKey is NOT a parameter: the arealrl.Client holds the admin key
// internally and ExportTrajectory uses it; proxyKey is the only per-call
// credential (session-key for CloseSegment). This drops the plan's redundant
// adminKey parameter - see U7.1 report.
func (s *InteractionDAGService) CloseSegmentForEvent(
	ctx context.Context,
	projectID, sessionID, proxyKey, closingEvent string,
	envSnapshot map[string]any,
) (string, error) {
	if !s.enabled {
		return "", nil
	}
	if s.store == nil || s.client == nil {
		return "", errors.New("interaction_dag: service not fully configured (store or client nil)")
	}
	if projectID == "" || sessionID == "" || proxyKey == "" {
		return "", errors.New("interaction_dag: CloseSegmentForEvent requires project_id, session_id, proxy_key")
	}

	// (a) resolve agent_run_id + issue_id from the session mapping.
	run, err := s.store.GetInteractionDAGSessionRun(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: lookup session_run for %s: %w", sessionID, err)
	}

	// (b) close the segment -> trajectory_id.
	trajectoryID, err := s.client.CloseSegment(ctx, proxyKey)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: close_segment for session %s: %w", sessionID, err)
	}

	// (c) export the just-closed trajectory -> raw traj JSON.
	raw, err := s.client.ExportTrajectory(ctx, sessionID, trajectoryID)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: export_trajectory %s/%d: %w", sessionID, trajectoryID, err)
	}

	// (d) decode tensor_ref from the export payload.
	tensorRef, err := decodeTensorRef(raw)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: decode tensor_ref for session %s: %w", sessionID, err)
	}

	// (e) record the segment + env snapshot atomically: a single data-modifying
	// CTE inserts both rows in one statement so a snapshot failure can never
	// orphan the segment (paired operations stay together). segment_id is known
	// ahead of time (<sessionID>-<trajectoryID>) and is reused as the snapshot FK.
	segmentID := fmt.Sprintf("%s-%d", sessionID, trajectoryID)
	sandboxIDs, issueSnapshotID, envState := encodeEnvSnapshot(envSnapshot)
	if err := s.store.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID:       segmentID,
		ProjectID:       projectID,
		AgentRunID:      run.AgentRunID,
		IssueID:         run.IssueID, // carry the looked-up issue_id (do not re-derive)
		TrajectoryID:    int64(trajectoryID),
		TensorRef:       tensorRef,
		ClosingEvent:    pgText(closingEvent),
		SandboxIDs:      sandboxIDs,
		IssueSnapshotID: issueSnapshotID,
		EnvState:        envState,
	}); err != nil {
		return "", fmt.Errorf("interaction_dag: insert segment+env_snapshot %s: %w", segmentID, err)
	}

	return segmentID, nil
}

// AddEdge records a typed DAG edge between two segments. edgeType must be one
// of delegation/mention/completion (the interaction_dag_edge.type CHECK values)
// else ErrInvalidEdgeType is returned. No FK to interaction_dag_segment so an
// edge can be recorded before both endpoints are known (best-effort); U8's
// assembly validates integrity.
func (s *InteractionDAGService) AddEdge(ctx context.Context, projectID, srcSegmentID, dstSegmentID, edgeType string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	if !validEdgeTypes[edgeType] {
		return fmt.Errorf("%w: %q (want delegation|mention|completion)", ErrInvalidEdgeType, edgeType)
	}
	if projectID == "" || srcSegmentID == "" || dstSegmentID == "" {
		return errors.New("interaction_dag: AddEdge requires project_id, src_segment_id, dst_segment_id")
	}
	return s.store.InsertInteractionDAGEdge(ctx, db.InsertInteractionDAGEdgeParams{
		ProjectID:    projectID,
		SrcSegmentID: srcSegmentID,
		DstSegmentID: dstSegmentID,
		Type:         edgeType,
	})
}

// decodeTensorRef extracts the tensor_ref JSON object from the raw exported
// trajectory payload. The areal export contract pins tensor_ref as a JSON
// object ({"shard_id": str}); it may appear either as a top-level "tensor_ref"
// field or, when the export returns the ref object directly, as the payload
// itself. The result is stored as jsonb verbatim - no fields are hardcoded.
//
// Absence (no object found) is reported as an error rather than default-filled:
// a fabricated default would destroy the only signal downstream layers have
// (boundary: absence stays distinguishable).
//
// The exact export shape is finalized by U6/U8; this decode is intentionally
// tolerant of both plausible shapes and is a U7.2 integration concern.
func decodeTensorRef(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty export payload, no tensor_ref")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode export payload: %w", err)
	}
	if ref, ok := m["tensor_ref"]; ok && len(ref) > 0 && string(ref) != "null" {
		return ref, nil
	}
	// Fallback: the payload itself is the ref object (e.g. {"shard_id":...}).
	var probe any
	if err := json.Unmarshal(raw, &probe); err == nil {
		if _, isObj := probe.(map[string]any); isObj {
			return raw, nil
		}
	}
	return nil, errors.New("tensor_ref not found in export payload")
}

// encodeEnvSnapshot splits a generic env-snapshot map into the three
// interaction_dag_env_snapshot columns. sandbox_ids and env_state are NOT NULL
// jsonb (default []/{} when absent); issue_snapshot_id is nullable text. When
// an explicit "env_state" key is present it is used verbatim, otherwise the
// whole snapshot map is stored as env_state so nothing is lost.
func encodeEnvSnapshot(snap map[string]any) (sandboxIDs []byte, issueSnapshotID pgtype.Text, envState []byte) {
	if v, ok := snap["sandbox_ids"]; ok {
		sandboxIDs, _ = json.Marshal(v)
	}
	if len(sandboxIDs) == 0 {
		sandboxIDs = []byte("[]")
	}
	if v, ok := snap["issue_snapshot_id"]; ok {
		if s, ok := v.(string); ok && s != "" {
			issueSnapshotID = pgtype.Text{String: s, Valid: true}
		}
	}
	if v, ok := snap["env_state"]; ok {
		envState, _ = json.Marshal(v)
	} else if len(snap) == 0 {
		// nil/empty snapshot: json.Marshal(nil map) yields "null"; use "{}" to
		// match the column's DEFAULT '{}'::jsonb intent.
		envState = []byte("{}")
	} else {
		envState, _ = json.Marshal(snap)
	}
	if len(envState) == 0 {
		envState = []byte("{}")
	}
	return sandboxIDs, issueSnapshotID, envState
}

// pgText converts a Go string to pgtype.Text, treating "" as SQL NULL. Used for
// the nullable text columns (issue_id, task_id, closing_event,
// closing_event_target_segment, issue_snapshot_id).
func pgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

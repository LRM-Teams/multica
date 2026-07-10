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
	GetInteractionDAGSegmentByAgentRun(ctx context.Context, agentRunID string) (db.InteractionDAGSegment, error)
	GetLastEndSeqForAgentRun(ctx context.Context, agentRunID string) (int32, error)
	GetMaxTaskMessageSeq(ctx context.Context, taskIDText string) (int32, error)

	// Read-only assembly queries (U8 AssembleAssembledDag). Each filters by
	// project_id; env_snapshot joins through segment (no project_id column).
	ListInteractionDAGSegmentsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGSegment, error)
	ListInteractionDAGEdgesForProject(ctx context.Context, projectID string) ([]db.InteractionDAGEdge, error)
	ListInteractionDAGSessionRunsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGSessionRun, error)
	ListInteractionDAGEnvSnapshotsForProject(ctx context.Context, projectID string) ([]db.InteractionDAGEnvSnapshot, error)
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

// SegmentIDForAgentRun resolves the segment_id recorded for a task by
// agent_run_id (= task.ID, D8). Used by the DELEGATION-edge hook (D11) to find
// the parent's segment at the child's close. Returns ("", nil) when the service
// is disabled; returns ("", pgx.ErrNoRows) when no segment exists yet (the
// parent has not been closed/recorded) so the caller can skip the edge
// best-effort without logging a warning.
func (s *InteractionDAGService) SegmentIDForAgentRun(ctx context.Context, agentRunID string) (string, error) {
	if !s.enabled || s.store == nil {
		return "", nil
	}
	if agentRunID == "" {
		return "", errors.New("interaction_dag: SegmentIDForAgentRun requires agent_run_id")
	}
	seg, err := s.store.GetInteractionDAGSegmentByAgentRun(ctx, agentRunID)
	if err != nil {
		return "", err
	}
	return seg.SegmentID, nil
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

	// Calculate the turn range for this segment.
	lastEndSeq, err := s.store.GetLastEndSeqForAgentRun(ctx, run.AgentRunID)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: get last end_seq for %s: %w", run.AgentRunID, err)
	}
	startSeq := lastEndSeq + 1
	endSeq, err := s.store.GetMaxTaskMessageSeq(ctx, run.AgentRunID)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: get max task_message seq for %s: %w", run.AgentRunID, err)
	}

	if err := s.store.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID:                 segmentID,
		ProjectID:                 projectID,
		AgentRunID:                run.AgentRunID,
		IssueID:                   run.IssueID, // carry the looked-up issue_id (do not re-derive)
		TrajectoryID:              int64(trajectoryID),
		TensorRef:                 tensorRef,
		ClosingEvent:              pgText(closingEvent),
		StartSeq:                  startSeq,
		EndSeq:                    endSeq,
		SandboxIDs:                sandboxIDs,
		IssueSnapshotID:           issueSnapshotID,
		EnvState:                  envState,
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

// AssembledDag is the read-only assembled DAG consumed by AReaL. Its JSON shape
// mirrors areal's AssembledDag.from_dict (SegmentSpec/EdgeSpec dataclasses) so
// AReaL can parse it with SegmentSpec(**s) / EdgeSpec(**e). The segment/edge
// structs carry EXACTLY the keys those dataclasses accept - extra keys would
// TypeError at parse time on the areal side, so do not add fields without
// updating the areal contract.
type AssembledDag struct {
	Segments          []AssembledSegment `json:"segments"`
	Edges             []AssembledEdge    `json:"edges"`
	SessionToAgentRun map[string]string  `json:"session_to_agent_run"`
}

// AssembledSegment mirrors areal's SegmentSpec: structure only - no judge
// scores, no turn indices, no message text. tensor_ref is the field->shard jsonb
// decoded at record time (Task 1) and passed through verbatim as
// json.RawMessage. closing_event is str|None (nil -> JSON null). env_snapshot
// is reconstructed from the three interaction_dag_env_snapshot columns. issue_id
// is a non-optional str on the areal side; a NULL DB issue_id is emitted as "".
type AssembledSegment struct {
	SegmentID    string               `json:"segment_id"`
	AgentRunID   string               `json:"agent_run_id"`
	IssueID      string               `json:"issue_id"`
	TrajectoryID int64                `json:"trajectory_id"`
	TensorRef    json.RawMessage      `json:"tensor_ref"`
	ClosingEvent *string              `json:"closing_event"`
	EnvSnapshot  AssembledEnvSnapshot `json:"env_snapshot"`
}

// AssembledEnvSnapshot is the inverse of encodeEnvSnapshot: the three
// interaction_dag_env_snapshot columns reassembled into the dict AReaL expects.
// sandbox_ids and env_state are jsonb passed through verbatim (json.RawMessage);
// issue_snapshot_id is nullable (nil -> JSON null).
type AssembledEnvSnapshot struct {
	SandboxIDs      json.RawMessage `json:"sandbox_ids"`
	IssueSnapshotID *string         `json:"issue_snapshot_id"`
	EnvState        json.RawMessage `json:"env_state"`
}

// AssembledEdge mirrors areal's EdgeSpec. type is one of
// delegation/mention/completion (the interaction_dag_edge.type CHECK values).
// branch_from_segment_id / branch_from_checkpoint_id are branch provenance for
// change 2 (MCTS); emitted nil for now but the fields are present so the JSON
// contract is stable when MCTS lands.
type AssembledEdge struct {
	SrcSegmentID           string  `json:"src_segment_id"`
	DstSegmentID           string  `json:"dst_segment_id"`
	Type                   string  `json:"type"`
	BranchFromSegmentID    *string `json:"branch_from_segment_id"`
	BranchFromCheckpointID *string `json:"branch_from_checkpoint_id"`
}

// AssembleAssembledDag is the read-only assembly consumed by AReaL (U8). It
// reads recorded interaction_dag_segment/_edge/_env_snapshot/_session_run rows
// for a project and returns AssembledDag{Segments, Edges, SessionToAgentRun}.
//
// NO judge scores, NO turn indices, NO message text cross this boundary: the
// returned structs carry exactly the SegmentSpec/EdgeSpec fields AReaL's
// AssembledDag.from_dict parses. tensor_ref is the field->shard jsonb decoded
// at record time (Task 1) and passed through verbatim (not re-decoded);
// env_snapshot is reconstructed from the three env_snapshot columns. agent_run_id
// is the multica task.ID (the run), NOT the agent UUID.
//
// A disabled service (or nil store) returns an empty AssembledDag, nil -
// consistent with the other service methods. Branch provenance
// (branch_from_segment_id/branch_from_checkpoint_id) is change 2 (MCTS) and
// emitted nil for now; the fields are present so the contract is stable.
func (s *InteractionDAGService) AssembleAssembledDag(ctx context.Context, projectID string) (AssembledDag, error) {
	if !s.enabled || s.store == nil {
		return AssembledDag{}, nil
	}
	if projectID == "" {
		return AssembledDag{}, errors.New("interaction_dag: AssembleAssembledDag requires project_id")
	}

	segs, err := s.store.ListInteractionDAGSegmentsForProject(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list segments for %s: %w", projectID, err)
	}
	edges, err := s.store.ListInteractionDAGEdgesForProject(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list edges for %s: %w", projectID, err)
	}
	runs, err := s.store.ListInteractionDAGSessionRunsForProject(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list session_runs for %s: %w", projectID, err)
	}
	snaps, err := s.store.ListInteractionDAGEnvSnapshotsForProject(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list env_snapshots for %s: %w", projectID, err)
	}

	// Index env_snapshots by segment_id for an O(1) 1:1 join (the atomic CTE
	// in CloseSegmentForEvent guarantees every segment has exactly one snapshot;
	// the map makes assembly defensive if that invariant ever drifts).
	snapBySeg := make(map[string]db.InteractionDAGEnvSnapshot, len(snaps))
	for _, sn := range snaps {
		snapBySeg[sn.SegmentID] = sn
	}

	out := AssembledDag{
		Segments:          make([]AssembledSegment, 0, len(segs)),
		Edges:             make([]AssembledEdge, 0, len(edges)),
		SessionToAgentRun: make(map[string]string, len(runs)),
	}
	for _, sg := range segs {
		// issue_id is a non-optional str in SegmentSpec; NULL -> "".
		issueID := ""
		if sg.IssueID.Valid {
			issueID = sg.IssueID.String
		}
		// closing_event is str|None; NULL -> nil (JSON null).
		var closingEvent *string
		if sg.ClosingEvent.Valid {
			ce := sg.ClosingEvent.String
			closingEvent = &ce
		}
		seg := AssembledSegment{
			SegmentID:    sg.SegmentID,
			AgentRunID:   sg.AgentRunID,
			IssueID:      issueID,
			TrajectoryID: sg.TrajectoryID,
			TensorRef:    json.RawMessage(sg.TensorRef),
			ClosingEvent: closingEvent,
		}
		if sn, ok := snapBySeg[sg.SegmentID]; ok {
			seg.EnvSnapshot = assembleEnvSnapshot(sn)
		} else {
			// Absence stays distinguishable: a segment without a snapshot row
			// (unreachable given the atomic insert, but assembly is defensive)
			// gets an empty env_snapshot, not a fabricated meaningful one.
			seg.EnvSnapshot = AssembledEnvSnapshot{
				SandboxIDs: json.RawMessage("[]"),
				EnvState:   json.RawMessage("{}"),
			}
		}
		out.Segments = append(out.Segments, seg)
	}
	for _, e := range edges {
		out.Edges = append(out.Edges, AssembledEdge{
			SrcSegmentID: e.SrcSegmentID,
			DstSegmentID: e.DstSegmentID,
			Type:         e.Type,
			// Branch provenance is change 2 (MCTS); emit nil for now.
		})
	}
	for _, r := range runs {
		out.SessionToAgentRun[r.SessionID] = r.AgentRunID
	}
	return out, nil
}

// assembleEnvSnapshot reconstructs the env_snapshot dict from the three
// interaction_dag_env_snapshot columns (sandbox_ids, issue_snapshot_id,
// env_state). It is the inverse of encodeEnvSnapshot: the jsonb columns are
// passed through verbatim as json.RawMessage (no re-decode/re-encode), and the
// nullable issue_snapshot_id becomes *string (nil -> JSON null).
func assembleEnvSnapshot(sn db.InteractionDAGEnvSnapshot) AssembledEnvSnapshot {
	out := AssembledEnvSnapshot{
		SandboxIDs: json.RawMessage(sn.SandboxIDs),
		EnvState:   json.RawMessage(sn.EnvState),
	}
	if sn.IssueSnapshotID.Valid {
		s := sn.IssueSnapshotID.String
		out.IssueSnapshotID = &s
	}
	return out
}

// decodeTensorRef extracts a field->shard map from the areal /export_trajectories
// traj dict. The real export emits one serialized RTensor per tensor field
// (input_ids, attention_mask, logprobs, loss_mask, versions); each RTensor's
// data.shard.data carries {shard_id, node_addr}. We store that field->ref map as
// the segment's tensor_ref jsonb. Absence (a field with no shard_id) is an error,
// not a default - downstream resolution would KeyError on a masked None.
func decodeTensorRef(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty export payload, no tensor_ref")
	}
	var traj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &traj); err != nil {
		return nil, fmt.Errorf("decode export payload: %w", err)
	}
	if len(traj) == 0 {
		return nil, errors.New("empty export payload, no tensor fields")
	}
	out := make(map[string]map[string]string, len(traj))
	for field, fieldRaw := range traj {
		ref, err := extractShardRef(fieldRaw)
		if err != nil {
			return nil, fmt.Errorf("tensor_ref field %q: %w", field, err)
		}
		out[field] = ref
	}
	return json.Marshal(out)
}

// extractShardRef tolerates either a bare {"shard_id","node_addr"} ref or the
// full serialized RTensor dataclass envelope (data.shard.data.{shard_id,node_addr}).
func extractShardRef(fieldRaw json.RawMessage) (map[string]string, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(fieldRaw, &probe); err != nil {
		return nil, fmt.Errorf("not an object: %w", err)
	}
	if sidRaw, ok := probe["shard_id"]; ok {
		var s string
		if err := json.Unmarshal(sidRaw, &s); err == nil && s != "" {
			ref := map[string]string{"shard_id": s}
			if na, ok := probe["node_addr"]; ok {
				var nodeAddr string
				_ = json.Unmarshal(na, &nodeAddr)
				ref["node_addr"] = nodeAddr
			}
			return ref, nil
		}
	}
	type shardInfo struct {
		ShardID  string `json:"shard_id"`
		NodeAddr string `json:"node_addr"`
	}
	type envelope struct {
		Data struct {
			Shard struct {
				Data shardInfo `json:"data"`
			} `json:"shard"`
		} `json:"data"`
	}
	var env envelope
	if err := json.Unmarshal(fieldRaw, &env); err != nil {
		return nil, fmt.Errorf("no shard_id and not an RTensor envelope: %w", err)
	}
	if env.Data.Shard.Data.ShardID == "" {
		return nil, errors.New("RTensor envelope missing shard_id")
	}
	return map[string]string{"shard_id": env.Data.Shard.Data.ShardID, "node_addr": env.Data.Shard.Data.NodeAddr}, nil
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

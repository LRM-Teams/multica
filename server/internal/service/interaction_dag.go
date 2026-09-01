// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EdgeType values for interaction_dag_edge.type (CHECK-constrained).
const (
	EdgeTypeContinues  = "continues"
	EdgeTypeRespondsTo = "responds_to"
	EdgeTypeDelegation = "delegates_to"
	EdgeTypeMention    = "mentions"
)

// ErrInvalidEdgeType is returned by AddEdge when edgeType is not a canonical
// interaction_dag_edge type.
var ErrInvalidEdgeType = errors.New("interaction_dag: invalid edge type")

var validEdgeTypes = map[string]bool{
	EdgeTypeContinues:  true,
	EdgeTypeRespondsTo: true,
	EdgeTypeDelegation: true,
	EdgeTypeMention:    true,
}

// InteractionDAGStore is the DB seam the InteractionDAGService records
// through. *db.Queries satisfies it; tests inject a fake.
type InteractionDAGStore interface {
	UpsertInteractionDAGSessionRun(ctx context.Context, arg db.UpsertInteractionDAGSessionRunParams) error
	GetInteractionDAGSessionRun(ctx context.Context, sessionID string) (db.InteractionDagSessionRun, error)
	InsertInteractionDAGSegmentWithSnapshot(ctx context.Context, arg db.InsertInteractionDAGSegmentWithSnapshotParams) (string, error)
	GetInteractionDAGSegmentByAgentRun(ctx context.Context, agentRunID pgtype.UUID) (db.GetInteractionDAGSegmentByAgentRunRow, error)
	GetInteractionDAGSegmentByID(ctx context.Context, segmentID string) (db.GetInteractionDAGSegmentByIDRow, error)
	GetLastEndSeqForAgentRun(ctx context.Context, agentRunID pgtype.UUID) (int32, error)
	GetMaxTaskMessageSeq(ctx context.Context, taskIDText string) (int32, error)
	AllocateUniversalDAGEdgeSeq(ctx context.Context, workspaceID pgtype.UUID) (int64, error)
	GetUniversalDAGEdgeTriggerMessageID(ctx context.Context, arg db.GetUniversalDAGEdgeTriggerMessageIDParams) (pgtype.UUID, error)
	InsertUniversalDAGEdge(ctx context.Context, arg db.InsertUniversalDAGEdgeParams) (db.InteractionDagEdge, error)
	InsertUniversalDAGEdgeAtomic(ctx context.Context, arg db.InsertUniversalDAGEdgeAtomicParams) (db.InteractionDagEdge, error)

	// Read-only assembly queries resolve the canonical Workspace first and then
	// filter both Segments and Edge endpoints to the requested project view.
	GetUniversalDAGProjectWorkspace(ctx context.Context, projectID string) (pgtype.UUID, error)
	ListInteractionDAGSegmentsForProject(ctx context.Context, arg db.ListInteractionDAGSegmentsForProjectParams) ([]db.ListInteractionDAGSegmentsForProjectRow, error)
	ListInteractionDAGEdgesForProject(ctx context.Context, arg db.ListInteractionDAGEdgesForProjectParams) ([]db.ListInteractionDAGEdgesForProjectRow, error)
	ListInteractionDAGSessionRunsForProject(ctx context.Context, projectID string) ([]db.InteractionDagSessionRun, error)
	ListInteractionDAGEnvSnapshotsForProject(ctx context.Context, arg db.ListInteractionDAGEnvSnapshotsForProjectParams) ([]db.InteractionDagEnvSnapshot, error)
	InsertInteractionDAGStepReward(ctx context.Context, arg db.InsertInteractionDAGStepRewardParams) error
	ListInteractionDAGStepRewardsForProject(ctx context.Context, arg db.ListInteractionDAGStepRewardsForProjectParams) ([]db.InteractionDagStepReward, error)
	ListLatestCompletedInteractionDAGDiagnosisTargetsForProject(ctx context.Context, projectID string) ([]db.ListLatestCompletedInteractionDAGDiagnosisTargetsForProjectRow, error)
}

// MessageStore is the DB seam for accessing task messages.
// *db.Queries satisfies it; tests inject a fake.
type MessageStore interface {
	MessagesForTaskInRange(ctx context.Context, arg db.MessagesForTaskInRangeParams) ([]db.TaskMessage, error)
	GetProjectInWorkspace(ctx context.Context, arg db.GetProjectInWorkspaceParams) (db.Project, error)
	GetIssueForTask(ctx context.Context, taskID string) (db.Issue, error)
}

// Compile-time guarantee that *db.Queries satisfies InteractionDAGStore, so a
// generated-method signature drift fails at compile time now, not at U7.2 wiring.
var _ InteractionDAGStore = (*db.Queries)(nil)

// Compile-time guarantee that *db.Queries satisfies MessageStore, so a
// generated-method signature drift fails at compile time now.
var _ MessageStore = (*db.Queries)(nil)

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
	msgs    MessageStore // optional; nil-safe, required only for RecordLocalSegmentForEvent
	client  ArealSegmentClient
	enabled bool
}

// NewInteractionDAGService constructs the recorder. When enabled is false the
// service is a no-op (RecordSessionAgentRun/AddEdge/CloseSegmentForEvent
// return nil without touching the DB or bridge).
//
// For local trajectory recording (RecordLocalSegmentForEvent), use
// NewInteractionDAGServiceWithMessages which also injects a MessageStore.
func NewInteractionDAGService(store InteractionDAGStore, client ArealSegmentClient, enabled bool) *InteractionDAGService {
	return &InteractionDAGService{store: store, client: client, enabled: enabled}
}

// NewInteractionDAGServiceWithMessages constructs the recorder with message
// access for local (non-AReaL) trajectory recording.
func NewInteractionDAGServiceWithMessages(store InteractionDAGStore, msgs MessageStore, client ArealSegmentClient, enabled bool) *InteractionDAGService {
	return &InteractionDAGService{store: store, msgs: msgs, client: client, enabled: enabled}
}

// Enabled reports whether segment-DAG recording is active.
func (s *InteractionDAGService) Enabled() bool { return s.enabled }

// RecordSessionAgentRun upserts the {session_id -> agent_run_id, issue_id}
// mapping (D8: agent_run_id = task.ID, the multica agent_inbox_event PK). Called
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

// LinkSessionTask links a training session to the real derived-agent task ID
// after normal task insertion, so DAG assembly maps the session to the actual
// agent run. Used by env-dispatch provisioning once the derived agent is ready
// and its real task has been enqueued. It is a thin wrapper over
// RecordSessionAgentRun matching the env-dispatch (sessionID, projectID,
// realTaskID, issueID) call order from the provisioning orchestrator.
func (s *InteractionDAGService) LinkSessionTask(ctx context.Context, sessionID, projectID, realTaskID, issueID string) error {
	return s.RecordSessionAgentRun(ctx, projectID, sessionID, realTaskID, issueID)
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
	agentRunUUID, err := util.ParseUUID(agentRunID)
	if err != nil {
		return "", fmt.Errorf("interaction_dag: parse agent_run_id: %w", err)
	}
	seg, err := s.store.GetInteractionDAGSegmentByAgentRun(ctx, agentRunUUID)
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
// Flow: (a) look up agent_run_id+issue_id by sessionID from
// interaction_dag_session_run; (b) arealrl.CloseSegment(proxyKey) ->
// trajectoryID; (c) arealrl.ExportTrajectory(sessionID, trajectoryID) -> raw
// traj JSON; (d) decode tensor_ref; (e) insert interaction_dag_segment +
// interaction_dag_env_snapshot. The raw trajectory export is returned
// alongside the segment id so the graph-memory ingest seam receives the real
// trajectory (review R1) instead of summarizing an empty one.
//
// adminKey is NOT a parameter: the arealrl.Client holds the admin key
// internally and ExportTrajectory uses it; proxyKey is the only per-call
// credential (session-key for CloseSegment). This drops the plan's redundant
// adminKey parameter - see U7.1 report.
func (s *InteractionDAGService) CloseSegmentForEvent(
	ctx context.Context,
	projectID, sessionID, proxyKey, closingEvent string,
	envSnapshot map[string]any,
) (string, json.RawMessage, error) {
	if !s.enabled {
		return "", nil, nil
	}
	if s.store == nil || s.client == nil {
		return "", nil, errors.New("interaction_dag: service not fully configured (store or client nil)")
	}
	if projectID == "" || sessionID == "" || proxyKey == "" {
		return "", nil, errors.New("interaction_dag: CloseSegmentForEvent requires project_id, session_id, proxy_key")
	}

	// (a) resolve agent_run_id + issue_id from the session mapping.
	run, err := s.store.GetInteractionDAGSessionRun(ctx, sessionID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: lookup session_run for %s: %w", sessionID, err)
	}

	// (b) close the segment -> trajectory_id.
	trajectoryID, err := s.client.CloseSegment(ctx, proxyKey)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: close_segment for session %s: %w", sessionID, err)
	}

	// (c) export the just-closed trajectory -> raw traj JSON.
	raw, err := s.client.ExportTrajectory(ctx, sessionID, trajectoryID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: export_trajectory %s/%d: %w", sessionID, trajectoryID, err)
	}

	// (d) decode tensor_ref from the export payload.
	tensorRef, err := decodeTensorRef(raw)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: decode tensor_ref for session %s: %w", sessionID, err)
	}

	// (e) record the segment + env snapshot atomically: a single data-modifying
	// CTE inserts both rows in one statement so a snapshot failure can never
	// orphan the segment (paired operations stay together). segment_id is known
	// ahead of time (<sessionID>-<trajectoryID>) and is reused as the snapshot FK.
	segmentID := fmt.Sprintf("%s-%d", sessionID, trajectoryID)
	sandboxIDs, issueSnapshotID, envState := encodeEnvSnapshot(envSnapshot)

	// Calculate the turn range for this segment.
	agentRunUUID, err := util.ParseUUID(run.AgentRunID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: parse agent_run_id for session %s: %w", sessionID, err)
	}
	lastEndSeq, err := s.store.GetLastEndSeqForAgentRun(ctx, agentRunUUID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: get last end_seq for %s: %w", run.AgentRunID, err)
	}
	startSeq := lastEndSeq + 1
	endSeq, err := s.store.GetMaxTaskMessageSeq(ctx, run.AgentRunID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: get max task_message seq for %s: %w", run.AgentRunID, err)
	}
	if endSeq < startSeq {
		startSeq, endSeq = 0, 0
	}

	insertedSegmentID, err := s.store.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID:        segmentID,
		ProjectID:        projectID,
		AgentRunID:       agentRunUUID,
		IssueID:          run.IssueID, // carry the looked-up issue_id (do not re-derive)
		TrajectoryID:     pgtype.Int8{Int64: int64(trajectoryID), Valid: true},
		TensorRef:        tensorRef,
		TrajectorySource: "areal_tensor",
		Trainable:        false,
		Trajectory:       []byte("[]"),
		ClosingEvent:     pgText(closingEvent),
		StartSeq:         startSeq,
		EndSeq:           endSeq,
		SandboxIds:       sandboxIDs,
		IssueSnapshotID:  issueSnapshotID,
		EnvState:         envState,
	})
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: insert segment+env_snapshot %s: %w", segmentID, err)
	}
	if insertedSegmentID != segmentID {
		return "", nil, errors.New("interaction_dag: inserted segment identity mismatch")
	}

	// The retained writer creates an untrusted legacy exception. Its exported
	// body is deliberately not returned to downstream readers or training.
	return segmentID, nil, nil
}

// AddEdge records a canonical workspace-scoped edge. Both endpoint segments
// must already exist in the workspace. Non-continuation edges resolve their
// durable trigger to the final persisted task message in the source Segment's
// frozen range; continuation edges have no trigger identity.
func (s *InteractionDAGService) AddEdge(ctx context.Context, workspaceID pgtype.UUID, srcSegmentID, dstSegmentID, edgeType string) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	if !validEdgeTypes[edgeType] {
		return fmt.Errorf("%w: %q (want continues|responds_to|delegates_to|mentions)", ErrInvalidEdgeType, edgeType)
	}
	if !workspaceID.Valid || srcSegmentID == "" || dstSegmentID == "" {
		return errors.New("interaction_dag: AddEdge requires workspace_id, src_segment_id, dst_segment_id")
	}

	_, err := s.store.InsertUniversalDAGEdgeAtomic(ctx, db.InsertUniversalDAGEdgeAtomicParams{
		WorkspaceID:  workspaceID,
		SrcSegmentID: srcSegmentID,
		DstSegmentID: dstSegmentID,
		EdgeType:     edgeType,
	})
	if err != nil {
		return fmt.Errorf("interaction_dag: insert canonical edge: %w", err)
	}
	return nil
}

// RecordLocalSegmentForEvent records a non-training segment for an env-dispatch
// task. It snapshots persisted task_message rows in the closing event's sequence
// range into a task_messages-source segment. No AReaL lifecycle calls are made.
//
// Session identity is deterministic: multica:<agentRunID>. The segment ID equals
// the session ID (one-segment-per-task, idempotent on repeated close). The
// recorded segment carries trajectory_source=task_messages, trainable=false, and
// null AReaL fields. The allowlisted trajectory snapshot is returned alongside
// the segment id so the graph-memory ingest seam receives it (review R1).
// Recording is best-effort: a failure returns an error but does
// not affect the task's terminal result.
func (s *InteractionDAGService) RecordLocalSegmentForEvent(
	ctx context.Context,
	projectID, agentRunID, issueID, closingEvent string,
	envSnapshot map[string]any,
) (string, json.RawMessage, error) {
	if !s.enabled {
		return "", nil, nil
	}
	if s.store == nil || s.msgs == nil {
		return "", nil, errors.New("interaction_dag: RecordLocalSegmentForEvent requires store and message store")
	}
	if projectID == "" || agentRunID == "" {
		return "", nil, errors.New("interaction_dag: RecordLocalSegmentForEvent requires project_id, agent_run_id")
	}

	sessionID := fmt.Sprintf("multica:%s", agentRunID)

	// Upsert the deterministic local session/run mapping (idempotent).
	if err := s.store.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
		SessionID:  sessionID,
		ProjectID:  projectID,
		AgentRunID: agentRunID,
		IssueID:    pgText(issueID),
	}); err != nil {
		return "", nil, fmt.Errorf("interaction_dag: upsert local session run %s: %w", sessionID, err)
	}

	// Compute the sequence range for this segment.
	agentRunUUID, err := util.ParseUUID(agentRunID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: parse agent_run_id: %w", err)
	}
	lastEndSeq, err := s.store.GetLastEndSeqForAgentRun(ctx, agentRunUUID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: get last end_seq for %s: %w", agentRunID, err)
	}
	startSeq := lastEndSeq + 1
	endSeq, err := s.store.GetMaxTaskMessageSeq(ctx, agentRunID)
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: get max task_message seq for %s: %w", agentRunID, err)
	}
	if endSeq < startSeq {
		startSeq, endSeq = 0, 0
	}

	// Read persisted task messages in the range and serialize the allowlisted
	// fields: sequence, type, tool, content, input, output. Provider API keys and
	// sandbox runtime configuration never enter this snapshot.
	msgs, err := s.msgs.MessagesForTaskInRange(ctx, db.MessagesForTaskInRangeParams{
		TaskID: agentRunID, StartSeq: startSeq, EndSeq: endSeq,
	})
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: read messages for %s [%d,%d]: %w", agentRunID, startSeq, endSeq, err)
	}
	trajectory := serializeLocalTrajectory(msgs)

	segmentID := sessionID
	sandboxIDs, issueSnapshotID, envState := encodeEnvSnapshot(envSnapshot)

	insertedSegmentID, err := s.store.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID:        segmentID,
		ProjectID:        projectID,
		AgentRunID:       agentRunUUID,
		IssueID:          pgText(issueID),
		TrajectoryID:     pgtype.Int8{Valid: false},
		TensorRef:        nil,
		ClosingEvent:     pgText(closingEvent),
		StartSeq:         startSeq,
		EndSeq:           endSeq,
		TrajectorySource: "task_messages",
		Trainable:        false,
		Trajectory:       trajectory,
		SandboxIds:       sandboxIDs,
		IssueSnapshotID:  issueSnapshotID,
		EnvState:         envState,
	})
	if err != nil {
		return "", nil, fmt.Errorf("interaction_dag: insert local segment+env_snapshot %s: %w", segmentID, err)
	}
	if insertedSegmentID != segmentID {
		return "", nil, errors.New("interaction_dag: inserted local segment identity mismatch")
	}

	// This compatibility row is legacy_unverified; do not surface its body.
	return segmentID, nil, nil
}

// localTrajectoryEntry is the allowlisted shape for each task_message entry in a
// local trajectory snapshot. Provider API keys and runtime secrets never appear here.
type localTrajectoryEntry struct {
	Seq     int32  `json:"sequence"`
	Type    string `json:"type"`
	Tool    string `json:"tool"`
	Content string `json:"content"`
	Input   string `json:"input"`
	Output  string `json:"output"`
}

// serializeLocalTrajectory converts persisted task_message rows into the
// allowlisted trajectory JSON array. Only sequence, type, tool, content, input,
// and output are included; provider keys and runtime configuration are excluded
// by construction (they are not task_message columns).
func serializeLocalTrajectory(msgs []db.TaskMessage) []byte {
	policy := DefaultSanitizerPolicy()
	entries := make([]localTrajectoryEntry, 0, len(msgs))
	for _, m := range msgs {
		// Compatibility rows pass through the same per-field gates as the
		// canonical pipeline — redaction, binary rejection, size cap — so no
		// unredacted pipeline payload is persisted anywhere (spec AC 8).
		sanitized, _ := sanitizeTaskMessageFields(m, policy)
		entries = append(entries, localTrajectoryEntry{
			Seq:     sanitized.Sequence,
			Type:    sanitized.Type,
			Tool:    sanitized.Tool,
			Content: sanitized.Content,
			Input:   sanitized.Input,
			Output:  sanitized.Output,
		})
	}
	if len(entries) == 0 {
		return []byte("[]")
	}
	raw, _ := json.Marshal(entries)
	if raw == nil {
		return []byte("[]")
	}
	return raw
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
	StepRewards       []StepReward       `json:"step_rewards"`
	// ScoreMax is the diagnosis agent's scoring scale (DIAGNOSIS_AGENT_SCORE_MAX,
	// default 10) - the inclusive upper bound on each StepReward.Score. It is
	// serving-boundary metadata, NOT assembled data: AssembleAssembledDag leaves
	// it 0 and the /dag handler stamps it from the diagnosis config so AReaL can
	// normalize per-turn scores to [0, 1] without guessing Multica's scale. 0
	// means diagnosis scoring was not configured; AReaL treats 0 as "no
	// normalization / sparse" (absence distinguishable, never a fabricated default).
	ScoreMax int `json:"score_max"`
}

// AssembledSegment mirrors areal's SegmentSpec: structure only - no judge
// scores, no turn indices, no message text. tensor_ref is the field->shard jsonb
// decoded at record time (Task 1) and passed through verbatim as
// json.RawMessage. closing_event is str|None (nil -> JSON null). env_snapshot
// is reconstructed from the three interaction_dag_env_snapshot columns. issue_id
// is a non-optional str on the areal side; a NULL DB issue_id is emitted as "".
type AssembledSegment struct {
	SegmentID         string               `json:"segment_id"`
	AgentRunID        string               `json:"agent_run_id"`
	IssueID           string               `json:"issue_id"`
	TrajectoryID      *int64               `json:"trajectory_id"`
	TensorRef         json.RawMessage      `json:"tensor_ref"`
	ClosingEvent      *string              `json:"closing_event"`
	TrajectorySource  string               `json:"trajectory_source"`
	Trainable         bool                 `json:"trainable"`
	Trajectory        json.RawMessage      `json:"trajectory"`
	EnvSnapshot       AssembledEnvSnapshot `json:"env_snapshot"`
	AssistantTurnSeqs []int32              `json:"assistant_turn_seqs"`
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

// AssembledEdge mirrors areal's EdgeSpec. Type uses the canonical Edge enum:
// continues, responds_to, delegates_to, or mentions.
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
// Legacy-only path: project-scoped dense session coverage and root-task
// readiness still apply here for historical /dag consumers. Mixed-RL freezes
// must use MixedRLFreezeService.GetFrozenRunDAG / ProviderCallLedger.GetFrozenDAG
// (run lifecycle + immutable snapshot) and must never enter this assembler.
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

	workspaceID, err := s.store.GetUniversalDAGProjectWorkspace(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: resolve project workspace: %w", err)
	}
	projectScope := db.ListInteractionDAGSegmentsForProjectParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
	}
	segs, err := s.store.ListInteractionDAGSegmentsForProject(ctx, projectScope)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list project segments: %w", err)
	}
	edges, err := s.store.ListInteractionDAGEdgesForProject(ctx, db.ListInteractionDAGEdgesForProjectParams(projectScope))
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list edges for %s: %w", projectID, err)
	}
	runs, err := s.store.ListInteractionDAGSessionRunsForProject(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list session_runs for %s: %w", projectID, err)
	}
	snaps, err := s.store.ListInteractionDAGEnvSnapshotsForProject(ctx, db.ListInteractionDAGEnvSnapshotsForProjectParams(projectScope))
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list env_snapshots for %s: %w", projectID, err)
	}
	stepRewards, err := s.store.ListInteractionDAGStepRewardsForProject(ctx, db.ListInteractionDAGStepRewardsForProjectParams(projectScope))
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list step_rewards for %s: %w", projectID, err)
	}
	targetRows, err := s.store.ListLatestCompletedInteractionDAGDiagnosisTargetsForProject(ctx, projectID)
	if err != nil {
		return AssembledDag{}, fmt.Errorf("interaction_dag: list diagnosis targets for %s: %w", projectID, err)
	}
	targetsBySegment := make(map[string][]int32, len(targetRows))
	for _, target := range targetRows {
		var seqs []int32
		if err := json.Unmarshal(target.ExpectedRewardSeqs, &seqs); err != nil {
			return AssembledDag{}, fmt.Errorf("interaction_dag: decode diagnosis targets for segment %s: %w", target.SegmentID, err)
		}
		targetsBySegment[target.SegmentID] = seqs
	}

	// Index env_snapshots by segment_id for an O(1) 1:1 join (the atomic CTE
	// in CloseSegmentForEvent guarantees every segment has exactly one snapshot;
	// the map makes assembly defensive if that invariant ever drifts).
	snapBySeg := make(map[string]db.InteractionDagEnvSnapshot, len(snaps))
	for _, sn := range snaps {
		snapBySeg[sn.SegmentID] = sn
	}

	out := AssembledDag{
		Segments:          make([]AssembledSegment, 0, len(segs)),
		Edges:             make([]AssembledEdge, 0, len(edges)),
		SessionToAgentRun: make(map[string]string, len(runs)),
		StepRewards:       make([]StepReward, 0, len(stepRewards)),
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
		trustedBody := sg.ContentStatus != "legacy_unverified"
		trajectoryID := (*int64)(nil)
		tensorRef := json.RawMessage(nil)
		trajectory := json.RawMessage("[]")
		if trustedBody {
			if sg.TrajectoryID > 0 {
				value := sg.TrajectoryID
				trajectoryID = &value
			}
			tensorRef = tensorRefOrEmpty(sg.TensorRef)
			trajectory = json.RawMessage(sg.Trajectory)
		}
		seg := AssembledSegment{
			SegmentID:        sg.SegmentID,
			AgentRunID:       util.UUIDToString(sg.AgentRunID),
			IssueID:          issueID,
			TrajectoryID:     trajectoryID,
			TensorRef:        tensorRef,
			ClosingEvent:     closingEvent,
			TrajectorySource: sg.TrajectorySource,
			Trainable:        trustedBody && sg.Trainable && sg.TrainableEligible,
			Trajectory:       trajectory,
			AssistantTurnSeqs: func() []int32 {
				if seqs, exists := targetsBySegment[sg.SegmentID]; exists {
					return seqs
				}
				return []int32{}
			}(),
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
	for _, sr := range stepRewards {
		out.StepRewards = append(out.StepRewards, StepReward{
			SegmentID: sr.SegmentID,
			Seq:       int(sr.Seq),
			Score:     int(sr.Score),
			Rationale: sr.Rationale,
		})
	}
	return out, nil
}

// RecordStepRewards upserts per-LLM-output (segment_id, seq) step rewards into
// interaction_dag_step_reward. It is the write path for the diagnosis agent's
// output. Re-recording a (segment_id, seq) key updates score/rationale (upsert),
// not duplicates. A disabled service (or nil store) is a no-op. Scores are
// already clamped to [0, scoreMax] by parseStepRewards; this method does not
// fabricate or zero-fill - absent rewards are simply not written.
func (s *InteractionDAGService) RecordStepRewards(ctx context.Context, projectID string, rewards []StepReward) error {
	if !s.enabled || s.store == nil {
		return nil
	}
	if projectID == "" {
		return errors.New("interaction_dag: RecordStepRewards requires project_id")
	}
	for _, r := range rewards {
		if err := s.store.InsertInteractionDAGStepReward(ctx, db.InsertInteractionDAGStepRewardParams{
			SegmentID: r.SegmentID,
			Seq:       int32(r.Seq),
			Score:     int32(r.Score),
			Rationale: r.Rationale,
		}); err != nil {
			return fmt.Errorf("interaction_dag: record step reward (%s,%d): %w", r.SegmentID, r.Seq, err)
		}
	}
	return nil
}

// assembleEnvSnapshot reconstructs the env_snapshot dict from the three
// interaction_dag_env_snapshot columns (sandbox_ids, issue_snapshot_id,
// env_state). It is the inverse of encodeEnvSnapshot: the jsonb columns are
// passed through verbatim as json.RawMessage (no re-decode/re-encode), and the
// nullable issue_snapshot_id becomes *string (nil -> JSON null).
func assembleEnvSnapshot(sn db.InteractionDagEnvSnapshot) AssembledEnvSnapshot {
	out := AssembledEnvSnapshot{
		SandboxIDs: json.RawMessage(sn.SandboxIds),
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

// int8Ptr converts a pgtype.Int8 to *int64, returning nil when the value is not
// present (NULL in the DB). Used to emit nullable trajectory_id in AssembledSegment.
func int8Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// tensorRefOrEmpty returns the tensor_ref bytes as json.RawMessage, falling back to
// json null when the ref is nil (task_messages segments). Absence stays distinguishable.
func tensorRefOrEmpty(b []byte) json.RawMessage {
	if b == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrGraphMemoryAgentRunUnavailable = errors.New("graph memory agent run unavailable")
	ErrGraphMemoryAgentRunFenced      = errors.New("graph memory agent run fenced")
	ErrGraphMemoryAgentQuotaExceeded  = errors.New("graph memory agent token quota exceeded")
	ErrGraphMemoryToolReplayConflict  = errors.New("graph memory tool idempotency conflict")
)

// GraphMemoryAgentRunClaim is the durable identity of one single-trajectory
// channel run. FencingToken must accompany every later mutation.
type GraphMemoryAgentRunClaim struct {
	RunID           string
	TrajectoryID    string
	FencingToken    int64
	GraphVersion    int64
	TargetSeq       int64
	InitialQuery    string
	Resumed         bool
	TokenBudgetLeft int64
}

// GraphMemoryAgentToolReservation is either a newly reserved operation or a
// replay of a terminal response. Pending=true means another worker owns it.
type GraphMemoryAgentToolReservation struct {
	OperationID string
	Replay      bool
	Pending     bool
	Response    json.RawMessage
	Error       string
}

type GraphMemoryAgentCitationInput struct {
	NodeID string
	// GraphIdentity distinguishes which physical graph the cited node lives
	// in ("<kind>:<owner>" route graphs, "research:<workspace>" research).
	GraphIdentity   string
	GraphVersion    int64
	Level           string
	EpistemicStatus string
	Tags            []string
	Title           string
	FirstParagraph  string
	Excerpt         string
	ContentHash     string
}

type GraphMemoryAgentRunStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
	// submittedRunSink is optional (nil = no-op): notified after a run
	// commits as "submitted" so the run becomes a channel-scoped
	// interaction_dag segment feeding graph-memory staging.
	submittedRunSink GraphMemorySubmittedRunSink
}

func NewGraphMemoryAgentRunStore(pool *pgxpool.Pool) *GraphMemoryAgentRunStore {
	return &GraphMemoryAgentRunStore{pool: pool, now: time.Now}
}

// SetSubmittedRunSink wires the submitted-run segment sink. Best-effort by
// contract: sink errors never affect the run's terminal result.
func (s *GraphMemoryAgentRunStore) SetSubmittedRunSink(sink GraphMemorySubmittedRunSink) {
	s.submittedRunSink = sink
}

type GraphMemoryAgentRunContext struct {
	Claim       GraphMemoryAgentRunClaim
	ConsumedSeq int64
}

func (s *GraphMemoryAgentRunStore) ActiveClaim(ctx context.Context, workspaceID, channelID string) (GraphMemoryAgentRunContext, error) {
	if s == nil || s.pool == nil {
		return GraphMemoryAgentRunContext{}, ErrGraphMemoryAgentRunUnavailable
	}
	var out GraphMemoryAgentRunContext
	err := s.pool.QueryRow(ctx, `
		SELECT run.id::text,trajectory.id::text,run.fencing_token,run.graph_version,run.target_seq,run.initial_query,state.consumed_seq
		FROM graph_memory_agent_state state
		JOIN graph_memory_channel_agent managed ON managed.channel_id=state.channel_id
		JOIN graph_memory_agent_run run ON run.id=state.active_run_id AND run.status='running'
		JOIN graph_memory_agent_trajectory trajectory ON trajectory.run_id=run.id AND trajectory.status='active'
		WHERE state.channel_id=$1::uuid AND managed.workspace_id=$2::uuid
		  AND managed.status='active' AND state.lease_expires_at>now()`, channelID, workspaceID).Scan(
		&out.Claim.RunID, &out.Claim.TrajectoryID, &out.Claim.FencingToken,
		&out.Claim.GraphVersion, &out.Claim.TargetSeq, &out.Claim.InitialQuery, &out.ConsumedSeq,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GraphMemoryAgentRunContext{}, ErrGraphMemoryAgentRunUnavailable
		}
		return GraphMemoryAgentRunContext{}, err
	}
	out.Claim.Resumed = true
	return out, nil
}

func (s *GraphMemoryAgentRunStore) LatestClaim(ctx context.Context, workspaceID, channelID string) (GraphMemoryAgentRunClaim, error) {
	if s == nil || s.pool == nil {
		return GraphMemoryAgentRunClaim{}, ErrGraphMemoryAgentRunUnavailable
	}
	var claim GraphMemoryAgentRunClaim
	err := s.pool.QueryRow(ctx, `
		SELECT run.id::text,trajectory.id::text,run.fencing_token,run.graph_version,run.target_seq,run.initial_query
		FROM graph_memory_agent_run run
		JOIN graph_memory_agent_trajectory trajectory ON trajectory.run_id=run.id
		WHERE run.workspace_id=$1::uuid AND run.channel_id=$2::uuid
		ORDER BY run.started_at DESC LIMIT 1`, workspaceID, channelID).Scan(
		&claim.RunID, &claim.TrajectoryID, &claim.FencingToken, &claim.GraphVersion, &claim.TargetSeq, &claim.InitialQuery,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GraphMemoryAgentRunClaim{}, ErrGraphMemoryAgentRunUnavailable
		}
		return GraphMemoryAgentRunClaim{}, err
	}
	return claim, nil
}

func (s *GraphMemoryAgentRunStore) Claim(ctx context.Context, workspaceID, channelID, targetKind, targetID, initialQuery string, graphVersion int64) (GraphMemoryAgentRunClaim, error) {
	if s == nil || s.pool == nil {
		return GraphMemoryAgentRunClaim{}, ErrGraphMemoryAgentRunUnavailable
	}
	if targetKind == "" {
		targetKind = "ambient"
	}
	if targetKind != "ambient" && targetKind != "channel" && targetKind != "thread" {
		return GraphMemoryAgentRunClaim{}, fmt.Errorf("invalid target kind %q", targetKind)
	}
	query := strings.TrimSpace(initialQuery)
	if query == "" || graphVersion < 0 {
		return GraphMemoryAgentRunClaim{}, errors.New("initial query and non-negative graph version are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GraphMemoryAgentRunClaim{}, err
	}
	defer tx.Rollback(ctx)

	var stateVersion int64
	var activeRunID pgtype.UUID
	var tokenLimit int64
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT s.state_version, s.active_run_id, p.memory_agent_max_tokens_per_hour,
		       COALESCE(a.status = 'active' AND s.lease_expires_at > $3, false)
		FROM graph_memory_agent_state s
		JOIN graph_memory_channel_agent a ON a.channel_id=s.channel_id
		JOIN graph_memory_profile p ON p.workspace_id=a.workspace_id
		WHERE s.channel_id=$1::uuid AND a.workspace_id=$2::uuid
		FOR UPDATE OF s`, channelID, workspaceID, s.now().UTC()).Scan(&stateVersion, &activeRunID, &tokenLimit, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GraphMemoryAgentRunClaim{}, ErrGraphMemoryAgentRunUnavailable
		}
		return GraphMemoryAgentRunClaim{}, err
	}
	if !active {
		return GraphMemoryAgentRunClaim{}, ErrGraphMemoryAgentRunUnavailable
	}

	var used int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(input_tokens + output_tokens),0)::bigint
		FROM graph_memory_agent_run
		WHERE workspace_id=$1::uuid AND started_at >= $2`, workspaceID, s.now().UTC().Add(-time.Hour)).Scan(&used); err != nil {
		return GraphMemoryAgentRunClaim{}, err
	}
	if used >= tokenLimit {
		return GraphMemoryAgentRunClaim{}, ErrGraphMemoryAgentQuotaExceeded
	}
	if activeRunID.Valid {
		var claim GraphMemoryAgentRunClaim
		if err := tx.QueryRow(ctx, `
			SELECT r.id::text, t.id::text, r.fencing_token, r.graph_version, r.target_seq, r.initial_query
			FROM graph_memory_agent_run r
			JOIN graph_memory_agent_trajectory t ON t.run_id=r.id
			WHERE r.id=$1 AND r.channel_id=$2::uuid AND r.status='running'`, activeRunID, channelID).Scan(
			&claim.RunID, &claim.TrajectoryID, &claim.FencingToken, &claim.GraphVersion, &claim.TargetSeq, &claim.InitialQuery,
		); err == nil {
			claim.Resumed = true
			claim.TokenBudgetLeft = tokenLimit - used
			if err := tx.Commit(ctx); err != nil {
				return GraphMemoryAgentRunClaim{}, err
			}
			return claim, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return GraphMemoryAgentRunClaim{}, err
		}
	}

	fencing := stateVersion + 1
	var runID, trajectoryID string
	var targetSeq int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO graph_memory_agent_run
		  (workspace_id,channel_id,target_kind,target_id,status,initial_query,effective_objective,graph_version,fencing_token,target_seq)
		VALUES ($1::uuid,$2::uuid,$3,NULLIF($4,'')::uuid,'running',$5,$5,$6,$7,
		        COALESCE((SELECT max(seq) FROM channel_message WHERE channel_id=$2::uuid),0))
		RETURNING id::text,target_seq`, workspaceID, channelID, targetKind, targetID, query, graphVersion, fencing).Scan(&runID, &targetSeq); err != nil {
		return GraphMemoryAgentRunClaim{}, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO graph_memory_agent_trajectory(run_id) VALUES($1::uuid) RETURNING id::text`, runID).Scan(&trajectoryID); err != nil {
		return GraphMemoryAgentRunClaim{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_agent_state SET active_run_id=$2::uuid,graph_version=$3,objective=$4,
		 state_version=$5,updated_at=now() WHERE channel_id=$1::uuid`, channelID, runID, graphVersion, query, fencing); err != nil {
		return GraphMemoryAgentRunClaim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphMemoryAgentRunClaim{}, err
	}
	return GraphMemoryAgentRunClaim{RunID: runID, TrajectoryID: trajectoryID, FencingToken: fencing, GraphVersion: graphVersion, TargetSeq: targetSeq, InitialQuery: query, TokenBudgetLeft: tokenLimit - used}, nil
}

// ReserveToolOperation reserves one idempotent Agent tool operation for a
// specific graph (graph_identity, unification spec §4.4): the same client
// key can reserve one operation per graph under the same trajectory.
func (s *GraphMemoryAgentRunStore) ReserveToolOperation(ctx context.Context, runID string, fencingToken int64, idempotencyKey, graphIdentity, operation string, request json.RawMessage) (GraphMemoryAgentToolReservation, error) {
	if s == nil || s.pool == nil {
		return GraphMemoryAgentToolReservation{}, ErrGraphMemoryAgentRunUnavailable
	}
	idempotencyKey, operation = strings.TrimSpace(idempotencyKey), strings.TrimSpace(operation)
	if idempotencyKey == "" || !json.Valid(request) {
		return GraphMemoryAgentToolReservation{}, errors.New("idempotency key and valid request are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GraphMemoryAgentToolReservation{}, err
	}
	defer tx.Rollback(ctx)
	var trajectoryID string
	if err := tx.QueryRow(ctx, `
		SELECT t.id::text FROM graph_memory_agent_run r
		JOIN graph_memory_agent_trajectory t ON t.run_id=r.id
		WHERE r.id=$1::uuid AND r.status='running' AND r.fencing_token=$2
		FOR UPDATE OF r`, runID, fencingToken).Scan(&trajectoryID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GraphMemoryAgentToolReservation{}, ErrGraphMemoryAgentRunFenced
		}
		return GraphMemoryAgentToolReservation{}, err
	}
	var existing GraphMemoryAgentToolReservation
	var existingOperation string
	var existingRequest json.RawMessage
	var status string
	err = tx.QueryRow(ctx, `
		SELECT id::text,operation,request,response,error,status
		FROM graph_memory_agent_tool_operation
		WHERE trajectory_id=$1::uuid AND graph_identity=$2 AND idempotency_key=$3`, trajectoryID, graphIdentity, idempotencyKey).Scan(
		&existing.OperationID, &existingOperation, &existingRequest, &existing.Response, &existing.Error, &status,
	)
	if err == nil {
		if existingOperation == operation && status == "failed" {
			// A terminally failed operation releases its idempotency key:
			// the retry legitimately fixes what the refusal complained about
			// (e.g. explores the nodes a citation-fenced submit cited), so its
			// request may differ. Flip the row back to pending and run the
			// attempt fresh instead of condemning the fixed key template the
			// runtime context teaches to an eternal OPERATION_PENDING replay.
			if _, err := tx.Exec(ctx, `
				UPDATE graph_memory_agent_tool_operation
				SET status='pending', response=NULL, error=NULL, completed_at=NULL
				WHERE id=$1::uuid`, existing.OperationID); err != nil {
				return GraphMemoryAgentToolReservation{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return GraphMemoryAgentToolReservation{}, err
			}
			return GraphMemoryAgentToolReservation{OperationID: existing.OperationID}, nil
		}
		if existingOperation != operation || !jsonEqual(existingRequest, request) {
			return GraphMemoryAgentToolReservation{}, ErrGraphMemoryToolReplayConflict
		}
		existing.Replay = status != "pending"
		existing.Pending = status == "pending"
		if err := tx.Commit(ctx); err != nil {
			return GraphMemoryAgentToolReservation{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GraphMemoryAgentToolReservation{}, err
	}
	var operationID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO graph_memory_agent_tool_operation
		 (trajectory_id,graph_identity,idempotency_key,operation,request,status)
		VALUES($1::uuid,$2,$3,$4,$5::jsonb,'pending') RETURNING id::text`, trajectoryID, graphIdentity, idempotencyKey, operation, request).Scan(&operationID); err != nil {
		return GraphMemoryAgentToolReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GraphMemoryAgentToolReservation{}, err
	}
	return GraphMemoryAgentToolReservation{OperationID: operationID}, nil
}

// ValidateToolOperationQuota enforces per-call, rolling node, and continuous
// turn limits before an idempotent operation is reserved. Node budgets are
// per graph (graph_identity): channel-route exploration never consumes the
// research graph's quota and vice versa (unification spec §4.4).
func (s *GraphMemoryAgentRunStore) ValidateToolOperationQuota(ctx context.Context, runID string, fencingToken int64, operation, idempotencyKey, graphIdentity string, request json.RawMessage) error {
	if s == nil || s.pool == nil || !json.Valid(request) {
		return ErrGraphMemoryAgentRunUnavailable
	}
	var nodeCount int64
	if operation == "explore" {
		var payload struct {
			NodeIDs []string `json:"node_ids"`
		}
		if err := json.Unmarshal(request, &payload); err != nil {
			return err
		}
		nodeCount = int64(len(payload.NodeIDs))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var maxPerCall, maxPerMinute, maxTurnSeconds, maxRounds int64
	var startedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT p.memory_agent_max_nodes_per_call,p.memory_agent_max_nodes_per_minute,
		       p.memory_agent_max_continuous_turn_seconds,p.explore_max_rounds,r.started_at
		FROM graph_memory_agent_run r
		JOIN graph_memory_profile p ON p.workspace_id=r.workspace_id
		WHERE r.id=$1::uuid AND r.fencing_token=$2 AND r.status='running'
		FOR UPDATE OF r`, runID, fencingToken).Scan(&maxPerCall, &maxPerMinute, &maxTurnSeconds, &maxRounds, &startedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGraphMemoryAgentRunFenced
		}
		return err
	}
	if maxTurnSeconds > 0 && !s.now().UTC().Before(startedAt.UTC().Add(time.Duration(maxTurnSeconds)*time.Second)) {
		return fmt.Errorf("%w: continuous turn duration exceeded", ErrGraphMemoryAgentQuotaExceeded)
	}
	if nodeCount > maxPerCall {
		return fmt.Errorf("%w: nodes per call exceeded", ErrGraphMemoryAgentQuotaExceeded)
	}
	if nodeCount > 0 {
		var usedMinute, usedTotal int64
		if err := tx.QueryRow(ctx, `
			SELECT
			  COALESCE(sum(jsonb_array_length(request->'node_ids')) FILTER (WHERE operation.created_at >= $2),0)::bigint,
			  COALESCE(sum(jsonb_array_length(request->'node_ids')),0)::bigint
			FROM graph_memory_agent_tool_operation operation
			JOIN graph_memory_agent_trajectory trajectory ON trajectory.id=operation.trajectory_id
			WHERE trajectory.run_id=$1::uuid AND operation.operation='explore'
			  AND operation.status<>'failed' AND operation.idempotency_key<>$3
			  AND operation.graph_identity=$4`,
			runID, s.now().UTC().Add(-time.Minute), strings.TrimSpace(idempotencyKey), graphIdentity).Scan(&usedMinute, &usedTotal); err != nil {
			return err
		}
		if usedMinute+nodeCount > maxPerMinute {
			return fmt.Errorf("%w: nodes per minute exceeded", ErrGraphMemoryAgentQuotaExceeded)
		}
		if maxRounds > 0 && usedTotal+nodeCount > maxRounds {
			return fmt.Errorf("%w: exploration round budget exceeded", ErrGraphMemoryAgentQuotaExceeded)
		}
	}
	return tx.Commit(ctx)
}

func (s *GraphMemoryAgentRunStore) CompleteToolOperation(ctx context.Context, runID string, fencingToken int64, operationID string, response json.RawMessage, operationError string) error {
	if s == nil || s.pool == nil || !json.Valid(response) {
		return ErrGraphMemoryAgentRunUnavailable
	}
	status := "completed"
	if strings.TrimSpace(operationError) != "" {
		status = "failed"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_agent_tool_operation o
		SET response=$4::jsonb,error=$5,status=$6,completed_at=now()
		FROM graph_memory_agent_trajectory t, graph_memory_agent_run r
		WHERE o.id=$1::uuid AND o.trajectory_id=t.id AND t.run_id=r.id
		  AND r.id=$2::uuid AND r.fencing_token=$3 AND r.status='running' AND o.status='pending'`,
		operationID, runID, fencingToken, response, operationError, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrGraphMemoryAgentRunFenced
	}
	return nil
}

func (s *GraphMemoryAgentRunStore) AddUsage(ctx context.Context, runID string, fencingToken, inputTokens, outputTokens int64) error {
	if inputTokens < 0 || outputTokens < 0 {
		return errors.New("token usage cannot be negative")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var workspaceID string
	var limit int64
	if err := tx.QueryRow(ctx, `
		SELECT r.workspace_id::text,p.memory_agent_max_tokens_per_hour
		FROM graph_memory_agent_run r JOIN graph_memory_profile p ON p.workspace_id=r.workspace_id
		WHERE r.id=$1::uuid AND r.fencing_token=$2 FOR UPDATE OF r`, runID, fencingToken).Scan(&workspaceID, &limit); err != nil {
		return ErrGraphMemoryAgentRunFenced
	}
	var used int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(input_tokens+output_tokens),0)::bigint FROM graph_memory_agent_run WHERE workspace_id=$1::uuid AND started_at >= $2`, workspaceID, s.now().UTC().Add(-time.Hour)).Scan(&used); err != nil {
		return err
	}
	exceeded := used+inputTokens+outputTokens > limit
	if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_run SET input_tokens=input_tokens+$2,output_tokens=output_tokens+$3 WHERE id=$1::uuid`, runID, inputTokens, outputTokens); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if exceeded {
		return ErrGraphMemoryAgentQuotaExceeded
	}
	return nil
}

func (s *GraphMemoryAgentRunStore) ExplorationRounds(ctx context.Context, runID string, fencingToken int64, graphIdentity string) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrGraphMemoryAgentRunUnavailable
	}
	var rounds int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(jsonb_array_length(operation.request->'node_ids')),0)::int
		FROM graph_memory_agent_run run
		JOIN graph_memory_agent_trajectory trajectory ON trajectory.run_id=run.id
		LEFT JOIN graph_memory_agent_tool_operation operation ON operation.trajectory_id=trajectory.id
		  AND operation.operation='explore' AND operation.status<>'failed'
		  AND operation.graph_identity=$3
		WHERE run.id=$1::uuid AND run.fencing_token=$2 AND run.status='running'
		GROUP BY run.id`, runID, fencingToken, graphIdentity).Scan(&rounds)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrGraphMemoryAgentRunFenced
		}
		return 0, err
	}
	return rounds, nil
}

// RecordViewedNodes durably extends the trajectory's provenance set. It is
// monotonic and idempotent; terminal trajectories and stale fences reject.
func (s *GraphMemoryAgentRunStore) RecordViewedNodes(ctx context.Context, runID string, fencingToken int64, nodeIDs []string) error {
	clean := make([]string, 0, len(nodeIDs))
	seen := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return errors.New("viewed node id is required")
		}
		if _, duplicate := seen[nodeID]; duplicate {
			continue
		}
		seen[nodeID] = struct{}{}
		clean = append(clean, nodeID)
	}
	if len(clean) == 0 {
		return nil
	}
	raw, _ := json.Marshal(clean)
	tag, err := s.pool.Exec(ctx, `
		UPDATE graph_memory_agent_trajectory t SET viewed_node_ids=(
		  SELECT COALESCE(jsonb_agg(value ORDER BY first_ordinal),'[]'::jsonb)
		  FROM (
		    SELECT value,min(ordinality) AS first_ordinal
		    FROM jsonb_array_elements_text(t.viewed_node_ids || $3::jsonb) WITH ORDINALITY item(value,ordinality)
		    GROUP BY value
		  ) dedup
		)
		FROM graph_memory_agent_run r
		WHERE t.run_id=r.id AND r.id=$1::uuid AND r.fencing_token=$2
		  AND r.status='running' AND t.status='active'`, runID, fencingToken, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrGraphMemoryAgentRunFenced
	}
	return nil
}

func (s *GraphMemoryAgentRunStore) Finish(ctx context.Context, runID string, fencingToken int64, terminalStatus string, consumedSeq int64, statePatch json.RawMessage, citations []GraphMemoryAgentCitationInput) error {
	return s.finish(ctx, runID, fencingToken, terminalStatus, consumedSeq, statePatch, citations, "", nil)
}

// FinishToolOperation atomically terminalizes both the trajectory and the
// operation that produced the terminal response.
func (s *GraphMemoryAgentRunStore) FinishToolOperation(ctx context.Context, runID string, fencingToken int64, operationID, terminalStatus string, consumedSeq int64, statePatch json.RawMessage, citations []GraphMemoryAgentCitationInput, response json.RawMessage) error {
	if strings.TrimSpace(operationID) == "" || !json.Valid(response) {
		return errors.New("terminal operation id and response are required")
	}
	return s.finish(ctx, runID, fencingToken, terminalStatus, consumedSeq, statePatch, citations, operationID, response)
}

func (s *GraphMemoryAgentRunStore) finish(ctx context.Context, runID string, fencingToken int64, terminalStatus string, consumedSeq int64, statePatch json.RawMessage, citations []GraphMemoryAgentCitationInput, operationID string, operationResponse json.RawMessage) error {
	if terminalStatus != "submitted" && terminalStatus != "checkpointed" && terminalStatus != "failed" && terminalStatus != "cancelled" {
		return fmt.Errorf("invalid terminal status %q", terminalStatus)
	}
	if consumedSeq < 0 || !json.Valid(statePatch) {
		return errors.New("non-negative cursor and valid state patch are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var trajectoryID, workspaceID, channelID string
	if err := tx.QueryRow(ctx, `
		SELECT t.id::text,r.workspace_id::text,r.channel_id::text
		FROM graph_memory_agent_run r JOIN graph_memory_agent_trajectory t ON t.run_id=r.id
		WHERE r.id=$1::uuid AND r.fencing_token=$2 AND r.status='running'
		FOR UPDATE OF r,t`, runID, fencingToken).Scan(&trajectoryID, &workspaceID, &channelID); err != nil {
		return ErrGraphMemoryAgentRunFenced
	}
	trajectoryStatus := terminalStatus
	if terminalStatus == "cancelled" {
		trajectoryStatus = "failed"
	}
	if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_trajectory SET status=$2,state_patch=$3::jsonb,finished_at=now() WHERE id=$1::uuid`, trajectoryID, trajectoryStatus, statePatch); err != nil {
		return err
	}
	for _, citation := range citations {
		if strings.TrimSpace(citation.NodeID) == "" || strings.TrimSpace(citation.ContentHash) == "" {
			return errors.New("citation node id and content hash are required")
		}
		// Viewed provenance is graph-qualified: a node id viewed on one graph
		// is not evidence for a citation on another graph.
		viewedKey := citation.GraphIdentity + "|" + citation.NodeID
		tags, _ := json.Marshal(citation.Tags)
		tag, err := tx.Exec(ctx, `
			INSERT INTO graph_memory_agent_citation
			 (workspace_id,channel_id,trajectory_id,node_id,graph_identity,graph_version,level,epistemic_status,tags,title,first_paragraph,excerpt,content_hash)
			SELECT $1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13
			WHERE EXISTS (
			  SELECT 1 FROM graph_memory_agent_trajectory
			  WHERE id=$3::uuid AND viewed_node_ids ? $14
			)
			ON CONFLICT (trajectory_id,graph_identity,node_id) DO NOTHING`, workspaceID, channelID, trajectoryID,
			citation.NodeID, citation.GraphIdentity, citation.GraphVersion, citation.Level, citation.EpistemicStatus, tags,
			citation.Title, citation.FirstParagraph, truncateUTF8(citation.Excerpt, 2000), citation.ContentHash, viewedKey)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("citation node %q was not viewed by the submitted trajectory", citation.NodeID)
		}
	}
	if operationID != "" {
		tag, err := tx.Exec(ctx, `
			UPDATE graph_memory_agent_tool_operation
			SET response=$2::jsonb,error='',status='completed',completed_at=now()
			WHERE id=$1::uuid AND trajectory_id=$3::uuid AND status='pending'`, operationID, operationResponse, trajectoryID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrGraphMemoryAgentRunFenced
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_run SET status=$2,finished_at=now() WHERE id=$1::uuid`, runID, terminalStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_agent_state SET
		 consumed_seq=GREATEST(consumed_seq,$3),active_run_id=NULL,state_version=state_version+1,
		 objective=COALESCE(NULLIF($4::jsonb->>'objective',''),objective),
		 observations=COALESCE($4::jsonb->'observations',observations),
		 rejected_branches=COALESCE($4::jsonb->'rejected_branches',rejected_branches),
		 open_questions=COALESCE($4::jsonb->'open_questions',open_questions),
		 candidate_node_ids=COALESCE($4::jsonb->'candidate_node_ids',candidate_node_ids),
		 viewed_node_ids=COALESCE($4::jsonb->'viewed_node_ids',viewed_node_ids),
		 pending_targets=COALESCE($4::jsonb->'pending_targets',pending_targets),
		 next_hint=COALESCE($4::jsonb->>'next_hint',next_hint),updated_at=now()
		WHERE channel_id=$2::uuid AND active_run_id=$1::uuid`, runID, channelID, consumedSeq, statePatch); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Submitted runs are the conversational turn of an agent-mode channel:
	// notify the segment sink (best-effort, detached) so the turn reaches
	// graph-memory staging. checkpointed/failed/cancelled runs record nothing.
	if terminalStatus == "submitted" && s.submittedRunSink != nil {
		sink := s.submittedRunSink
		detached := context.WithoutCancel(ctx)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("graph memory agent run segment sink panicked", "run_id", runID, "panic", r)
				}
			}()
			sink.RecordSubmittedRun(detached, runID, workspaceID, channelID, consumedSeq)
		}()
	}
	return nil
}

func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
	}
	aa, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return bytes.Equal(aa, bb)
}

func truncateUTF8(value string, max int) string {
	if len(value) <= max {
		return value
	}
	for max > 0 && (value[max]&0xc0) == 0x80 {
		max--
	}
	return value[:max]
}

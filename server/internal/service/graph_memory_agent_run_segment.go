// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GraphMemorySubmittedRunSink is notified after a graph_memory_agent_run
// commits with status "submitted". Agent-mode channel turns never create an
// agent_inbox_event (the conversational worker is the managed Memory Agent),
// so the task-close seams cannot see them; this sink is the seam that turns a
// submitted run into a channel-scoped interaction_dag segment feeding
// graph-memory staging. Best-effort by contract: sink errors never affect the
// run's terminal result.
type GraphMemorySubmittedRunSink interface {
	RecordSubmittedRun(ctx context.Context, runID, workspaceID, channelID string, consumedSeq int64)
}

// GraphMemoryRunSegmentRecorder is the production sink. It is self-contained
// on the pool: dedup, run/message reads, the segment insert, and the ingest
// hook all resolve through it, so wiring needs no new dependencies.
type GraphMemoryRunSegmentRecorder struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	dag     *InteractionDAGService

	hookOnce sync.Once
	hook     *GraphMemoryIngestHook

	// ingestOverride is test-only: replaces IngestChannelRun with a stub.
	ingestOverride func(ctx context.Context, workspaceID, channelID, agentID, runID string, seg memorygraph.SegmentExport) error
}

// NewGraphMemoryRunSegmentRecorder builds the sink. dag may be nil (recording
// disabled); the ingest hook is constructed lazily from the pool.
func NewGraphMemoryRunSegmentRecorder(pool *pgxpool.Pool) *GraphMemoryRunSegmentRecorder {
	queries := db.New(pool)
	return &GraphMemoryRunSegmentRecorder{
		pool:    pool,
		queries: queries,
		dag:     NewInteractionDAGService(queries, nil, interactionDAGEnabledDefault()),
	}
}

// interactionDAGEnabledDefault mirrors the TrainingConfig gate: segment-DAG
// recording is on unless INTERACTION_DAG_ENABLED is explicitly false.
func interactionDAGEnabledDefault() bool {
	if raw := os.Getenv(interactionDAGEnabledEnv); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return true
		}
		return v
	}
	return true
}

// RecordSubmittedRun records the submitted run as a channel-scoped segment and
// fires the graph-memory ingest. Every step is best-effort: failures are
// logged and swallowed so a staging hiccup never surfaces to the agent turn.
func (r *GraphMemoryRunSegmentRecorder) RecordSubmittedRun(ctx context.Context, runID, workspaceID, channelID string, consumedSeq int64) {
	if r == nil || r.pool == nil || r.dag == nil || !r.dag.Enabled() {
		return
	}
	if runID == "" || workspaceID == "" || channelID == "" {
		return
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		slog.Warn("graph memory run segment: invalid workspace id", "workspace_id", workspaceID, "err", err)
		return
	}
	channelUUID, err := util.ParseUUID(channelID)
	if err != nil {
		slog.Warn("graph memory run segment: invalid channel id", "channel_id", channelID, "err", err)
		return
	}
	runUUID, err := util.ParseUUID(runID)
	if err != nil {
		slog.Warn("graph memory run segment: invalid run id", "run_id", runID, "err", err)
		return
	}
	if rt := resolveGraphMemoryType(ctx, r.queries, wsUUID, graphMemoryEnvMemoryType()); rt != "graph" {
		return
	}

	// One segment per run: the deterministic segment id makes a duplicate
	// notification a no-op.
	if existing, err := r.dag.SegmentIDForAgentRun(ctx, runID); err == nil && existing != "" {
		return
	}

	// The run's conversational evidence: the triggering channel message (the
	// user's learn instruction), falling back to the agent's own start query.
	runQuery, targetSeq, err := r.loadRun(ctx, runID)
	if err != nil {
		slog.Warn("graph memory run segment: load run failed", "run_id", runID, "err", err)
		return
	}
	content := r.loadChannelMessage(ctx, channelID, targetSeq)
	if strings.TrimSpace(content) == "" {
		content = runQuery
	}
	if strings.TrimSpace(content) == "" {
		slog.Warn("graph memory run segment: no trajectory content", "run_id", runID)
		return
	}

	trajectory := channelRunTrajectory(content)

	// Canonical representation (Task 3B, D1 plan amendment): materialize the
	// run as a task-owned universal DAG segment instead of a legacy-shaped
	// row, so the canonical 454 schema accepts the write.
	segmentID, err := r.recordCanonicalRunSegment(ctx, wsUUID, channelUUID, runUUID, content, targetSeq, consumedSeq)
	if err != nil {
		slog.Warn("graph memory run segment: canonical record failed", "run_id", runID, "err", err)
		return
	}

	// ClosingEvent becomes the open daily node's body line (spec §6), and
	// that body is the only BM25-retrievable text for this run until
	// consolidation folds the staging segment into statement nodes: an empty
	// close line leaves the learned fact unrecallable.
	seg := memorygraph.SegmentExport{
		SegmentID:    segmentID,
		AgentRunID:   runID,
		Trajectory:   trajectory,
		ClosingEvent: truncateForSummary(content, triggerSummaryMaxLen),
	}
	if err := r.ingestChannelRun(ctx, workspaceID, channelID, runID, seg); err != nil {
		slog.Warn("graph memory run segment: ingest failed", "segment_id", seg.SegmentID, "err", err)
	}
}

// loadRun reads the run's start query and target channel message seq.
func (r *GraphMemoryRunSegmentRecorder) loadRun(ctx context.Context, runID string) (string, int64, error) {
	var initialQuery string
	var targetSeq int64
	err := r.pool.QueryRow(ctx, `
		SELECT initial_query, target_seq FROM graph_memory_agent_run WHERE id=$1::uuid`, runID).Scan(&initialQuery, &targetSeq)
	return initialQuery, targetSeq, err
}

// loadChannelMessage fetches the triggering message's text content; "" on any
// miss (deleted, non-content kind, seq unknown).
func (r *GraphMemoryRunSegmentRecorder) loadChannelMessage(ctx context.Context, channelID string, seq int64) string {
	if seq <= 0 {
		return ""
	}
	var content string
	err := r.pool.QueryRow(ctx, `
		SELECT content FROM channel_message
		WHERE channel_id=$1::uuid AND seq=$2 AND kind='content' AND deleted_at IS NULL
		LIMIT 1`, channelID, seq).Scan(&content)
	if err != nil {
		return ""
	}
	return content
}

// ingestChannelRun fires the hook's channel-run entry, constructing the hook
// lazily from the pool (production) or using the test override.
func (r *GraphMemoryRunSegmentRecorder) ingestChannelRun(ctx context.Context, workspaceID, channelID, runID string, seg memorygraph.SegmentExport) error {
	if r.ingestOverride != nil {
		return r.ingestOverride(ctx, workspaceID, channelID, "", runID, seg)
	}
	hook := r.ingestHook()
	if hook == nil {
		return nil
	}
	agentID := r.managedAgentID(ctx, workspaceID, channelID)
	return hook.IngestChannelRun(ctx, workspaceID, channelID, agentID, runID, seg)
}

func (r *GraphMemoryRunSegmentRecorder) ingestHook() *GraphMemoryIngestHook {
	r.hookOnce.Do(func() {
		r.hook = NewGraphMemoryIngestHook(db.New(r.pool), r.pool, "", nil)
	})
	return r.hook
}

// managedAgentID resolves the channel's active managed Memory Agent for
// segment scope metadata; "" on any miss (metadata only).
func (r *GraphMemoryRunSegmentRecorder) managedAgentID(ctx context.Context, workspaceID, channelID string) string {
	var agentID string
	err := r.pool.QueryRow(ctx, `
		SELECT agent_id::text FROM graph_memory_channel_agent
		WHERE workspace_id=$1::uuid AND channel_id=$2::uuid AND status='active'
		LIMIT 1`, workspaceID, channelID).Scan(&agentID)
	if err != nil {
		return ""
	}
	return agentID
}

// channelRunTrajectory serializes the run's evidence into the same allowlisted
// entry shape the local task_messages snapshot uses, so the ingester's
// summarizer treats both identically.
func channelRunTrajectory(content string) json.RawMessage {
	entries := []localTrajectoryEntry{{
		Seq:     1,
		Type:    "user",
		Content: content,
	}}
	data, err := json.Marshal(entries)
	if err != nil {
		return json.RawMessage("[]")
	}
	return data
}

// recordCanonicalRunSegment materializes a submitted memory-agent run as a
// task-owned canonical segment (Task 3B): inside one transaction it creates
// the synthetic task (agent_inbox_event, id = run id, terminal_outcome
// completed), persists the run's channel evidence as task_message seq 1,
// then opens segment [1,1] with a canonical inbound boundary and seals it
// with a terminal close. The canonical segment id is returned. Every write
// satisfies the 454 validation chain: task/channel ownership, exact range
// coverage, and the task_messages-only trajectory_source.
func (r *GraphMemoryRunSegmentRecorder) recordCanonicalRunSegment(
	ctx context.Context,
	workspaceID, channelID, runID pgtype.UUID,
	content string,
	targetSeq, consumedSeq int64,
) (string, error) {
	runIDText := util.UUIDToString(runID)
	agentID := r.managedAgentID(ctx, util.UUIDToString(workspaceID), util.UUIDToString(channelID))
	if agentID == "" {
		return "", fmt.Errorf("graph memory run segment: no active managed memory agent for channel (run %s)", runIDText)
	}
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return "", fmt.Errorf("graph memory run segment: parse managed agent id: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	qtx := r.queries.WithTx(tx)

	// Synthetic task marker: provenance only, no payload beyond ids and seqs.
	marker, err := json.Marshal(map[string]any{
		"kind":         "memory_agent_run",
		"run_id":       runIDText,
		"target_seq":   targetSeq,
		"consumed_seq": consumedSeq,
	})
	if err != nil {
		return "", err
	}
	seqFrom := targetSeq
	if seqFrom < 0 {
		seqFrom = 0
	}
	seqTo := consumedSeq
	if seqTo < seqFrom {
		seqTo = seqFrom
	}
	// Idempotent on the task id: a retried notification re-creates nothing; the
	// recorder-level segment dedup normally returns before reaching here.
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_inbox_event (
			id, workspace_id, agent_id, channel_id, reason, status,
			seq_from, seq_to, context, started_at, completed_at, terminal_at,
			terminal_outcome
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::uuid, 'channel_message', 'acked',
			$5::bigint, $6::bigint, $7::jsonb, now(), now(), now(), 'completed'
		)
		ON CONFLICT (id) DO NOTHING`,
		runID, workspaceID, agentUUID, channelID, seqFrom, seqTo, marker); err != nil {
		return "", fmt.Errorf("graph memory run segment: insert synthetic task: %w", err)
	}
	task, err := qtx.GetAgentInboxEvent(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("graph memory run segment: load synthetic task: %w", err)
	}

	// The evidence lives in task_messages so the future publish pipeline can
	// compute the redacted content; the legacy segment body is never written.
	if _, err := qtx.CreateTaskMessage(ctx, db.CreateTaskMessageParams{
		TaskID:     runID,
		Seq:        1,
		Type:       "user",
		Content:    pgText(content),
		Visibility: "user_facing",
	}); err != nil {
		return "", fmt.Errorf("graph memory run segment: insert evidence task_message: %w", err)
	}

	dag := NewUniversalInteractionDAG()
	base := DAGBoundaryInput{
		WorkspaceID:       workspaceID,
		Task:              task,
		MemoryTypeAtEvent: "graph",
		ChannelID:         channelID,
	}
	openInput := base
	openInput.BoundaryKind = DAGBoundaryInbound
	openInput.EndSeq = 1
	open, err := dag.RecordBoundaryTx(ctx, qtx, tx, openInput)
	if err != nil {
		return "", fmt.Errorf("graph memory run segment: inbound boundary: %w", err)
	}
	if open.Closed {
		return "", fmt.Errorf("graph memory run segment: inbound boundary closed a segment (run %s)", runIDText)
	}
	closeInput := base
	closeInput.BoundaryKind = DAGBoundaryTerminal
	closeInput.CloseActionKind = DAGCloseTerminal
	sealed, err := dag.RecordBoundaryTx(ctx, qtx, tx, closeInput)
	if err != nil {
		return "", fmt.Errorf("graph memory run segment: terminal close: %w", err)
	}
	if !sealed.Closed {
		return "", fmt.Errorf("graph memory run segment: terminal close did not seal the segment (run %s)", runIDText)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return sealed.SegmentID, nil
}

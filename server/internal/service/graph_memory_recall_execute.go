// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

const (
	graphMemoryRecallMaxSummaryChars  = 4000
	graphMemoryRecallMaxCitationCount = 16
	graphMemoryRecallTruncationMarker = "…[truncated]"
)

// GraphMemoryRecallBackendFactory constructs only the already-resolved backend.
type GraphMemoryRecallBackendFactory func(ctx context.Context, policy ResolvedMemoryProvider) (memorygraph.AgentBackend, error)

// GraphMemoryRecallInjection is the bounded result returned to a daemon. Its
// content is rendered server-side so clients never run Explore locally.
type GraphMemoryRecallInjection struct {
	Found     bool                   `json:"found"`
	Summary   string                 `json:"summary"`
	Citations []memorygraph.Citation `json:"citations"`
	Content   string                 `json:"content"`
	Rounds    int                    `json:"rounds"`
	Version   int                    `json:"version"`
}

// GraphMemoryRecallExecutor owns synchronous Explore execution after Begin has
// durably created the recall ledger rows.
type GraphMemoryRecallExecutor struct {
	pool         *pgxpool.Pool
	dive         *GraphMemoryDiveService
	policy       *MemoryProviderPolicyResolver
	backendFor   GraphMemoryRecallBackendFactory
	embedder     *memorygraph.CachedEmbedder
	traces       *memorygraph.TraceRecorder
	priorFlights singleflight.Group
}

func NewGraphMemoryRecallExecutor(pool *pgxpool.Pool, dive *GraphMemoryDiveService, policy *MemoryProviderPolicyResolver, backendFor GraphMemoryRecallBackendFactory, embedder *memorygraph.CachedEmbedder, traces *memorygraph.TraceRecorder) *GraphMemoryRecallExecutor {
	return &GraphMemoryRecallExecutor{
		pool: pool, dive: dive, policy: policy, backendFor: backendFor, embedder: embedder, traces: traces,
	}
}

// Execute completes each persisted trajectory, queues Dive after the terminal
// barrier, and returns only the bounded adopted result. Provider and graph
// execution failures are ledger data; only database failures are returned.
func (e *GraphMemoryRecallExecutor) Execute(ctx context.Context, plan *GraphMemoryRecallPlan) (*GraphMemoryRecallInjection, error) {
	if e == nil || e.pool == nil {
		return nil, fmt.Errorf("graph memory recall executor is not configured")
	}
	workspaceUUID, err := util.ParseUUID(plan.WorkspaceID)
	if err != nil {
		return e.executionFailure(ctx, plan, "provider policy: workspace id: "+err.Error(), "")
	}
	resolved, err := e.policy.Resolve(ctx, workspaceUUID, ProviderDive)
	if err != nil {
		return e.executionFailure(ctx, plan, "provider policy: "+err.Error(), "")
	}

	store := memorygraph.NewStore(plan.GraphDir)
	if _, err := store.OpenSnapshot(plan.GraphVersion); err != nil {
		return e.executionFailure(ctx, plan, "pinned graph unavailable: "+err.Error(), resolved.Model)
	}

	retrievalCfg := memorygraph.DefaultRetrievalConfig()
	retrievalCfg.View = plan.GraphView
	retr := memorygraph.NewHybridRetriever(store, e.embedder, retrievalCfg)
	if err := retr.RebuildForVersion(ctx, plan.GraphVersion); err != nil {
		return e.executionFailure(ctx, plan, "pinned retriever unavailable: "+err.Error(), resolved.Model)
	}
	if e.backendFor == nil {
		return e.executionFailure(ctx, plan, "agent backend is not configured", resolved.Model)
	}
	backend, err := e.backendFor(ctx, resolved)
	if err != nil {
		return e.executionFailure(ctx, plan, "agent backend: "+err.Error(), resolved.Model)
	}

	cfg := memorygraph.DefaultExploreConfig()
	cfg.Agents = plan.K
	if plan.Tunables.ExploreMaxRounds > 0 {
		cfg.MaxRounds = plan.Tunables.ExploreMaxRounds
	}
	if plan.Tunables.ExploreNodesPerExpansion > 0 {
		cfg.ViewsPerExpansion = plan.Tunables.ExploreNodesPerExpansion
	}
	cfg.Model = resolved.Model
	explorer := memorygraph.NewExplorer(store, retr, backend, cfg, resolved.Provider, e.traces)
	explorer.PinVersion(plan.GraphVersion)
	priorStore := memorygraph.NewPriorRecordStore(filepath.Join(plan.GraphDir, "continuation"))
	ownerKey := graphPriorOwnerKey(plan)
	var brief *memorygraph.PriorBrief
	if rec, err := priorStore.Load(ownerKey); err != nil {
		slog.Warn("graph memory recall: prior record load failed; continuing without prior", "recall_id", plan.RecallID, "error", err)
	} else if rec != nil && rec.GraphVersion == plan.GraphVersion {
		brief = e.priorBrief(ctx, plan, rec, priorStore, ownerKey, backend, resolved.Model)
	}
	result, err := explorer.ExploreWithPrior(ctx, plan.Query, plan.Seeds, brief)
	if err != nil {
		return e.executionFailure(ctx, plan, "explore: "+err.Error(), resolved.Model)
	}
	if err := memorygraph.NewQueryRecorder(store, "daemon").RecordRecall(memorygraph.QueryLogEntry{
		TraceID:   plan.TraceID,
		Query:     plan.Query,
		Timestamp: time.Now().UTC(),
		Version:   result.Version,
		NodeIDs:   result.NodeIDs,
		Rounds:    result.Rounds,
		AgentRuns: len(result.AgentRuns),
		Found:     result.Found,
		PriorUsed: brief != nil,
	}); err != nil {
		slog.Warn("graph memory recall: query log append failed", "trace_id", plan.TraceID, "error", err)
	}
	if err := e.persistRuns(ctx, plan.RecallID, result.AgentRuns, resolved.Model); err != nil {
		return nil, err
	}
	if err := e.enqueueDive(ctx, plan.RecallID); err != nil {
		return nil, err
	}
	if result.Found {
		if err := priorStore.Save(ownerKey, memorygraph.PriorRecord{
			GraphVersion: plan.GraphVersion,
			Query:        plan.Query,
			CreatedAt:    time.Now().UTC(),
			Transcript:   result.AdoptedTranscript,
		}); err != nil {
			slog.Warn("graph memory recall: prior record save failed", "recall_id", plan.RecallID, "error", err)
		}
	}
	if !result.Found {
		return &GraphMemoryRecallInjection{Version: plan.GraphVersion, Rounds: result.Rounds}, nil
	}
	return graphMemoryRecallInjection(store, plan.GraphVersion, result.Summary, result.NodeIDs, result.Rounds), nil
}

// graphPriorOwnerKey is the per-channel continuation key: workspace +
// graph identity + view channel. Cross-workspace or cross-channel reuse is
// impossible by construction.
func graphPriorOwnerKey(plan *GraphMemoryRecallPlan) string {
	return strings.Join([]string{
		plan.WorkspaceID, plan.GraphKind, plan.GraphOwnerID, plan.GraphView.ChannelID,
	}, "|")
}

// priorBrief resolves the query-aware brief for this recall: exact-match
// cache on the normalized query, then a singleflight-compressed miss. Every
// failure degrades to nil so recall continues without prior evidence.
func (e *GraphMemoryRecallExecutor) priorBrief(ctx context.Context, plan *GraphMemoryRecallPlan, rec *memorygraph.PriorRecord, store *memorygraph.PriorRecordStore, ownerKey string, backend memorygraph.AgentBackend, model string) *memorygraph.PriorBrief {
	key := memorygraph.NormalizeRecallKey(plan.Query)
	if key == "" {
		return nil
	}
	if cached, ok := rec.Briefs[key]; ok {
		slog.Info("graph memory recall: prior brief cache hit", "recall_id", plan.RecallID)
		return &cached
	}
	flightKey := ownerKey + "|" + strconv.Itoa(rec.GraphVersion) + "|" + key
	started := time.Now()
	v, err, _ := e.priorFlights.Do(flightKey, func() (any, error) {
		return memorygraph.NewPriorCompressor(backend, model, memorygraph.DefaultPriorCompressionTimeout).
			Compress(ctx, plan.Query, rec.Transcript)
	})
	if err != nil {
		slog.Warn("graph memory recall: prior compression failed; continuing without prior", "recall_id", plan.RecallID, "error", err)
		return nil
	}
	brief, _ := v.(*memorygraph.PriorBrief)
	if brief == nil {
		return nil
	}
	slog.Info("graph memory recall: prior brief compressed",
		"recall_id", plan.RecallID, "ms", time.Since(started).Milliseconds(),
		"transcript_msgs", len(rec.Transcript), "brief_nodes", len(brief.NodeIDs))
	if rec.Briefs == nil {
		rec.Briefs = map[string]memorygraph.PriorBrief{}
	}
	rec.Briefs[key] = *brief
	if err := store.Save(ownerKey, *rec); err != nil {
		slog.Warn("graph memory recall: prior brief write-back failed", "recall_id", plan.RecallID, "error", err)
	}
	return brief
}

func (e *GraphMemoryRecallExecutor) executionFailure(ctx context.Context, plan *GraphMemoryRecallPlan, reason, model string) (*GraphMemoryRecallInjection, error) {
	if err := e.persistFailure(ctx, plan.RecallID, reason, model); err != nil {
		return nil, err
	}
	if err := e.enqueueDive(ctx, plan.RecallID); err != nil {
		return nil, err
	}
	return nil, nil
}

func (e *GraphMemoryRecallExecutor) enqueueDive(ctx context.Context, recallID string) error {
	if e.dive == nil {
		return fmt.Errorf("graph memory dive service is not configured")
	}
	_, err := e.dive.EnqueueIfBarrierMet(ctx, recallID)
	return err
}

func (e *GraphMemoryRecallExecutor) persistFailure(ctx context.Context, recallID, reason, model string) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_trajectory
		SET status = 'error', error_kind = 'error', summary = '', viewed_node_ids = '[]'::jsonb,
			submitted_node_ids = '[]'::jsonb, rounds = 0, model = $2, terminal_at = now(), updated_at = now()
		WHERE recall_id = $1
	`, recallID, model); err != nil {
		return fmt.Errorf("graph memory recall: mark trajectories failed (%s): %w", reason, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'explore_terminal', updated_at = now()
		WHERE id = $1
	`, recallID); err != nil {
		return fmt.Errorf("graph memory recall: mark recall terminal: %w", err)
	}
	return tx.Commit(ctx)
}

func (e *GraphMemoryRecallExecutor) persistRuns(ctx context.Context, recallID string, runs []memorygraph.ExploreRun, model string) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, run := range runs {
		viewed, err := json.Marshal(run.ViewedNodeIDs)
		if err != nil {
			return err
		}
		submitted, err := json.Marshal(run.NodeIDs)
		if err != nil {
			return err
		}
		status := graphMemoryTrajectoryStatus(run)
		// error_kind stays empty for non-error outcomes so downstream error
		// breakdowns only see genuine failures.
		errorKind := ""
		switch status {
		case "error", "timeout", "budget":
			errorKind = status
		}
		if _, err := tx.Exec(ctx, `
			UPDATE graph_memory_trajectory
			SET status = $3, error_kind = $4, summary = $5, viewed_node_ids = $6,
				submitted_node_ids = $7, rounds = $8, model = $9, terminal_at = now(), updated_at = now()
			WHERE recall_id = $1 AND seed_index = $2
		`, recallID, run.Seed, status, errorKind, run.Summary, viewed, submitted, run.Rounds, model); err != nil {
			return fmt.Errorf("graph memory recall: persist trajectory %d: %w", run.Seed, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'explore_terminal', updated_at = now()
		WHERE id = $1
	`, recallID); err != nil {
		return fmt.Errorf("graph memory recall: mark recall terminal: %w", err)
	}
	return tx.Commit(ctx)
}

// graphMemoryTrajectoryStatus maps explicit Explorer failures to error. Error
// text containing timeout/deadline maps to timeout and text containing budget
// maps to budget; all other failed runs map to error.
func graphMemoryTrajectoryStatus(run memorygraph.ExploreRun) string {
	if run.Error != "" {
		message := strings.ToLower(run.Error)
		switch {
		case strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
			return "timeout"
		case strings.Contains(message, "budget"):
			return "budget"
		default:
			return "error"
		}
	}
	if run.Found {
		return "found"
	}
	return "miss"
}

// LoadReplayInjection reads the adopted persisted trajectory. It never
// invokes the provider, preserving replay idempotency.
func (e *GraphMemoryRecallExecutor) LoadReplayInjection(ctx context.Context, plan *GraphMemoryRecallPlan) (*GraphMemoryRecallInjection, error) {
	var running int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM graph_memory_trajectory WHERE recall_id = $1 AND status = 'running'`, plan.RecallID).Scan(&running); err != nil {
		return nil, err
	}
	if running != 0 {
		return &GraphMemoryRecallInjection{Version: plan.GraphVersion}, nil
	}
	var summary string
	var submitted []byte
	var rounds int
	err := e.pool.QueryRow(ctx, `
		SELECT summary, submitted_node_ids, rounds
		FROM graph_memory_trajectory
		WHERE recall_id = $1 AND status = 'found'
		ORDER BY rounds ASC, seed_index ASC
		LIMIT 1
	`, plan.RecallID).Scan(&summary, &submitted, &rounds)
	if err == pgx.ErrNoRows {
		return &GraphMemoryRecallInjection{Version: plan.GraphVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var nodeIDs []string
	if err := json.Unmarshal(submitted, &nodeIDs); err != nil {
		return nil, fmt.Errorf("graph memory recall: decode submitted node ids: %w", err)
	}
	return graphMemoryRecallInjection(memorygraph.NewStore(plan.GraphDir), plan.GraphVersion, summary, nodeIDs, rounds), nil
}

func graphMemoryRecallInjection(store *memorygraph.Store, version int, summary string, nodeIDs []string, rounds int) *GraphMemoryRecallInjection {
	citations := graphMemoryRecallCitations(store, version, nodeIDs)
	return &GraphMemoryRecallInjection{
		Found: true, Summary: summary, Citations: citations,
		Content: graphMemoryRecallInjectionContent(summary, citations), Rounds: rounds, Version: version,
	}
}

func graphMemoryRecallCitations(store *memorygraph.Store, version int, nodeIDs []string) []memorygraph.Citation {
	graph, err := memorygraph.LoadGraph(store, version)
	if err != nil {
		return nil
	}
	citations := make([]memorygraph.Citation, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node := graph.Node(id)
		if node == nil {
			continue
		}
		citations = append(citations, memorygraph.Citation{NodeID: id, Level: node.Level, Epistemic: node.Epistemic})
	}
	return citations
}

func graphMemoryRecallInjectionContent(summary string, citations []memorygraph.Citation) string {
	summary = strings.TrimSpace(summary)
	if runes := []rune(summary); len(runes) > graphMemoryRecallMaxSummaryChars {
		summary = string(runes[:graphMemoryRecallMaxSummaryChars]) + graphMemoryRecallTruncationMarker
	}
	overflow := 0
	if len(citations) > graphMemoryRecallMaxCitationCount {
		overflow = len(citations) - graphMemoryRecallMaxCitationCount
		citations = citations[:graphMemoryRecallMaxCitationCount]
	}
	var b strings.Builder
	b.WriteString("## Graph Memory Recall\n")
	b.WriteString(summary)
	if len(citations) > 0 {
		b.WriteString("\n\ncited nodes:")
		for _, citation := range citations {
			fmt.Fprintf(&b, "\n- %s", citation.NodeID)
			qualifiers := make([]string, 0, 2)
			if citation.Level >= 0 {
				qualifiers = append(qualifiers, fmt.Sprintf("level %d", citation.Level))
			}
			if citation.Epistemic != "" {
				qualifiers = append(qualifiers, citation.Epistemic)
			}
			if len(qualifiers) > 0 {
				b.WriteString(" (")
				b.WriteString(strings.Join(qualifiers, ", "))
				b.WriteString(")")
			}
		}
		if overflow > 0 {
			fmt.Fprintf(&b, "\n- …and %d more", overflow)
		}
	}
	return b.String()
}

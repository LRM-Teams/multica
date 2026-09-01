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
)

const (
	graphMemoryRecallMaxSummaryChars  = 4000
	graphMemoryRecallMaxCitationCount = 16
	graphMemoryRecallTruncationMarker = "…[truncated]"

	// Bounded research federation (spec §4.4): filtered retrieval only, no
	// second Explore, no provider call.
	researchRecallTopK         = 5
	researchRecallMaxBodyChars = 600
)

// GraphMemoryRecallBackendFactory creates the provider backend for one
// server-authoritative recall plan.
type GraphMemoryRecallBackendFactory func(context.Context, *GraphMemoryRecallPlan) (memorygraph.AgentBackend, error)

// GraphMemoryRecallInjection is the bounded result returned to a daemon. Its
// content is rendered server-side so clients never run Explore locally.
type GraphMemoryRecallInjection struct {
	Found     bool                   `json:"found"`
	Summary   string                 `json:"summary"`
	Citations []memorygraph.Citation `json:"citations"`
	Content   string                 `json:"content"`
	// Research carries the federated research-graph section (spec §4.4) as
	// its own injection so the daemon can express the 16 KiB trim order
	// (historical first, then current, then research). Citations already
	// include the qualified research entries.
	Research string `json:"research"`
	Rounds   int    `json:"rounds"`
	Version  int    `json:"version"`
}

// GraphMemoryRecallExecutor owns synchronous Explore execution after Begin has
// durably created the recall ledger rows.
type GraphMemoryRecallExecutor struct {
	pool           *pgxpool.Pool
	dive           *GraphMemoryDiveService
	backendFactory GraphMemoryRecallBackendFactory
	embedder       *memorygraph.CachedEmbedder
	traces         *memorygraph.TraceRecorder
	model          string
	priorFlights   singleflight.Group
}

func NewGraphMemoryRecallExecutor(pool *pgxpool.Pool, dive *GraphMemoryDiveService, backendFactory GraphMemoryRecallBackendFactory, embedder *memorygraph.CachedEmbedder, traces *memorygraph.TraceRecorder, model string) *GraphMemoryRecallExecutor {
	return &GraphMemoryRecallExecutor{
		pool: pool, dive: dive, backendFactory: backendFactory, embedder: embedder, traces: traces, model: model,
	}
}

// Execute completes each persisted trajectory, queues Dive after the terminal
// barrier, and returns only the bounded adopted result. Provider and graph
// execution failures are ledger data; only database failures are returned.
func (e *GraphMemoryRecallExecutor) Execute(ctx context.Context, plan *GraphMemoryRecallPlan) (*GraphMemoryRecallInjection, error) {
	if e == nil || e.pool == nil {
		return nil, fmt.Errorf("graph memory recall executor is not configured")
	}
	store := memorygraph.NewStore(plan.GraphDir)
	if _, err := store.OpenSnapshot(plan.GraphVersion); err != nil {
		return e.executionFailure(ctx, plan, "pinned graph unavailable: "+err.Error())
	}

	retrievalCfg := memorygraph.DefaultRetrievalConfig()
	retrievalCfg.View = plan.GraphView
	retr := memorygraph.NewHybridRetriever(store, e.embedder, retrievalCfg)
	if err := retr.RebuildForVersion(ctx, plan.GraphVersion); err != nil {
		return e.executionFailure(ctx, plan, "pinned retriever unavailable: "+err.Error())
	}
	if e.backendFactory == nil {
		return e.executionFailure(ctx, plan, "agent backend is not configured")
	}
	backend, err := e.backendFactory(ctx, plan)
	if err != nil {
		return e.executionFailure(ctx, plan, "agent backend: "+err.Error())
	}

	cfg := memorygraph.DefaultExploreConfig()
	cfg.Agents = plan.K
	if plan.Tunables.ExploreMaxRounds > 0 {
		cfg.MaxRounds = plan.Tunables.ExploreMaxRounds
	}
	if plan.Tunables.ExploreNodesPerExpansion > 0 {
		cfg.ViewsPerExpansion = plan.Tunables.ExploreNodesPerExpansion
	}
	cfg.Model = e.model
	explorer := memorygraph.NewExplorer(store, retr, backend, cfg, "pi", e.traces)
	explorer.PinVersion(plan.GraphVersion)
	priorStore := memorygraph.NewPriorRecordStore(filepath.Join(plan.GraphDir, "continuation"))
	ownerKey := graphPriorOwnerKey(plan)
	var brief *memorygraph.PriorBrief
	if rec, err := priorStore.Load(ownerKey); err != nil {
		slog.Warn("graph memory recall: prior record load failed; continuing without prior", "recall_id", plan.RecallID, "error", err)
	} else if rec != nil && rec.GraphVersion == plan.GraphVersion {
		brief = e.priorBrief(ctx, plan, rec, priorStore, ownerKey, backend)
	}
	result, err := explorer.ExploreWithPrior(ctx, plan.Query, plan.Seeds, brief)
	if err != nil {
		return e.executionFailure(ctx, plan, "explore: "+err.Error())
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
	if err := e.persistRuns(ctx, plan.RecallID, result.AgentRuns); err != nil {
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
	// Research federation (spec §4.4) joins after the primary explore: the
	// research graph supplements scope-resolved recall but never substitutes
	// for it. A primary miss plus research hits still surfaces knowledge.
	researchContent, researchCitations := e.researchRecall(ctx, plan)
	if !result.Found && researchContent == "" {
		return &GraphMemoryRecallInjection{Version: plan.GraphVersion, Rounds: result.Rounds}, nil
	}
	injection := &GraphMemoryRecallInjection{Version: plan.GraphVersion, Rounds: result.Rounds}
	if result.Found {
		injection = graphMemoryRecallInjection(store, plan.GraphVersion, result.Summary, result.NodeIDs, result.Rounds)
	}
	if researchContent != "" {
		injection.Research = researchContent
		injection.Citations = append(injection.Citations, researchCitations...)
		injection.Found = true
	}
	return injection, nil
}

// researchRecall runs filtered retrieval over the federated research graph:
// one bounded hybrid search under a research-only view, no second Explore and
// no provider call (spec §4.4). Hits are recorded in the research graph's own
// query log so federated reads drive its maintenance rounds. Every failure
// degrades to empty — federation is additive and never blocks recall.
func (e *GraphMemoryRecallExecutor) researchRecall(ctx context.Context, plan *GraphMemoryRecallPlan) (string, []memorygraph.Citation) {
	if plan.Research == nil || strings.TrimSpace(plan.Query) == "" {
		return "", nil
	}
	store := memorygraph.NewStore(plan.Research.Dir)
	if _, err := store.OpenSnapshot(plan.Research.Version); err != nil {
		slog.Warn("graph memory recall: research snapshot unavailable", "recall_id", plan.RecallID, "error", err)
		return "", nil
	}
	cfg := memorygraph.DefaultRetrievalConfig()
	cfg.View = plan.Research.View
	cfg.TopK = researchRecallTopK
	retr := memorygraph.NewHybridRetriever(store, e.embedder, cfg)
	if err := retr.RebuildForVersion(ctx, plan.Research.Version); err != nil {
		slog.Warn("graph memory recall: research retriever unavailable", "recall_id", plan.RecallID, "error", err)
		return "", nil
	}
	docs, err := retr.Search(ctx, plan.Query)
	if err != nil || len(docs) == 0 {
		return "", nil
	}
	graph, err := memorygraph.LoadGraph(store, plan.Research.Version)
	if err != nil {
		return "", nil
	}
	hits := make([]researchRecallHit, 0, len(docs))
	nodeIDs := make([]string, 0, len(docs))
	for _, doc := range docs {
		node := graph.Node(doc.ID)
		if node == nil {
			continue
		}
		nodeIDs = append(nodeIDs, node.NodeID)
		hits = append(hits, researchRecallHit{
			Citation: memorygraph.Citation{NodeID: node.NodeID, Level: node.Level, Epistemic: node.Epistemic},
			Body:     node.Body,
		})
	}
	if len(hits) == 0 {
		return "", nil
	}
	if err := memorygraph.NewQueryRecorder(store, "daemon").RecordRecall(memorygraph.QueryLogEntry{
		TraceID:   plan.TraceID,
		Query:     plan.Query,
		Timestamp: time.Now().UTC(),
		Version:   plan.Research.Version,
		NodeIDs:   nodeIDs,
		Found:     true,
	}); err != nil {
		slog.Warn("graph memory recall: research query log append failed", "trace_id", plan.TraceID, "error", err)
	}
	section, qualified := researchRecallSection(plan.WorkspaceID, hits, nil)
	return section, qualified
}

// researchRecallHit is one research retrieval hit with its markdown body for
// the rendered section.
type researchRecallHit struct {
	Citation memorygraph.Citation
	Body     string
}

// researchQualifiedNodeID graph-qualifies a research node id so citations
// stay distinguishable from primary-graph ids: research:<workspace>/node:<id>.
func researchQualifiedNodeID(workspaceID, nodeID string) string {
	return "research:" + workspaceID + "/node:" + nodeID
}

// researchRecallSection renders the federated research section and returns
// the merged citation list: qualified research entries first, then the bare
// primary citations unchanged.
func researchRecallSection(workspaceID string, hits []researchRecallHit, primary []memorygraph.Citation) (string, []memorygraph.Citation) {
	if len(hits) == 0 {
		return "", primary
	}
	citations := make([]memorygraph.Citation, 0, len(hits)+len(primary))
	var b strings.Builder
	b.WriteString("## Research Memory\n")
	for _, hit := range hits {
		qualifiedID := researchQualifiedNodeID(workspaceID, hit.Citation.NodeID)
		citations = append(citations, memorygraph.Citation{
			NodeID: qualifiedID, Level: hit.Citation.Level, Epistemic: hit.Citation.Epistemic,
		})
		b.WriteString("\n- ")
		b.WriteString(qualifiedID)
		var qualifiers []string
		if hit.Citation.Epistemic != "" {
			qualifiers = append(qualifiers, hit.Citation.Epistemic)
		}
		if len(qualifiers) > 0 {
			b.WriteString(" (")
			b.WriteString(strings.Join(qualifiers, ", "))
			b.WriteString(")")
		}
		body := strings.TrimSpace(hit.Body)
		if body != "" {
			if runes := []rune(body); len(runes) > researchRecallMaxBodyChars {
				body = string(runes[:researchRecallMaxBodyChars]) + graphMemoryRecallTruncationMarker
			}
			b.WriteString(": ")
			b.WriteString(body)
		}
	}
	citations = append(citations, primary...)
	return b.String(), citations
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
func (e *GraphMemoryRecallExecutor) priorBrief(ctx context.Context, plan *GraphMemoryRecallPlan, rec *memorygraph.PriorRecord, store *memorygraph.PriorRecordStore, ownerKey string, backend memorygraph.AgentBackend) *memorygraph.PriorBrief {
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
		return memorygraph.NewPriorCompressor(backend, e.model, memorygraph.DefaultPriorCompressionTimeout).
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

func (e *GraphMemoryRecallExecutor) executionFailure(ctx context.Context, plan *GraphMemoryRecallPlan, reason string) (*GraphMemoryRecallInjection, error) {
	if err := e.persistFailure(ctx, plan.RecallID, reason); err != nil {
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

func (e *GraphMemoryRecallExecutor) persistFailure(ctx context.Context, recallID, reason string) error {
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
	`, recallID, e.model); err != nil {
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

func (e *GraphMemoryRecallExecutor) persistRuns(ctx context.Context, recallID string, runs []memorygraph.ExploreRun) error {
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
		`, recallID, run.Seed, status, errorKind, run.Summary, viewed, submitted, run.Rounds, e.model); err != nil {
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

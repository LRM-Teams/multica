package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// AgentBackend is the narrow execution surface the Explorer needs from
// pkg/agent backends. Keeping it minimal enables fakes in tests.
type AgentBackend interface {
	Execute(ctx context.Context, prompt string, opts agent.ExecOptions) (*agent.Session, error)
}

// ExploreConfig configures the explore-agent layer (design §6 explore block).
type ExploreConfig struct {
	Agents            int // TTT K parallel trajectories; 1 in non-TTT mode
	MaxRounds         int // exploration-round budget per trajectory (one served node = one round)
	MaxExpandPerRound int // inline-neighbor cap per served node
	// ViewsPerExpansion is retained only for caller compatibility; /explore
	// has no per-expansion view quota and does not consume it.
	ViewsPerExpansion int
	MaxNodeChars      int           // node-body truncation served by /explore
	Temperature       float64       // trajectory sampling temperature (wired at integration time)
	Model             string        // model name passed to the agent backend
	Timeout           time.Duration // per-trajectory wall-clock timeout
}

// DefaultExploreConfig returns the conservative design §6 defaults.
func DefaultExploreConfig() ExploreConfig {
	return ExploreConfig{
		Agents:            1,
		MaxRounds:         6,
		MaxExpandPerRound: 5,
		ViewsPerExpansion: 1,
		MaxNodeChars:      2000,
		Timeout:           5 * time.Minute,
	}
}

// normalized fills zero/negative fields with DefaultExploreConfig values.
func (c ExploreConfig) normalized() ExploreConfig {
	d := DefaultExploreConfig()
	if c.Agents < 1 {
		c.Agents = d.Agents
	}
	if c.MaxRounds < 1 {
		c.MaxRounds = d.MaxRounds
	}
	if c.MaxExpandPerRound < 1 {
		c.MaxExpandPerRound = d.MaxExpandPerRound
	}
	if c.MaxNodeChars < 1 {
		c.MaxNodeChars = d.MaxNodeChars
	}
	if c.Timeout <= 0 {
		c.Timeout = d.Timeout
	}
	return c
}

// Explorer runs the explore-agent layer of design §5.2: hybrid retrieval
// produces seed nodes, then cfg.Agents agent trajectories explore the graph
// in parallel through the loopback tool server, and the fewest-rounds
// successful trajectory is adopted.
type Explorer struct {
	store    *Store
	retr     *HybridRetriever
	backend  AgentBackend
	cfg      ExploreConfig
	provider string         // agent CLI provider name (e.g. "pi"); integration-time wiring
	traces   *TraceRecorder // nil → trajectories are drained but not persisted

	// pinnedVersion, when > 0, forces Explore to run against that graph
	// version instead of resolving the current pointer. Production callers
	// always pin the DB-authoritative publication version (Task 14); the
	// file-pointer fallback below serves offline tooling only (the
	// FullBacktestRunner uses pinning to evaluate candidate versions).
	pinnedVersion int
}

// NewExplorer returns an Explorer over the given store/retriever. cfg zero
// values fall back to DefaultExploreConfig. provider is informational until
// the provider-extension wiring lands at integration time. traces may be
// nil, in which case trajectories are not persisted.
func NewExplorer(store *Store, retr *HybridRetriever, backend AgentBackend, cfg ExploreConfig, provider string, traces *TraceRecorder) *Explorer {
	return &Explorer{
		store:    store,
		retr:     retr,
		backend:  backend,
		cfg:      cfg.normalized(),
		provider: provider,
		traces:   traces,
	}
}

// PinVersion forces Explore to run against the given graph version
// regardless of the current pointer (used by the full backtest runner to
// evaluate a candidate version). A zero value restores the default
// resolve-current-at-start behavior.
func (e *Explorer) PinVersion(version int) { e.pinnedVersion = version }

// exploreSeed is one hybrid-retrieval hit embedded in the trajectory prompt.
type exploreSeed struct {
	id      string
	snippet string
}

// exploreOutput is the strict-JSON final-response contract of the explore
// agent prompt.
type exploreOutput struct {
	Found   bool     `json:"found"`
	Summary string   `json:"summary"`
	NodeIDs []string `json:"node_ids"`
	Rounds  int      `json:"rounds"`
}

// Explore recalls memory relevant to query. A miss (no trajectory found
// relevant information, or every trajectory failed) is data, not an error:
// it returns Found=false with a nil error.
// Explore recalls memory relevant to query, computing hybrid-retrieval
// seeds internally. It is ExploreWithSeeds with a nil seed list.
func (e *Explorer) Explore(ctx context.Context, query string) (*RecallResult, error) {
	return e.ExploreWithSeeds(ctx, query, nil)
}

// ExploreWithSeeds is ExploreWithPrior without prior evidence.
func (e *Explorer) ExploreWithSeeds(ctx context.Context, query string, seedIDs []string) (*RecallResult, error) {
	return e.ExploreWithPrior(ctx, query, seedIDs, nil)
}

// ExploreWithPrior runs Explore against server-persisted round-0 seed node
// ids, optionally appending visible, hydrated prior nodes and injecting the
// previous recall's tentative evidence. Fresh seeds always remain first. An
// empty seed list falls back to internal hybrid search. A miss is data, not
// an error: it returns Found=false with a nil error.
func (e *Explorer) ExploreWithPrior(ctx context.Context, query string, seedIDs []string, prior *PriorBrief) (*RecallResult, error) {
	if e.backend == nil {
		return nil, fmt.Errorf("explore: agent backend not configured")
	}
	if e.retr == nil {
		return nil, fmt.Errorf("explore: retriever not configured")
	}

	// Version pinning (design R5/R12): resolve the graph version ONCE — the
	// pinned version when set, else the current pointer — and serve the
	// whole call (seed hydration, /explore, /submit validation) from that
	// version, so a mid-explore consolidation switch never swaps the graph
	// under an in-flight trajectory.
	version := e.pinnedVersion
	if version <= 0 {
		var err error
		version, err = e.store.CurrentVersion()
		if err != nil {
			return nil, fmt.Errorf("explore: current version: %w", err)
		}
	}
	retr, err := e.retr.ForkForVersion(ctx, version)
	if err != nil {
		return nil, fmt.Errorf("explore: pin retriever to v%d: %w", version, err)
	}

	// (a) Round-0 seeds: the persisted batch when provided, else internal
	// hybrid retrieval.
	var seeds []exploreSeed
	if len(seedIDs) > 0 {
		seeds = e.seedsFromIDs(seedIDs, version)
	} else {
		hits, err := retr.Search(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("explore: seed retrieval: %w", err)
		}
		seeds = e.seedSnippets(hits, version)
	}
	if prior != nil {
		seeds = e.mergePriorSeeds(retr, seeds, prior, version)
		prior = e.visiblePrior(retr, prior, version)
	}

	// Tool server shared by all trajectories of this Explore call.
	srv, err := NewExploreToolServer(e.store, retr, e.cfg, version)
	if err != nil {
		return nil, err
	}
	baseURL, token, err := srv.Start(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// (b) Run trajectories in parallel; each records its index as Seed. The
	// trace id (query id) is fixed up front so every run's trajectory file
	// is named after it.
	traceID := uuid.NewString()
	runs := make([]ExploreRun, e.cfg.Agents)
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			runs[seed] = e.runTrajectory(ctx, srv, baseURL, token, traceID, version, query, seed, seeds, prior)
		}(i)
	}
	wg.Wait()

	// (e) Adoption: fewest rounds among successful found runs (tie: lowest
	// seed, which is the iteration order here).
	result := &RecallResult{TraceID: traceID, Version: version, AgentRuns: runs}
	adopted := -1
	for i := range runs {
		r := &runs[i]
		if r.Error != "" || !r.Found {
			continue
		}
		if adopted < 0 || r.Rounds < runs[adopted].Rounds {
			adopted = i
		}
	}
	if adopted >= 0 {
		a := runs[adopted]
		result.Found = true
		result.Summary = a.Summary
		result.NodeIDs = a.NodeIDs
		result.Rounds = a.Rounds
		result.Citations = qualifyRecallCitations(e.store, version, a.NodeIDs)
		result.AdoptedIndex = adopted
		result.AdoptedTranscript = sanitizeTranscript(a.Messages)
	}
	for i := range runs {
		runs[i].Messages = nil
	}
	if adopted >= 0 {
		return result, nil
	}
	for i := range runs {
		r := &runs[i]
		if r.Error != "" {
			continue
		}
		if result.Rounds == 0 || r.Rounds < result.Rounds {
			result.Rounds = r.Rounds
		}
	}
	return result, nil
}

// sanitizeTranscript maps the adopted run's message stream onto the
// allowlisted TraceMessage shape (same columns as the trace writer).
func sanitizeTranscript(msgs []agent.Message) []TraceMessage {
	out := make([]TraceMessage, 0, len(msgs))
	for i, m := range msgs {
		out = append(out, serializeTraceMessage(i, m))
	}
	return out
}

// runTrajectory executes one explore trajectory: one backend.Execute call
// whose prompt embeds the seeds, budget rules, tool-server credentials and
// the strict-JSON final-response contract. Failures are recorded in
// ExploreRun.Error; the run's rounds are cross-checked against the
// server-side expand count (max of reported and counted), and a
// budget-blown trajectory (server-side, design Q15/A6) is forced to
// Found=false.
func (e *Explorer) runTrajectory(ctx context.Context, srv *ExploreToolServer, baseURL, token, traceID string, version int, query string, seed int, seeds []exploreSeed, prior *PriorBrief) (run ExploreRun) {
	run = ExploreRun{RunID: uuid.NewString(), Seed: seed}
	trajectoryID := run.RunID

	prompt := e.buildPrompt(baseURL, token, trajectoryID, query, seed, seeds, prior)
	startedAt := time.Now().UTC()

	// Persist the trajectory best-effort once the outcome is final (the
	// deferred write observes the named return value, including the
	// budget-blown Found=false override). A nil drain writes header+footer
	// only; a nil recorder skips the write entirely.
	var drain *TraceDrain
	defer func() {
		e.traces.WriteExploreTrace(ExploreTraceMeta{
			TraceID:      traceID,
			RunID:        run.RunID,
			GraphVersion: version,
			Seed:         seed,
			Model:        e.cfg.Model,
			StartedAt:    startedAt,
			PromptChars:  len(prompt),
		}, drain, run)
	}()

	execCtx := ctx
	cancel := context.CancelFunc(func() {})
	if e.cfg.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
	}
	defer cancel()

	session, err := e.backend.Execute(execCtx, prompt, agent.ExecOptions{
		Model:            e.cfg.Model,
		ThreadName:       "memorygraph-explore",
		Timeout:          e.cfg.Timeout,
		EphemeralSession: true,
	})
	if err != nil {
		run.Error = fmt.Sprintf("execute: %v", err)
		return run
	}
	// Start draining the message stream immediately, BEFORE waiting on
	// Result: Session.Messages is a 256-cap buffered channel and a long
	// trajectory stalls when nobody reads it. The channel closes before
	// Result resolves, so the drain is complete by trace-write time.
	drain = e.traces.Drain(session.Messages)
	result, ok := <-session.Result
	if !ok {
		run.Error = "agent session ended without a result"
		return run
	}
	run.Messages = drain.Messages()
	if result.Status != "completed" {
		reason := strings.TrimSpace(result.Error)
		if reason == "" {
			reason = "agent did not complete: " + result.Status
		}
		run.Error = reason
		return run
	}

	// (d) Strict-JSON parsing of the final response.
	var out exploreOutput
	if !extractExploreOutput(result.Output, &out) {
		run.Error = "final response is not a valid explore JSON object"
		return run
	}
	sub := srv.trajectorySubmission(trajectoryID)
	if sub == nil {
		run.Error = "agent completed without a tool-server submission"
		return run
	}
	run.Found = sub.Found
	run.Summary = sub.Summary
	run.NodeIDs = sub.NodeIDs
	run.ViewedNodeIDs = srv.trajectoryViewed(trajectoryID)
	run.Rounds = srv.trajectoryRounds(trajectoryID)
	// Budget enforcement cross-check (design Q15/A6): a trajectory that kept
	// expanding past the round budget is never adopted, no matter what the
	// agent's final response claims.
	if srv.trajectoryBudgetBlown(trajectoryID) {
		run.Found = false
	}
	return run
}

// qualifyRecallCitations attaches level/epistemic qualifiers from the pinned
// graph version to each adopted node id (spec §3 step 8). Best-effort: ids
// that are not graph nodes (staging segments) — or every id when the pinned
// graph cannot be read — yield bare citations with Level -1.
func qualifyRecallCitations(store *Store, version int, ids []string) []Citation {
	if len(ids) == 0 {
		return nil
	}
	var g *Graph
	if graph, err := LoadGraph(store, version); err == nil {
		g = graph
	}
	out := make([]Citation, 0, len(ids))
	for _, id := range ids {
		c := Citation{NodeID: id, GraphVersion: version, Level: -1, CapturedAt: time.Now().UTC()}
		if g != nil {
			if n := g.Node(id); n != nil {
				c.Level = n.Level
				c.Epistemic = n.Epistemic
				c.Tags = append([]string(nil), n.Tags...)
				c.Title, c.FirstParagraph = citationTextSnapshot(n.Body)
				c.Excerpt = c.FirstParagraph
				c.ContentHash = n.ContentHash
			}
		}
		out = append(out, c)
	}
	return out
}

func citationTextSnapshot(body string) (string, string) {
	paragraphs := strings.Split(strings.TrimSpace(body), "\n\n")
	first := ""
	for _, paragraph := range paragraphs {
		if value := strings.TrimSpace(paragraph); value != "" {
			first = value
			break
		}
	}
	if first == "" {
		return "", ""
	}
	lines := strings.Split(first, "\n")
	title := strings.TrimSpace(strings.TrimLeft(lines[0], "# "))
	if len(title) > 160 {
		title = title[:160]
	}
	if len(first) > 500 {
		first = first[:500]
	}
	return title, first
}

// seedSnippets resolves retrieval hits to prompt snippets, reading graph
// node bodies of the pinned version and staging segment bodies.
func (e *Explorer) seedSnippets(hits []ScoredDoc, version int) []exploreSeed {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return e.seedsFromIDs(ids, version)
}

// seedsFromIDs hydrates seed node ids into prompt seeds by reading each
// node's body from the pinned version (staging segments included), so a
// persisted seed batch and a fresh hybrid batch produce identical seeds.
func (e *Explorer) seedsFromIDs(ids []string, version int) []exploreSeed {
	var g *Graph
	if e.store != nil {
		if loaded, err := LoadGraph(e.store, version); err == nil {
			g = loaded
		}
	}
	seeds := make([]exploreSeed, 0, len(ids))
	for _, id := range ids {
		var body string
		switch {
		case IsStagingID(id) && e.store != nil:
			if b, err := e.store.ReadStagingSegment(strings.TrimPrefix(id, stagingDocPrefix)); err == nil {
				body = string(b)
			}
		case g != nil:
			if n := g.Node(id); n != nil {
				body = n.Body
			}
		}
		if len(body) > expandSnippetChars {
			body = body[:expandSnippetChars] + "..."
		}
		seeds = append(seeds, exploreSeed{id: id, snippet: body})
	}
	return seeds
}

// mergePriorSeeds appends prior node ids that are view-visible and hydrate
// to a non-empty body at the pinned version. Fresh seeds always come first;
// duplicates and unknown ids are skipped.
func (e *Explorer) mergePriorSeeds(retr *HybridRetriever, seeds []exploreSeed, prior *PriorBrief, version int) []exploreSeed {
	seen := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		seen[s.id] = true
	}
	for _, id := range prior.NodeIDs {
		if seen[id] || !retr.AllowsNodeID(id) {
			continue
		}
		merged := e.seedsFromIDs([]string{id}, version)
		if len(merged) == 1 && merged[0].snippet != "" {
			seeds = append(seeds, merged[0])
			seen[id] = true
		}
	}
	return seeds
}

// visiblePrior retains only prior node ids that pass the active graph view
// and hydrate at the pinned version before they enter the prompt evidence.
func (e *Explorer) visiblePrior(retr *HybridRetriever, prior *PriorBrief, version int) *PriorBrief {
	filtered := *prior
	filtered.NodeIDs = nil
	seen := make(map[string]bool, len(prior.NodeIDs))
	for _, id := range prior.NodeIDs {
		if seen[id] || !retr.AllowsNodeID(id) {
			continue
		}
		seed := e.seedsFromIDs([]string{id}, version)
		if len(seed) == 1 && seed[0].snippet != "" {
			filtered.NodeIDs = append(filtered.NodeIDs, id)
			seen[id] = true
		}
	}
	return &filtered
}

// buildPrompt assembles the trajectory prompt. The tool server is reached
// over loopback HTTP with curl-style calls from the agent's shell tool;
// provider-extension wiring replaces these instructions at integration time.
func (e *Explorer) buildPrompt(baseURL, token, trajectoryID, query string, seed int, seeds []exploreSeed, prior *PriorBrief) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are memory-graph explore trajectory #%d (seed %d). Answer the query by exploring the memory graph through a local HTTP tool server, then submit your findings.\n\n", seed, seed)
	fmt.Fprintf(&b, "Query: %s\n\n", query)
	fmt.Fprintf(&b, "Trajectory ID: %s\n", trajectoryID)
	fmt.Fprintf(&b, "Use this exact value as \"trajectory_id\" in every tool call.\n\n")

	b.WriteString("Initial nodes from hybrid retrieval (start here):\n")
	if len(seeds) == 0 {
		b.WriteString("- (none; begin by expanding any node id you discover)\n")
	}
	for _, s := range seeds {
		fmt.Fprintf(&b, "- %s: %s\n", s.id, s.snippet)
	}
	if prior != nil {
		b.WriteString("\nPrior exploration evidence from the previous recall in this channel (MAY BE STALE OR WRONG - this round's query always wins; re-read any node via /explore to verify before relying on it):\n")
		fmt.Fprintf(&b, "- prior summary: %s\n", prior.Summary)
		if len(prior.NodeIDs) > 0 {
			fmt.Fprintf(&b, "- prior node ids: %s\n", strings.Join(prior.NodeIDs, ", "))
		}
		for _, o := range prior.Observations {
			fmt.Fprintf(&b, "- observation: %s\n", o)
		}
		for _, rj := range prior.Rejected {
			fmt.Fprintf(&b, "- rejected branch: %s\n", rj)
		}
		for _, q := range prior.OpenQuestions {
			fmt.Fprintf(&b, "- open question: %s\n", q)
		}
	}

	fmt.Fprintf(&b, "\nTool server base URL: %s\n", baseURL)
	fmt.Fprintf(&b, "Bearer token: %s\n\n", token)
	b.WriteString("Call it with your shell tool using curl. Every request needs the Authorization header and a JSON body, e.g.:\n")
	fmt.Fprintf(&b, "  curl -s -X POST %s/explore -H \"Authorization: Bearer %s\" -H \"Content-Type: application/json\" -d '{\"trajectory_id\":\"%s\",\"node_ids\":[\"<node_id>\"]}'\n\n", baseURL, token, trajectoryID)

	b.WriteString("Endpoints:\n")
	fmt.Fprintf(&b, "- POST /explore {\"trajectory_id\",\"node_ids\":[...]} -> for each requested node: its body (truncated to %d chars) plus level, epistemic_status, tags, and inline neighbors ordered by relevance (hierarchy parents/children, entity co-occurrence, typed relations, embedding neighbors) each with id/via/level/snippet. \"seg:\" ids are staging segments (body only, no neighbors). At most %d neighbors per node; response has {\"round\",\"budget_exceeded\",\"nodes\"}. Reading a neighbor's full body costs one more /explore on that id.\n", e.cfg.MaxNodeChars, e.cfg.MaxExpandPerRound)
	b.WriteString("- POST /submit {\"trajectory_id\",\"found\":bool,\"summary\",\"node_ids\":[...]} -> record your final answer.\n\n")

	b.WriteString("Budget rules (hard limits):\n")
	fmt.Fprintf(&b, "- At most %d exploration rounds; each node served by /explore consumes one round (a batch of N nodes consumes N rounds). Once the budget is spent the server rejects further explores with budget_exceeded=true and empty nodes.\n", e.cfg.MaxRounds)
	fmt.Fprintf(&b, "- Each served node lists at most %d neighbors.\n", e.cfg.MaxExpandPerRound)
	fmt.Fprintf(&b, "- Node bodies are truncated to %d characters.\n\n", e.cfg.MaxNodeChars)

	b.WriteString("When you find information relevant to the query, call /submit with found=true, a concise summary, and the supporting node ids. If the budget is exhausted without finding relevant information, call /submit with found=false.\n\n")
	b.WriteString("Your FINAL response must be exactly one JSON object and nothing else (no prose, no markdown fences):\n")
	b.WriteString("{\"found\":bool,\"summary\":string,\"node_ids\":[string],\"rounds\":int}\n")
	return b.String()
}

// extractExploreOutput parses the strict-JSON final response, tolerating
// surrounding prose by slicing from the first "{" to the last "}" (same
// approach as memorycuration's team_output.go).
func extractExploreOutput(output string, dst *exploreOutput) bool {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return false
	}
	return json.Unmarshal([]byte(output[start:end+1]), dst) == nil
}

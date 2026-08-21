package memorygraph

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// exploreToolServerMaxBody is the hard cap on tool-server request bodies.
const exploreToolServerMaxBody = 64 * 1024

// expandSnippetChars caps the neighbor snippet returned by /explore.
const expandSnippetChars = 200

// ExploreToolServer exposes the explore-agent graph operations over a
// loopback HTTP server, mirroring the diagnosis tool server pattern. One
// server serves one Explore call; it is started with Start, handed to the
// agent CLIs via the prompt, and torn down with Shutdown.
//
// Endpoints (all require "Authorization: Bearer <token>"):
//
//	POST /explore {trajectory_id, node_ids[]} -> each node's body + inline
//	    neighbor edge info; rounds consumed = nodes served (spec §4);
//	    beyond the round budget: 200 {"budget_exceeded":true,"nodes":[]} (Q15/A6)
//	POST /submit  {trajectory_id, found, summary, node_ids} -> record the final answer
type ExploreToolServer struct {
	store *Store
	retr  *HybridRetriever // may be nil; embedding neighbors are then skipped
	cfg   ExploreConfig
	// version is the graph version this server is pinned to (design R5/R12):
	// /explore and /submit read this version's dir for the whole Explore
	// call and never re-resolve the current pointer.
	version int

	httpServer  *http.Server
	baseURL     string
	bearerToken string

	mu           sync.Mutex
	trajectories map[string]*trajectoryState
}

// trajectoryState is the per-trajectory server-side accounting. The rounds
// counter is the authoritative exploration-round count (one served node =
// one round, spec §4.2); budgetBlown is set when the trajectory kept calling
// /explore after reaching MaxRounds (design Q15/A6: the budget is enforced
// server-side); the submission records the trajectory's /submit payload.
type trajectoryState struct {
	rounds      int
	budgetBlown bool
	submission  *submitRecord
}

// submitRecord is the validated /submit payload kept server-side.
type submitRecord struct {
	Found    bool     `json:"found"`
	Summary  string   `json:"summary"`
	NodeIDs  []string `json:"node_ids"`
	Warnings []string `json:"warnings,omitempty"`
}

// NewExploreToolServer creates the server with a cryptographically random
// 32-byte bearer token, pinned to graph version v. cfg is normalized with
// DefaultExploreConfig values for zero fields.
func NewExploreToolServer(store *Store, retr *HybridRetriever, cfg ExploreConfig, version int) (*ExploreToolServer, error) {
	cfg = cfg.normalized()
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("explore tool server: bearer token: %w", err)
	}
	s := &ExploreToolServer{
		store:        store,
		retr:         retr,
		cfg:          cfg,
		version:      version,
		bearerToken:  fmt.Sprintf("%x", token),
		trajectories: make(map[string]*trajectoryState),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /explore", s.handleExplore)
	mux.HandleFunc("POST /submit", s.handleSubmit)

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s, nil
}

// Start binds to 127.0.0.1:0 and starts serving. It returns the allocated
// base URL and the per-session bearer token. The caller must call Shutdown.
func (s *ExploreToolServer) Start(ctx context.Context) (baseURL, token string, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("explore tool server: listen: %w", err)
	}
	s.baseURL = fmt.Sprintf("http://%s", ln.Addr().String())
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("explore tool server: serve", "error", err)
		}
	}()
	return s.baseURL, s.bearerToken, nil
}

// Shutdown gracefully stops the HTTP server.
func (s *ExploreToolServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// trajectoryRounds returns the server-side round count for a trajectory
// (zero for unknown trajectories).
func (s *ExploreToolServer) trajectoryRounds(trajectoryID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.trajectories[trajectoryID]; ok {
		return st.rounds
	}
	return 0
}

// trajectoryBudgetBlown reports whether the trajectory exceeded the
// exploration-round budget (design Q15/A6). A budget-blown trajectory's
// submission is forced to Found=false server-side.
func (s *ExploreToolServer) trajectoryBudgetBlown(trajectoryID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.trajectories[trajectoryID]; ok {
		return st.budgetBlown
	}
	return false
}

// trajectorySubmission returns the recorded submission for a trajectory, or
// nil when the trajectory never called /submit.
func (s *ExploreToolServer) trajectorySubmission(trajectoryID string) *submitRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.trajectories[trajectoryID]; ok {
		return st.submission
	}
	return nil
}

// stateLocked returns the lazily created state for a trajectory.
func (s *ExploreToolServer) stateLocked(trajectoryID string) *trajectoryState {
	st, ok := s.trajectories[trajectoryID]
	if !ok {
		st = &trajectoryState{}
		s.trajectories[trajectoryID] = st
	}
	return st
}

// ---------------------------------------------------------------------------
// auth + envelope helpers
// ---------------------------------------------------------------------------

func (s *ExploreToolServer) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := auth[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.bearerToken)) == 1
}

func exploreWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func exploreWriteError(w http.ResponseWriter, status int, code, message string) {
	exploreWriteJSON(w, status, map[string]string{"error": code, "message": message})
}

// decodeExploreRequest enforces auth, the body cap and JSON decoding.
func (s *ExploreToolServer) decodeExploreRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !s.checkAuth(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, exploreToolServerMaxBody))
	if err != nil {
		exploreWriteError(w, http.StatusBadRequest, "BAD_BODY", "cannot read request body")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		exploreWriteError(w, http.StatusBadRequest, "BAD_JSON", "invalid request body")
		return false
	}
	return true
}

// viewAllows reports whether the graph view attached to the retriever (if
// any) permits access to n (spec §5: the view is reapplied at every
// traversal step so edges can never bypass scope). Without a retriever or
// with an inactive (zero) view, all nodes are allowed — legacy behavior.
func (s *ExploreToolServer) viewAllows(n *Node) bool {
	if s.retr == nil || !s.retr.viewActive() {
		return true
	}
	return s.retr.cfg.View.Allows(n)
}

// loadGraph loads the pinned version graph.
func (s *ExploreToolServer) loadGraph() (*Graph, error) {
	g, err := LoadGraph(s.store, s.version)
	if err != nil {
		return nil, fmt.Errorf("load graph v%d: %w", s.version, err)
	}
	return g, nil
}

// ---------------------------------------------------------------------------
// POST /explore
// ---------------------------------------------------------------------------

type exploreRequest struct {
	TrajectoryID string   `json:"trajectory_id"`
	NodeIDs      []string `json:"node_ids"`
}

// expandCandidate is one neighbor offered to the explore agent. Via records
// how the candidate was reached: "parent", "child", "entity", a relation
// edge type, or "embedding".
type expandCandidate struct {
	NodeID  string `json:"node_id"`
	Via     string `json:"via"`
	Level   int    `json:"level"` // -1 for staging segments
	Snippet string `json:"snippet"`
}

// exploredNode is one served node: its body plus inline neighbor edge info,
// so the agent sees a node and its edges in one call (spec §4.1).
type exploredNode struct {
	NodeID          string            `json:"node_id"`
	Level           int               `json:"level"` // -1 for staging segments
	EpistemicStatus string            `json:"epistemic_status,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Body            string            `json:"body"`
	Truncated       bool              `json:"truncated"`
	Staging         bool              `json:"staging,omitempty"`
	Neighbors       []expandCandidate `json:"neighbors,omitempty"`
}

type exploreResponse struct {
	Round          int            `json:"round"` // cumulative rounds consumed after this call
	BudgetExceeded bool           `json:"budget_exceeded"`
	Nodes          []exploredNode `json:"nodes"`
}

func (s *ExploreToolServer) handleExplore(w http.ResponseWriter, r *http.Request) {
	var req exploreRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || len(req.NodeIDs) == 0 {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id and node_ids are required")
		return
	}
	s.mu.Lock()
	s.stateLocked(req.TrajectoryID)
	s.mu.Unlock()

	g, err := s.loadGraph()
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
		return
	}

	// Fail closed before consuming any rounds: every requested node must
	// resolve within the caller's graph view. A batch with one unknown or
	// out-of-view node is rejected whole, mirroring the missing-node shape
	// (no existence leak, spec §5).
	for _, id := range req.NodeIDs {
		if IsStagingID(id) {
			segID := strings.TrimPrefix(id, stagingDocPrefix)
			if _, err := s.store.ReadStagingSegment(segID); err != nil {
				exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "staging segment not found")
				return
			}
			continue
		}
		n := g.Node(id)
		if n == nil || !s.viewAllows(n) {
			exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
	}

	// Rounds are consumed per node served (server-authoritative, spec §4.2).
	// Budget enforcement: once the round counter reaches MaxRounds, further
	// calls are rejected with HTTP 200 and {"budget_exceeded":true,"nodes":[]}
	// — a 200, not a 429, so curl-driven agents parse one response shape.
	// Blowing the budget means asking for more than it allows: a rejected
	// call or a request that straddles the remaining budget (partially
	// served) marks the trajectory budget-blown, which later forces its
	// /submit to Found=false. A request fully served within budget never
	// blows, even when it spends the last round.
	s.mu.Lock()
	st := s.stateLocked(req.TrajectoryID)
	if st.rounds >= s.cfg.MaxRounds {
		st.budgetBlown = true
		round := st.rounds
		s.mu.Unlock()
		exploreWriteJSON(w, http.StatusOK, exploreResponse{
			Round:          round,
			BudgetExceeded: true,
			Nodes:          []exploredNode{},
		})
		return
	}
	remaining := s.cfg.MaxRounds - st.rounds
	if len(req.NodeIDs) > remaining {
		st.budgetBlown = true
	} else {
		remaining = len(req.NodeIDs)
	}
	st.rounds += remaining
	round := st.rounds
	s.mu.Unlock()

	nodes := make([]exploredNode, 0, remaining)
	for _, id := range req.NodeIDs[:remaining] {
		nodes = append(nodes, s.exploreNode(g, id))
	}
	exploreWriteJSON(w, http.StatusOK, exploreResponse{
		Round:          round,
		BudgetExceeded: round >= s.cfg.MaxRounds,
		Nodes:          nodes,
	})
}

// exploreNode assembles one served node: body (truncated to MaxNodeChars)
// plus inline neighbors. Staging segments carry a body but no neighbors.
// Neighbor ordering and the per-node cap reuse expandCandidates, and the
// caller's graph view is reapplied to every offered neighbor.
func (s *ExploreToolServer) exploreNode(g *Graph, id string) exploredNode {
	if IsStagingID(id) {
		segID := strings.TrimPrefix(id, stagingDocPrefix)
		body, _ := s.store.ReadStagingSegment(segID)
		node := exploredNode{NodeID: id, Level: -1, Staging: true, Body: string(body)}
		return truncateExploreBody(&node, s.cfg.MaxNodeChars)
	}
	n := g.Node(id)
	node := exploredNode{
		NodeID:          n.NodeID,
		Level:           n.Level,
		EpistemicStatus: n.Epistemic,
		Tags:            n.Tags,
		Body:            n.Body,
	}
	node.Neighbors = s.expandCandidates(g, n, "")
	return truncateExploreBody(&node, s.cfg.MaxNodeChars)
}

func truncateExploreBody(node *exploredNode, max int) exploredNode {
	if len(node.Body) > max {
		node.Body = node.Body[:max]
		node.Truncated = true
	}
	return *node
}

// expandCandidateRef identifies one expand candidate without rendering it. The
// shared construction keeps /explore and backtest L3 on the same candidate
// priority, visibility and cap semantics.
type expandCandidateRef struct {
	NodeID string
	Via    string
}

// expandCandidateRefs builds the capped candidate list returned by /explore.
// It is pure over its graph and retriever inputs; allow may be nil when no
// graph view applies.
func expandCandidateRefs(g *Graph, node *Node, retr *HybridRetriever, maxExpand int, relationFilter string, allow func(*Node) bool) []expandCandidateRef {
	if node == nil || maxExpand <= 0 {
		return nil
	}
	seen := map[string]bool{node.NodeID: true}
	var out, demoted []expandCandidateRef
	add := func(id, via string, demote bool) {
		if seen[id] || id == "" {
			return
		}
		seen[id] = true
		if n := g.Node(id); n != nil && allow != nil && !allow(n) {
			return
		}
		c := expandCandidateRef{NodeID: id, Via: via}
		if demote {
			demoted = append(demoted, c)
		} else {
			out = append(out, c)
		}
	}

	// 1. Hierarchy parents/children.
	parents, children, _, relEdges := g.Neighbors(node.NodeID)
	for _, p := range parents {
		add(p.NodeID, "parent", false)
	}
	for _, c := range children {
		add(c.NodeID, "child", false)
	}

	// 2. entity_refs co-occurrence.
	for _, ref := range node.EntityRefs {
		for _, id := range g.EntityNodes(ref) {
			add(id, "entity", false)
		}
	}

	// 3. Typed relation neighbors (either direction, node targets only).
	for _, e := range relEdges {
		if relationFilter != "" && e.Type != relationFilter {
			continue
		}
		var targetID string
		switch {
		case e.From == node.NodeID:
			if e.IsEdgeRef() {
				continue // edge-pointing evidence is not expandable to a node
			}
			targetID = e.To
		case !e.IsEdgeRef() && e.To == node.NodeID:
			targetID = e.From
		default:
			continue
		}
		demote := e.Type != EdgeTypeEvidenceFor && absInt(e.LevelDelta) > 1
		add(targetID, e.Type, demote)
	}

	// 4. Embedding neighbors (skipped without an embedder).
	if retr != nil {
		for _, hit := range retr.vectorNeighbors(node.NodeID, maxExpand) {
			add(hit.ID, "embedding", false)
		}
	}

	candidates := append(out, demoted...)
	if len(candidates) > maxExpand {
		candidates = candidates[:maxExpand]
	}
	return candidates
}

// expandCandidates renders the shared candidate construction for /explore.
func (s *ExploreToolServer) expandCandidates(g *Graph, node *Node, relationFilter string) []expandCandidate {
	refs := expandCandidateRefs(g, node, s.retr, s.cfg.MaxExpandPerRound, relationFilter, s.viewAllows)
	out := make([]expandCandidate, 0, len(refs))
	for _, ref := range refs {
		c := expandCandidate{NodeID: ref.NodeID, Via: ref.Via, Level: -1, Snippet: s.snippet(g, ref.NodeID)}
		if n := g.Node(ref.NodeID); n != nil {
			c.Level = n.Level
		}
		out = append(out, c)
	}
	return out
}

// snippet returns a bounded body preview for a graph node or staging id.
func (s *ExploreToolServer) snippet(g *Graph, id string) string {
	var body string
	if IsStagingID(id) {
		if b, err := s.store.ReadStagingSegment(strings.TrimPrefix(id, stagingDocPrefix)); err == nil {
			body = string(b)
		}
	} else if n := g.Node(id); n != nil {
		body = n.Body
	}
	if len(body) > expandSnippetChars {
		body = body[:expandSnippetChars] + "..."
	}
	return body
}

// ---------------------------------------------------------------------------
// POST /submit
// ---------------------------------------------------------------------------

type submitRequest struct {
	TrajectoryID string   `json:"trajectory_id"`
	Found        bool     `json:"found"`
	Summary      string   `json:"summary"`
	NodeIDs      []string `json:"node_ids"`
}

type submitResponse struct {
	Status   string   `json:"status"`
	NodeIDs  []string `json:"node_ids"`
	Warnings []string `json:"warnings,omitempty"`
}

func (s *ExploreToolServer) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req submitRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_TRAJECTORY_ID", "trajectory_id is required")
		return
	}
	g, err := s.loadGraph()
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
		return
	}

	// Validate cited ids: unknown ids are dropped from the stored submission
	// and reported back as warnings.
	kept := make([]string, 0, len(req.NodeIDs))
	var warnings []string
	for _, id := range req.NodeIDs {
		switch {
		case IsStagingID(id):
			if _, err := s.store.ReadStagingSegment(strings.TrimPrefix(id, stagingDocPrefix)); err != nil {
				warnings = append(warnings, fmt.Sprintf("unknown staging id %q dropped", id))
				continue
			}
			kept = append(kept, id)
		case g.Node(id) != nil:
			kept = append(kept, id)
		default:
			warnings = append(warnings, fmt.Sprintf("unknown node id %q dropped", id))
		}
	}

	s.mu.Lock()
	st := s.stateLocked(req.TrajectoryID)
	// Budget-blown trajectories (design Q15/A6): the submission is recorded
	// for audit, but Found is forced to false no matter what the agent
	// reported — a trajectory that blew the exploration budget cannot claim
	// a find.
	found := req.Found
	if st.budgetBlown && found {
		found = false
		warnings = append(warnings, "exploration budget exceeded: submission recorded with found=false")
	}
	st.submission = &submitRecord{
		Found:    found,
		Summary:  req.Summary,
		NodeIDs:  kept,
		Warnings: warnings,
	}
	s.mu.Unlock()

	exploreWriteJSON(w, http.StatusOK, submitResponse{Status: "recorded", NodeIDs: kept, Warnings: warnings})
}

// ---------------------------------------------------------------------------
// retriever vector neighbors (embedding channel for /explore neighbors)
// ---------------------------------------------------------------------------

// vectorNeighbors returns up to topN doc ids most similar to the given doc id
// by cosine similarity over the in-memory vector map. It returns nil when no
// embedder is configured or the doc has no vector (callers skip the embedding
// channel in that case, per design §5.2 "embedding neighbors are dynamically
// computed").
func (r *HybridRetriever) vectorNeighbors(id string, topN int) []ScoredDoc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.emb == nil || topN <= 0 {
		return nil
	}
	base, ok := r.vecs[id]
	if !ok {
		return nil
	}
	out := make([]ScoredDoc, 0, len(r.vecs))
	for other, vec := range r.vecs {
		if other == id {
			continue
		}
		out = append(out, ScoredDoc{ID: other, Score: cosineSimilarity(base, vec)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

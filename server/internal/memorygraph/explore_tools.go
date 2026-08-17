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

// expandSnippetChars caps the candidate snippet returned by /expand.
const expandSnippetChars = 200

// ExploreToolServer exposes the explore-agent graph operations over a
// loopback HTTP server, mirroring the diagnosis tool server pattern. One
// server serves one Explore call; it is started with Start, handed to the
// agent CLIs via the prompt, and torn down with Shutdown.
//
// Endpoints (all require "Authorization: Bearer <token>"):
//
//	POST /view    {trajectory_id, node_id}             -> node body + frontmatter subset
//	POST /expand  {trajectory_id, node_id, relation?}  -> ordered neighbor candidates;
//	    beyond the round budget: 200 {"budget_exceeded":true,"candidates":[]} (Q15/A6)
//	POST /submit  {trajectory_id, found, summary, node_ids} -> record the final answer
type ExploreToolServer struct {
	store *Store
	retr  *HybridRetriever // may be nil; embedding neighbors are then skipped
	cfg   ExploreConfig
	// version is the graph version this server is pinned to (design R5/R12):
	// /view, /expand and /submit read this version's dir for the whole
	// Explore call and never re-resolve the current pointer.
	version int

	httpServer  *http.Server
	baseURL     string
	bearerToken string

	mu           sync.Mutex
	trajectories map[string]*trajectoryState
}

// trajectoryState is the per-trajectory server-side accounting. The rounds
// counter is the authoritative exploration-round count (one served /expand
// call = one round); budgetBlown is set when the trajectory kept calling
// /expand after reaching MaxRounds (design Q15/A6: the budget is enforced
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
	mux.HandleFunc("POST /view", s.handleView)
	mux.HandleFunc("POST /expand", s.handleExpand)
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
// POST /view
// ---------------------------------------------------------------------------

type viewRequest struct {
	TrajectoryID string `json:"trajectory_id"`
	NodeID       string `json:"node_id"`
}

type viewResponse struct {
	NodeID          string   `json:"node_id"`
	Level           int      `json:"level"` // -1 for staging segments
	EpistemicStatus string   `json:"epistemic_status,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Body            string   `json:"body"`
	Truncated       bool     `json:"truncated"`
	Staging         bool     `json:"staging,omitempty"`
}

func (s *ExploreToolServer) handleView(w http.ResponseWriter, r *http.Request) {
	var req viewRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || strings.TrimSpace(req.NodeID) == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id and node_id are required")
		return
	}
	s.mu.Lock()
	s.stateLocked(req.TrajectoryID)
	s.mu.Unlock()

	var resp viewResponse
	if IsStagingID(req.NodeID) {
		segID := strings.TrimPrefix(req.NodeID, stagingDocPrefix)
		body, err := s.store.ReadStagingSegment(segID)
		if err != nil {
			exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "staging segment not found")
			return
		}
		resp = viewResponse{NodeID: req.NodeID, Level: -1, Staging: true, Body: string(body)}
	} else {
		g, err := s.loadGraph()
		if err != nil {
			exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
			return
		}
		n := g.Node(req.NodeID)
		// A node outside the caller's graph view returns the same not-found
		// shape as a missing node: fail closed, no existence leak (spec §5).
		if n == nil || !s.viewAllows(n) {
			exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		resp = viewResponse{
			NodeID:          n.NodeID,
			Level:           n.Level,
			EpistemicStatus: n.Epistemic,
			Tags:            n.Tags,
			Body:            n.Body,
		}
	}
	if len(resp.Body) > s.cfg.MaxNodeChars {
		resp.Body = resp.Body[:s.cfg.MaxNodeChars]
		resp.Truncated = true
	}
	exploreWriteJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /expand
// ---------------------------------------------------------------------------

type expandRequest struct {
	TrajectoryID string `json:"trajectory_id"`
	NodeID       string `json:"node_id"`
	Relation     string `json:"relation,omitempty"`
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

type expandResponse struct {
	Round          int               `json:"round"`
	BudgetExceeded bool              `json:"budget_exceeded"`
	Candidates     []expandCandidate `json:"candidates"`
}

func (s *ExploreToolServer) handleExpand(w http.ResponseWriter, r *http.Request) {
	var req expandRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || strings.TrimSpace(req.NodeID) == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id and node_id are required")
		return
	}
	if IsStagingID(req.NodeID) {
		exploreWriteError(w, http.StatusBadRequest, "STAGING_NOT_EXPANDABLE", "staging segments have no graph neighbors")
		return
	}

	g, err := s.loadGraph()
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
		return
	}
	node := g.Node(req.NodeID)
	// Same fail-closed not-found shape as /view for out-of-view anchors.
	if node == nil || !s.viewAllows(node) {
		exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
		return
	}

	// One served /expand call consumes one exploration round
	// (server-authoritative). Budget enforcement (design Q15/A6): once the
	// round counter reaches MaxRounds, subsequent /expand calls are rejected
	// with HTTP 200 and {"budget_exceeded":true,"candidates":[]} — a 200,
	// not a 429, so curl-driven agents parse one response shape — and the
	// trajectory is marked budget-blown, which later forces its /submit to
	// Found=false.
	s.mu.Lock()
	st := s.stateLocked(req.TrajectoryID)
	if st.rounds >= s.cfg.MaxRounds {
		st.budgetBlown = true
		round := st.rounds
		s.mu.Unlock()
		exploreWriteJSON(w, http.StatusOK, expandResponse{
			Round:          round,
			BudgetExceeded: true,
			Candidates:     []expandCandidate{},
		})
		return
	}
	st.rounds++
	round := st.rounds
	s.mu.Unlock()

	candidates := s.expandCandidates(g, node, req.Relation)
	if len(candidates) > s.cfg.MaxExpandPerRound {
		candidates = candidates[:s.cfg.MaxExpandPerRound]
	}
	exploreWriteJSON(w, http.StatusOK, expandResponse{
		Round:          round,
		BudgetExceeded: round >= s.cfg.MaxRounds,
		Candidates:     candidates,
	})
}

// expandCandidates orders neighbors by the design §5.2 priority: hierarchy
// parents/children (summarizes) first, then entity_refs co-occurrence, then
// typed relation neighbors, then embedding neighbors. Cross-level relation
// edges with |LevelDelta| > 1 are demoted to the end, except evidence_for
// which is never demoted (Q5).
func (s *ExploreToolServer) expandCandidates(g *Graph, node *Node, relationFilter string) []expandCandidate {
	seen := map[string]bool{node.NodeID: true}
	var out, demoted []expandCandidate
	add := func(id, via string, demote bool) {
		if seen[id] || id == "" {
			return
		}
		seen[id] = true
		// Reapply the graph view on every offered neighbor (spec §5): an
		// edge must never surface a node the caller may not see. Staging
		// docs have no graph node and pass through (scope-resolved upstream).
		if n := g.Node(id); n != nil && !s.viewAllows(n) {
			return
		}
		c := expandCandidate{NodeID: id, Via: via, Level: -1, Snippet: s.snippet(g, id)}
		if n := g.Node(id); n != nil {
			c.Level = n.Level
		}
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
	if s.retr != nil {
		for _, hit := range s.retr.vectorNeighbors(node.NodeID, s.cfg.MaxExpandPerRound) {
			add(hit.ID, "embedding", false)
		}
	}

	return append(out, demoted...)
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
// retriever vector neighbors (embedding channel for /expand)
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

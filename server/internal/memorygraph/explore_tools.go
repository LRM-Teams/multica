package memorygraph

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Traversal-state failure codes (spec §4, A2/A3/A24). Handlers map them onto
// HTTP statuses; the in-memory and SQL-backed stores share the vocabulary.
var (
	ErrTrajectoryNotFound   = errors.New("trajectory not found")
	ErrExpansionNotFound    = errors.New("expansion batch not found")
	ErrViewNotInBatch       = errors.New("viewed node is not a candidate of the expansion batch")
	ErrViewQuotaExceeded    = errors.New("expansion batch distinct-view quota exhausted")
	ErrAnchorNotViewed      = errors.New("expansion anchor was never viewed by the trajectory")
	ErrRequestKeyConflict   = errors.New("expand request key replayed with different parameters")
	ErrSubmitConflict       = errors.New("conflicting replay of the immutable submission")
	ErrSubmitNodeNotViewed  = errors.New("submitted node was never viewed by the trajectory")
	ErrSubmitDuplicateNodes = errors.New("submitted node ids contain duplicates")
)

// expansionBatch is one persisted /expand (or round-0 seed) batch.
type expansionBatch struct {
	ExpansionID string
	Round       int
	Anchor      string
	Relation    string
	RequestKey  string
	Candidates  []expandCandidate
}

// expandOutcome is the result of TraversalStore.Expand.
type expandOutcome struct {
	Batch          expansionBatch
	BudgetExceeded bool
}

// TraversalStore is the server-side per-trajectory exploration state (spec
// §4): batch membership, distinct-view accounting, viewed-anchor eligibility,
// request-key idempotency, and the single immutable submission. The
// in-memory implementation serves tests and backtests; the recall path uses
// the SQL-backed store over the durable ledger.
type TraversalStore interface {
	// RegisterTrajectory creates traversal state with the round-0 seed batch
	// and returns the seed expansion id. Re-registering the same trajectory
	// returns the existing seed expansion id.
	RegisterTrajectory(ctx context.Context, trajectoryID string, seedCandidateIDs []string, viewQuota int) (string, error)
	// Expand validates the request key, anchor eligibility and the round
	// budget, persists a new batch with gen()'s candidates, and returns it.
	// Replaying a request key returns the original batch without consuming a
	// round. Expanding past the budget marks the trajectory budget-blown and
	// returns an empty batch with BudgetExceeded set (not an error).
	Expand(ctx context.Context, trajectoryID, anchor, relation, requestKey string, maxRounds int, gen func() ([]expandCandidate, error)) (expandOutcome, error)
	// RecordView validates batch membership and enforces the per-batch
	// distinct-view quota, then commits the view only when load succeeds —
	// a failed load releases the reservation. Re-viewing the same node in
	// the same batch is idempotent and consumes no slot.
	RecordView(ctx context.Context, trajectoryID, expansionID, nodeID string, load func() error) error
	// Submit records the single immutable submission. An identical replay
	// returns the stored record; a conflicting replay is rejected. A
	// budget-blown trajectory's record is forced to Found=false.
	Submit(ctx context.Context, trajectoryID string, found bool, summary string, nodeIDs []string) (submitRecord, error)
	// Rounds is the server-counted exploration-round total.
	Rounds(ctx context.Context, trajectoryID string) (int, error)
	// BudgetBlown reports whether the trajectory expanded past its budget.
	BudgetBlown(ctx context.Context, trajectoryID string) (bool, error)
	// Submission returns the recorded submission, or nil.
	Submission(ctx context.Context, trajectoryID string) (*submitRecord, error)
	// Viewed returns the trajectory's successfully served node ids in
	// observed order.
	Viewed(ctx context.Context, trajectoryID string) ([]string, error)
	// Serve atomically accounts for a /explore request. It returns the prefix
	// that fits the node budget; only an over-budget request marks the run blown.
	Serve(ctx context.Context, trajectoryID string, nodeIDs []string, maxRounds int) (served, rounds int, budgetExceeded bool, err error)
}

// ExploreToolServer exposes the explore-agent graph operations over a
// loopback HTTP server, mirroring the diagnosis tool server pattern. One
// server serves one Explore call; it is started with Start, handed to the
// agent CLIs via the prompt, and torn down with Shutdown.
//
// Endpoints (all require "Authorization: Bearer <token>"):
//

// AgentToolOperationReservation is the durable idempotency result for one
// native Agent Graph operation.
type AgentToolOperationReservation struct {
	OperationID string
	Replay      bool
	Pending     bool
	Response    json.RawMessage
	Error       string
}

// AgentToolLedger binds one ExploreToolServer to one fenced PostgreSQL Agent
// run. A nil ledger preserves the in-memory recall/backtest behavior.
type AgentToolLedger interface {
	TrajectoryID() string
	Reserve(context.Context, string, string, json.RawMessage) (AgentToolOperationReservation, error)
	Complete(context.Context, string, json.RawMessage, string) error
	RecordViewed(context.Context, []string) error
	Finish(context.Context, string, string, json.RawMessage, []Citation, json.RawMessage) error
}

// AgentToolQuota is an optional durable pre-reservation quota gate. Agent mode
// ledgers implement it; recall/backtest ledgers remain unaffected.
type AgentToolQuota interface {
	ValidateOperation(context.Context, string, string, json.RawMessage) error
}

// AgentToolProgress optionally exposes durable trajectory progress so a
// stateless gateway response does not reset round accounting per HTTP request.
type AgentToolProgress interface {
	ExplorationRounds(context.Context) (int, error)
}

// POST /view    {trajectory_id, expansion_id, node_id} -> node body + frontmatter subset
// POST /expand  {trajectory_id, node_id, relation?, request_key} -> ordered candidates
//
//	plus a unique expansion_id; beyond the round budget: 200
//	{"budget_exceeded":true,"candidates":[]} (Q15/A6)
//
// POST /submit  {trajectory_id, found, summary, node_ids} -> the single
//
//	immutable final answer (identical replay idempotent, conflicting rejected)
type ExploreToolServer struct {
	store *Store
	retr  *HybridRetriever // may be nil; embedding neighbors are then skipped
	cfg   ExploreConfig
	// version is the graph version this server is pinned to (design R5/R12):
	// /view, /expand and /submit read this version's dir for the whole
	// Explore call and never re-resolve the current pointer.
	version int
	state   TraversalStore
	ledger  AgentToolLedger

	httpServer  *http.Server
	baseURL     string
	bearerToken string
	terminalMu  sync.Mutex
	checkpoints map[string]json.RawMessage
	queries     map[string]string
	startByKey  map[string]memoryGraphStartResponse
}

// submitRecord is the validated /submit payload kept server-side.
type submitRecord struct {
	Found    bool     `json:"found"`
	Summary  string   `json:"summary"`
	NodeIDs  []string `json:"node_ids"`
	Warnings []string `json:"warnings,omitempty"`
}

// NewExploreToolServer creates the server with a cryptographically random
// 32-byte bearer token, pinned to graph version v, with in-memory traversal
// state. cfg is normalized with DefaultExploreConfig values for zero fields.
func NewExploreToolServer(store *Store, retr *HybridRetriever, cfg ExploreConfig, version int) (*ExploreToolServer, error) {
	return NewExploreToolServerWithState(store, retr, cfg, version, nil)
}

// NewExploreToolServerWithState is NewExploreToolServer with an explicit
// traversal-state store; a nil state store falls back to the in-memory
// implementation.
func NewExploreToolServerWithState(store *Store, retr *HybridRetriever, cfg ExploreConfig, version int, state TraversalStore) (*ExploreToolServer, error) {
	return NewExploreToolServerWithAgentLedger(store, retr, cfg, version, state, nil)
}

// NewExploreToolServerWithAgentLedger binds native tools to one fenced durable
// Agent run while retaining an injectable traversal store for tests.
func NewExploreToolServerWithAgentLedger(store *Store, retr *HybridRetriever, cfg ExploreConfig, version int, state TraversalStore, ledger AgentToolLedger) (*ExploreToolServer, error) {
	cfg = cfg.normalized()
	if state == nil {
		state = newInMemoryTraversalStore()
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("explore tool server: bearer token: %w", err)
	}
	s := &ExploreToolServer{
		store:       store,
		retr:        retr,
		cfg:         cfg,
		version:     version,
		state:       state,
		ledger:      ledger,
		bearerToken: fmt.Sprintf("%x", token),
		checkpoints: make(map[string]json.RawMessage),
		queries:     make(map[string]string),
		startByKey:  make(map[string]memoryGraphStartResponse),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /start", s.handleStart)
	mux.HandleFunc("POST /explore", s.handleExplore)
	mux.HandleFunc("POST /redirect", s.handleRedirect)
	mux.HandleFunc("POST /submit", s.handleSubmit)
	mux.HandleFunc("POST /checkpoint", s.handleCheckpoint)

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

// ServeAuthorizedHTTP executes one gateway request against the five-operation
// mux. Authentication to the public Agent API has already happened; this method
// replaces it with the execution-local bearer without exposing that secret to
// the resident model or daemon.
func (s *ExploreToolServer) ServeAuthorizedHTTP(w http.ResponseWriter, r *http.Request, operation string) {
	operation = strings.TrimSpace(operation)
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = "/" + operation
	urlCopy.RawPath = ""
	clone.URL = &urlCopy
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+s.bearerToken)
	s.httpServer.Handler.ServeHTTP(w, clone)
}

// RegisterTrajectory creates the trajectory's traversal state with its
// round-0 seed batch and returns the seed expansion id the agent cites in
// /view calls.
func (s *ExploreToolServer) RegisterTrajectory(ctx context.Context, trajectoryID string, seedCandidateIDs []string) (string, error) {
	return s.state.RegisterTrajectory(ctx, trajectoryID, seedCandidateIDs, s.cfg.ViewsPerExpansion)
}

// trajectoryRounds returns the server-side round count for a trajectory

func (s *ExploreToolServer) reserveAgentOperation(ctx context.Context, operation, key string, request any) (AgentToolOperationReservation, bool, error) {
	if s.ledger == nil {
		return AgentToolOperationReservation{}, false, nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return AgentToolOperationReservation{}, true, errors.New("idempotency_key is required for Agent tools")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return AgentToolOperationReservation{}, true, err
	}
	reservation, err := s.ledger.Reserve(ctx, key, operation, raw)
	if err != nil {
		return reservation, true, err
	}
	if !reservation.Replay && !reservation.Pending {
		if quota, ok := s.ledger.(AgentToolQuota); ok {
			if err := quota.ValidateOperation(ctx, operation, key, raw); err != nil {
				_ = s.ledger.Complete(ctx, reservation.OperationID, json.RawMessage(`{}`), err.Error())
				return reservation, true, err
			}
		}
	}
	return reservation, true, nil
}

func (s *ExploreToolServer) writeAgentOperationReplay(w http.ResponseWriter, reservation AgentToolOperationReservation) bool {
	if reservation.Pending {
		exploreWriteError(w, http.StatusConflict, "OPERATION_PENDING", "the idempotent operation is still pending")
		return true
	}
	if !reservation.Replay {
		return false
	}
	if reservation.Error != "" {
		exploreWriteError(w, http.StatusConflict, "OPERATION_FAILED", reservation.Error)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reservation.Response)
	return true
}

func (s *ExploreToolServer) completeAgentOperation(ctx context.Context, reservation AgentToolOperationReservation, response any, operationErr string) error {
	if s.ledger == nil || reservation.OperationID == "" {
		return nil
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return s.ledger.Complete(ctx, reservation.OperationID, raw, operationErr)
}

// (zero for unknown trajectories).
func (s *ExploreToolServer) trajectoryRounds(trajectoryID string) int {
	n, err := s.state.Rounds(context.Background(), trajectoryID)
	if err != nil {
		return 0
	}
	return n
}

// trajectoryBudgetBlown reports whether the trajectory exceeded the
// exploration-round budget (design Q15/A6). A budget-blown trajectory's
// submission is forced to Found=false server-side.
func (s *ExploreToolServer) trajectoryBudgetBlown(trajectoryID string) bool {
	blown, err := s.state.BudgetBlown(context.Background(), trajectoryID)
	return err == nil && blown
}

// trajectorySubmission returns the recorded submission for a trajectory, or
// nil when the trajectory never called /submit.
func (s *ExploreToolServer) trajectorySubmission(trajectoryID string) *submitRecord {
	sub, err := s.state.Submission(context.Background(), trajectoryID)
	if err != nil {
		return nil
	}
	return sub
}

// trajectoryViewed returns the trajectory's viewed node ids in observed
// order.
func (s *ExploreToolServer) trajectoryViewed(trajectoryID string) []string {
	viewed, err := s.state.Viewed(context.Background(), trajectoryID)
	if err != nil {
		return nil
	}
	return viewed
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

// writeTraversalError maps the traversal-state failure vocabulary onto HTTP.
func writeTraversalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrTrajectoryNotFound):
		exploreWriteError(w, http.StatusNotFound, "TRAJECTORY_NOT_FOUND", err.Error())
	case errors.Is(err, ErrExpansionNotFound):
		exploreWriteError(w, http.StatusNotFound, "EXPANSION_NOT_FOUND", err.Error())
	case errors.Is(err, ErrViewNotInBatch):
		exploreWriteError(w, http.StatusConflict, "VIEW_NOT_IN_BATCH", err.Error())
	case errors.Is(err, ErrViewQuotaExceeded):
		exploreWriteError(w, http.StatusConflict, "VIEW_QUOTA_EXCEEDED", err.Error())
	case errors.Is(err, ErrAnchorNotViewed):
		exploreWriteError(w, http.StatusConflict, "ANCHOR_NOT_VIEWED", err.Error())
	case errors.Is(err, ErrRequestKeyConflict):
		exploreWriteError(w, http.StatusConflict, "REQUEST_KEY_CONFLICT", err.Error())
	case errors.Is(err, ErrSubmitConflict):
		exploreWriteError(w, http.StatusConflict, "SUBMIT_CONFLICT", err.Error())
	case errors.Is(err, ErrSubmitNodeNotViewed):
		exploreWriteError(w, http.StatusConflict, "SUBMIT_NODE_NOT_VIEWED", err.Error())
	case errors.Is(err, ErrSubmitDuplicateNodes):
		exploreWriteError(w, http.StatusBadRequest, "DUPLICATE_NODE_IDS", err.Error())
	default:
		exploreWriteError(w, http.StatusInternalServerError, "TRAVERSAL_STATE_ERROR", err.Error())
	}
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
	ExpansionID  string `json:"expansion_id"`
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
	if strings.TrimSpace(req.TrajectoryID) == "" || strings.TrimSpace(req.ExpansionID) == "" || strings.TrimSpace(req.NodeID) == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id, expansion_id and node_id are required")
		return
	}

	// loadBody resolves the node body on the pinned version. A node outside
	// the caller's graph view returns the same not-found shape as a missing
	// node: fail closed, no existence leak (spec §5).
	var resp viewResponse
	loadBody := func() error {
		if IsStagingID(req.NodeID) {
			segID := strings.TrimPrefix(req.NodeID, stagingDocPrefix)
			body, err := s.store.ReadStagingSegment(segID)
			if err != nil {
				return errViewNodeNotFound
			}
			resp = viewResponse{NodeID: req.NodeID, Level: -1, Staging: true, Body: string(body)}
			return nil
		}
		g, err := s.loadGraph()
		if err != nil {
			return err
		}
		n := g.Node(req.NodeID)
		if n == nil || !s.viewAllows(n) {
			return errViewNodeNotFound
		}
		resp = viewResponse{
			NodeID:          n.NodeID,
			Level:           n.Level,
			EpistemicStatus: n.Epistemic,
			Tags:            n.Tags,
			Body:            n.Body,
		}
		return nil
	}

	// The store validates batch membership and the distinct-view quota first,
	// then commits the view only when the load succeeds (A2/A24).
	err := s.state.RecordView(r.Context(), req.TrajectoryID, req.ExpansionID, req.NodeID, loadBody)
	switch {
	case err == nil:
	case errors.Is(err, errViewNodeNotFound):
		exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
		return
	default:
		writeTraversalError(w, err)
		return
	}
	if len(resp.Body) > s.cfg.MaxNodeChars {
		resp.Body = resp.Body[:s.cfg.MaxNodeChars]
		resp.Truncated = true
	}
	exploreWriteJSON(w, http.StatusOK, resp)
}

// errViewNodeNotFound marks a failed node load inside RecordView's load
// callback so the handler can answer 404 while the store releases the
// reservation.
var errViewNodeNotFound = errors.New("node not found")

// ---------------------------------------------------------------------------
// POST /expand
// ---------------------------------------------------------------------------

type expandRequest struct {
	TrajectoryID string `json:"trajectory_id"`
	NodeID       string `json:"node_id"`
	Relation     string `json:"relation,omitempty"`
	RequestKey   string `json:"request_key"`
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

// memoryGraphStartRequest begins one server-pinned trajectory. Scope, graph
// owner, and version are intentionally absent: they belong to this delegated
// server instance and can never be supplied by the model.
type memoryGraphStartRequest struct {
	Query          string `json:"query"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type memoryGraphStartResponse struct {
	TrajectoryID string         `json:"trajectory_id"`
	GraphVersion int            `json:"graph_version"`
	Query        string         `json:"query"`
	Nodes        []exploredNode `json:"nodes"`
}

func (s *ExploreToolServer) handleStart(w http.ResponseWriter, r *http.Request) {
	var req memoryGraphStartRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Query == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "query is required")
		return
	}
	reservation, durable, err := s.reserveAgentOperation(r.Context(), "start", req.IdempotencyKey, req)
	if err != nil {
		status := http.StatusConflict
		if strings.Contains(err.Error(), "idempotency_key is required") {
			status = http.StatusBadRequest
		}
		exploreWriteError(w, status, "OPERATION_RESERVATION_FAILED", err.Error())
		return
	}
	if durable && s.writeAgentOperationReplay(w, reservation) {
		return
	}
	if req.IdempotencyKey != "" && !durable {
		s.terminalMu.Lock()
		existing, ok := s.startByKey[req.IdempotencyKey]
		s.terminalMu.Unlock()
		if ok {
			if existing.Query != req.Query {
				exploreWriteError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency_key was already used with another query")
				return
			}
			exploreWriteJSON(w, http.StatusOK, existing)
			return
		}
	}
	if s.retr == nil {
		exploreWriteError(w, http.StatusServiceUnavailable, "RETRIEVER_UNAVAILABLE", "hybrid retrieval is unavailable")
		return
	}
	hits, err := s.retr.Search(r.Context(), req.Query)
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "RETRIEVAL_ERROR", err.Error())
		return
	}
	ids := make([]string, 0, min(4, len(hits)))
	for _, hit := range hits {
		if len(ids) == 4 {
			break
		}
		if s.retr.AllowsNodeID(hit.ID) {
			ids = append(ids, hit.ID)
		}
	}
	trajectoryID := ""
	if s.ledger != nil {
		trajectoryID = strings.TrimSpace(s.ledger.TrajectoryID())
		if trajectoryID == "" {
			exploreWriteError(w, http.StatusInternalServerError, "TRAJECTORY_ERROR", "durable trajectory is unavailable")
			return
		}
	} else {
		idBytes := make([]byte, 16)
		if _, err := rand.Read(idBytes); err != nil {
			exploreWriteError(w, http.StatusInternalServerError, "TRAJECTORY_ERROR", "cannot allocate trajectory")
			return
		}
		trajectoryID = hex.EncodeToString(idBytes)
	}
	if _, err := s.state.RegisterTrajectory(r.Context(), trajectoryID, ids, s.cfg.ViewsPerExpansion); err != nil {
		writeTraversalError(w, err)
		return
	}
	g, err := s.loadGraph()
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
		return
	}
	nodes := make([]exploredNode, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, s.exploreNode(g, id))
	}
	response := memoryGraphStartResponse{TrajectoryID: trajectoryID, GraphVersion: s.version, Query: req.Query, Nodes: nodes}
	if err := s.completeAgentOperation(r.Context(), reservation, response, ""); err != nil {
		exploreWriteError(w, http.StatusConflict, "OPERATION_COMPLETION_FAILED", err.Error())
		return
	}
	s.terminalMu.Lock()
	s.queries[trajectoryID] = req.Query
	if req.IdempotencyKey != "" && !durable {
		s.startByKey[req.IdempotencyKey] = response
	}
	s.terminalMu.Unlock()
	exploreWriteJSON(w, http.StatusOK, response)
}

type memoryGraphRedirectRequest struct {
	TrajectoryID      string `json:"trajectory_id"`
	Query             string `json:"query"`
	SteeringMessageID string `json:"steering_message_id"`
	IdempotencyKey    string `json:"idempotency_key,omitempty"`
}

func (s *ExploreToolServer) handleRedirect(w http.ResponseWriter, r *http.Request) {
	var req memoryGraphRedirectRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || strings.TrimSpace(req.Query) == "" || strings.TrimSpace(req.SteeringMessageID) == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id, query and steering_message_id are required")
		return
	}
	if s.ledger != nil && req.TrajectoryID != s.ledger.TrajectoryID() {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_FENCED", "trajectory does not match the active run")
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = "redirect:" + strings.TrimSpace(req.SteeringMessageID)
	}
	reservation, durable, err := s.reserveAgentOperation(r.Context(), "redirect", req.IdempotencyKey, req)
	if err != nil {
		exploreWriteError(w, http.StatusConflict, "OPERATION_RESERVATION_FAILED", err.Error())
		return
	}
	if durable && s.writeAgentOperationReplay(w, reservation) {
		return
	}
	if s.ledger == nil {
		if _, err := s.state.Rounds(r.Context(), req.TrajectoryID); err != nil {
			writeTraversalError(w, err)
			return
		}
	}
	s.terminalMu.Lock()
	_, known := s.queries[req.TrajectoryID]
	_, checkpointed := s.checkpoints[req.TrajectoryID]
	s.terminalMu.Unlock()
	if !known && s.ledger == nil {
		writeTraversalError(w, ErrTrajectoryNotFound)
		return
	}
	if checkpointed || s.trajectorySubmission(req.TrajectoryID) != nil {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_TERMINAL", "trajectory is already terminal")
		return
	}
	hits, err := s.retr.Search(r.Context(), strings.TrimSpace(req.Query))
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "RETRIEVAL_ERROR", err.Error())
		return
	}
	g, err := s.loadGraph()
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
		return
	}
	nodes := make([]exploredNode, 0, min(4, len(hits)))
	for _, hit := range hits {
		if len(nodes) == 4 {
			break
		}
		if s.retr.AllowsNodeID(hit.ID) {
			nodes = append(nodes, s.exploreNode(g, hit.ID))
		}
	}
	response := memoryGraphStartResponse{TrajectoryID: req.TrajectoryID, GraphVersion: s.version, Query: strings.TrimSpace(req.Query), Nodes: nodes}
	if err := s.completeAgentOperation(r.Context(), reservation, response, ""); err != nil {
		exploreWriteError(w, http.StatusConflict, "OPERATION_COMPLETION_FAILED", err.Error())
		return
	}
	exploreWriteJSON(w, http.StatusOK, response)
}

type memoryGraphCheckpointRequest struct {
	TrajectoryID   string          `json:"trajectory_id"`
	State          json.RawMessage `json:"state"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

func (s *ExploreToolServer) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	var req memoryGraphCheckpointRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || len(req.State) == 0 || !json.Valid(req.State) {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id and valid state are required")
		return
	}
	if s.ledger != nil && req.TrajectoryID != s.ledger.TrajectoryID() {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_FENCED", "trajectory does not match the active run")
		return
	}
	reservation, durable, err := s.reserveAgentOperation(r.Context(), "checkpoint", req.IdempotencyKey, req)
	if err != nil {
		exploreWriteError(w, http.StatusBadRequest, "OPERATION_RESERVATION_FAILED", err.Error())
		return
	}
	if durable && s.writeAgentOperationReplay(w, reservation) {
		return
	}
	response := map[string]any{"trajectory_id": req.TrajectoryID, "status": "checkpointed"}
	if s.ledger != nil {
		raw, _ := json.Marshal(response)
		if err := s.ledger.Finish(r.Context(), reservation.OperationID, "checkpointed", req.State, nil, raw); err != nil {
			exploreWriteError(w, http.StatusConflict, "CHECKPOINT_FAILED", err.Error())
			return
		}
		exploreWriteJSON(w, http.StatusOK, response)
		return
	}
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	if _, ok := s.queries[req.TrajectoryID]; !ok {
		writeTraversalError(w, ErrTrajectoryNotFound)
		return
	}
	if existing, ok := s.checkpoints[req.TrajectoryID]; ok {
		if string(existing) != string(req.State) {
			exploreWriteError(w, http.StatusConflict, "CHECKPOINT_CONFLICT", "conflicting checkpoint replay")
			return
		}
		exploreWriteJSON(w, http.StatusOK, response)
		return
	}
	if sub, _ := s.state.Submission(r.Context(), req.TrajectoryID); sub != nil {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_TERMINAL", "trajectory is already submitted")
		return
	}
	s.checkpoints[req.TrajectoryID] = append(json.RawMessage(nil), req.State...)
	exploreWriteJSON(w, http.StatusOK, response)
}

type expandResponse struct {
	ExpansionID    string            `json:"expansion_id"`
	Round          int               `json:"round"`
	BudgetExceeded bool              `json:"budget_exceeded"`
	Candidates     []expandCandidate `json:"candidates"`
}

type exploreRequest struct {
	TrajectoryID   string   `json:"trajectory_id"`
	NodeIDs        []string `json:"node_ids"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type exploredNode struct {
	NodeID          string            `json:"node_id"`
	Level           int               `json:"level"`
	EpistemicStatus string            `json:"epistemic_status,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Body            string            `json:"body"`
	Truncated       bool              `json:"truncated"`
	Staging         bool              `json:"staging,omitempty"`
	Neighbors       []expandCandidate `json:"neighbors,omitempty"`
}

type exploreResponse struct {
	Round          int            `json:"round"`
	BudgetExceeded bool           `json:"budget_exceeded"`
	Nodes          []exploredNode `json:"nodes"`
}

// handleExplore is the sole graph-read endpoint. Validation is completed for
// the whole request before TraversalStore consumes a single node round.
func (s *ExploreToolServer) handleExplore(w http.ResponseWriter, r *http.Request) {
	var req exploreRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || len(req.NodeIDs) == 0 {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id and node_ids are required")
		return
	}
	if s.ledger != nil && req.TrajectoryID != s.ledger.TrajectoryID() {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_FENCED", "trajectory does not match the active run")
		return
	}
	reservation, durable, err := s.reserveAgentOperation(r.Context(), "explore", req.IdempotencyKey, req)
	if err != nil {
		exploreWriteError(w, http.StatusBadRequest, "OPERATION_RESERVATION_FAILED", err.Error())
		return
	}
	if durable && s.writeAgentOperationReplay(w, reservation) {
		return
	}
	g, err := s.loadGraph()
	if err != nil {
		exploreWriteError(w, http.StatusInternalServerError, "GRAPH_ERROR", err.Error())
		return
	}
	for _, id := range req.NodeIDs {
		if IsStagingID(id) {
			if _, err := s.store.ReadStagingSegment(strings.TrimPrefix(id, stagingDocPrefix)); err != nil {
				exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
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
	if _, err := s.state.RegisterTrajectory(r.Context(), req.TrajectoryID, req.NodeIDs, len(req.NodeIDs)); err != nil {
		writeTraversalError(w, err)
		return
	}
	served, round, exceeded, err := s.state.Serve(r.Context(), req.TrajectoryID, req.NodeIDs, s.cfg.MaxRounds)
	if err != nil {
		writeTraversalError(w, err)
		return
	}
	nodes := make([]exploredNode, 0, served)
	for _, id := range req.NodeIDs[:served] {
		nodes = append(nodes, s.exploreNode(g, id))
	}
	response := exploreResponse{Round: round, BudgetExceeded: exceeded, Nodes: nodes}
	if s.ledger != nil {
		viewed := append([]string(nil), req.NodeIDs[:served]...)
		if err := s.ledger.RecordViewed(r.Context(), viewed); err != nil {
			exploreWriteError(w, http.StatusConflict, "VIEW_RECORD_FAILED", err.Error())
			return
		}
		if progress, ok := s.ledger.(AgentToolProgress); ok {
			durableRounds, err := progress.ExplorationRounds(r.Context())
			if err != nil {
				exploreWriteError(w, http.StatusConflict, "ROUND_RECORD_FAILED", err.Error())
				return
			}
			response.Round = durableRounds
			response.BudgetExceeded = durableRounds >= s.cfg.MaxRounds
		}
	}
	if err := s.completeAgentOperation(r.Context(), reservation, response, ""); err != nil {
		exploreWriteError(w, http.StatusConflict, "OPERATION_COMPLETION_FAILED", err.Error())
		return
	}
	exploreWriteJSON(w, http.StatusOK, response)
}

func (s *ExploreToolServer) exploreNode(g *Graph, id string) exploredNode {
	if IsStagingID(id) {
		body, _ := s.store.ReadStagingSegment(strings.TrimPrefix(id, stagingDocPrefix))
		n := exploredNode{NodeID: id, Level: -1, Staging: true, Body: string(body)}
		return truncateExploreBody(&n, s.cfg.MaxNodeChars)
	}
	node := g.Node(id)
	n := exploredNode{NodeID: node.NodeID, Level: node.Level, EpistemicStatus: node.Epistemic, Tags: node.Tags, Body: node.Body, Neighbors: s.expandCandidates(g, node, "")}
	return truncateExploreBody(&n, s.cfg.MaxNodeChars)
}

func truncateExploreBody(node *exploredNode, max int) exploredNode {
	if len(node.Body) > max {
		node.Body, node.Truncated = node.Body[:max], true
	}
	return *node
}

func (s *ExploreToolServer) handleExpand(w http.ResponseWriter, r *http.Request) {
	var req expandRequest
	if !s.decodeExploreRequest(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrajectoryID) == "" || strings.TrimSpace(req.NodeID) == "" || strings.TrimSpace(req.RequestKey) == "" {
		exploreWriteError(w, http.StatusBadRequest, "MISSING_FIELDS", "trajectory_id, node_id and request_key are required")
		return
	}
	if IsStagingID(req.NodeID) {
		exploreWriteError(w, http.StatusBadRequest, "STAGING_NOT_EXPANDABLE", "staging segments have no graph neighbors")
		return
	}

	// gen derives the ordered candidates from the pinned graph. The store
	// calls it only after the request key, anchor and budget checks pass, so
	// rejected expands never consume a round (A24).
	gen := func() ([]expandCandidate, error) {
		g, err := s.loadGraph()
		if err != nil {
			return nil, err
		}
		node := g.Node(req.NodeID)
		if node == nil || !s.viewAllows(node) {
			return nil, errViewNodeNotFound
		}
		candidates := s.expandCandidates(g, node, req.Relation)
		if len(candidates) > s.cfg.MaxExpandPerRound {
			candidates = candidates[:s.cfg.MaxExpandPerRound]
		}
		return candidates, nil
	}

	outcome, err := s.state.Expand(r.Context(), req.TrajectoryID, req.NodeID, req.Relation, req.RequestKey, s.cfg.MaxRounds, gen)
	if err != nil {
		if errors.Is(err, errViewNodeNotFound) {
			exploreWriteError(w, http.StatusNotFound, "NODE_NOT_FOUND", "node not found")
			return
		}
		writeTraversalError(w, err)
		return
	}
	candidates := outcome.Batch.Candidates
	if candidates == nil {
		candidates = []expandCandidate{}
	}
	exploreWriteJSON(w, http.StatusOK, expandResponse{
		ExpansionID:    outcome.Batch.ExpansionID,
		Round:          outcome.Batch.Round,
		BudgetExceeded: outcome.BudgetExceeded,
		Candidates:     candidates,
	})
}

// expandCandidateRef identifies one expand candidate without rendering it. The
// shared construction keeps /explore and backtest L3 on the same candidate
// priority, visibility and cap semantics.
type expandCandidateRef struct {
	NodeID string
	Via    string
}

// expandCandidateRefs orders neighbors by the design §5.2 priority: hierarchy
// parents/children (summarizes) first, then entity_refs co-occurrence, then
// typed relation neighbors, then embedding neighbors. Cross-level relation
// edges with |LevelDelta| > 1 are demoted to the end, except evidence_for
// which is never demoted (Q5).
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
	TrajectoryID   string   `json:"trajectory_id"`
	Found          bool     `json:"found"`
	Summary        string   `json:"summary"`
	NodeIDs        []string `json:"node_ids"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type submitResponse struct {
	Status    string     `json:"status"`
	Found     bool       `json:"found"`
	NodeIDs   []string   `json:"node_ids"`
	Warnings  []string   `json:"warnings,omitempty"`
	Citations []Citation `json:"citations,omitempty"`
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
	if s.ledger != nil && req.TrajectoryID != s.ledger.TrajectoryID() {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_FENCED", "trajectory does not match the active run")
		return
	}
	reservation, durable, err := s.reserveAgentOperation(r.Context(), "submit", req.IdempotencyKey, req)
	if err != nil {
		exploreWriteError(w, http.StatusBadRequest, "OPERATION_RESERVATION_FAILED", err.Error())
		return
	}
	if durable && s.writeAgentOperationReplay(w, reservation) {
		return
	}

	s.terminalMu.Lock()
	_, checkpointed := s.checkpoints[req.TrajectoryID]
	s.terminalMu.Unlock()
	if checkpointed {
		exploreWriteError(w, http.StatusConflict, "TRAJECTORY_TERMINAL", "trajectory is already checkpointed")
		return
	}
	var rec submitRecord
	if s.ledger != nil {
		seen := make(map[string]struct{}, len(req.NodeIDs))
		for _, nodeID := range req.NodeIDs {
			nodeID = strings.TrimSpace(nodeID)
			if nodeID == "" {
				exploreWriteError(w, http.StatusBadRequest, "INVALID_NODE_ID", "node_ids cannot contain an empty id")
				return
			}
			if _, duplicate := seen[nodeID]; duplicate {
				writeTraversalError(w, fmt.Errorf("%w: %q", ErrSubmitDuplicateNodes, nodeID))
				return
			}
			seen[nodeID] = struct{}{}
		}
		rec = submitRecord{Found: req.Found, Summary: req.Summary, NodeIDs: append([]string(nil), req.NodeIDs...)}
	} else {
		rec, err = s.state.Submit(r.Context(), req.TrajectoryID, req.Found, req.Summary, req.NodeIDs)
		if err != nil {
			writeTraversalError(w, err)
			return
		}
	}
	citations := qualifyRecallCitations(s.store, s.version, rec.NodeIDs)
	response := submitResponse{
		Status:    "recorded",
		Found:     rec.Found,
		NodeIDs:   rec.NodeIDs,
		Warnings:  rec.Warnings,
		Citations: citations,
	}
	if s.ledger != nil {
		statePatch, _ := json.Marshal(map[string]any{
			"objective": rec.Summary,
		})
		raw, _ := json.Marshal(response)
		if err := s.ledger.Finish(r.Context(), reservation.OperationID, "submitted", statePatch, citations, raw); err != nil {
			exploreWriteError(w, http.StatusConflict, "SUBMIT_FAILED", err.Error())
			return
		}
	} else if err := s.completeAgentOperation(r.Context(), reservation, response, ""); err != nil {
		exploreWriteError(w, http.StatusConflict, "OPERATION_COMPLETION_FAILED", err.Error())
		return
	}
	exploreWriteJSON(w, http.StatusOK, response)
}

// ---------------------------------------------------------------------------
// in-memory traversal store (tests, backtests)
// ---------------------------------------------------------------------------

// memTrajectory is the in-memory per-trajectory traversal state.
type memTrajectory struct {
	viewQuota       int
	rounds          int
	budgetBlown     bool
	batches         map[string]*memBatch // by expansion id
	batchByKey      map[string]*memBatch // by request key
	viewed          []string
	viewedSet       map[string]bool
	submission      *submitRecord
	submissionHash  string
	seedExpansionID string
}

// memBatch is one persisted expansion batch with its distinct-view
// accounting.
type memBatch struct {
	batch     expansionBatch
	viewedSet map[string]bool
	quota     int
}

type inMemoryTraversalStore struct {
	mu           sync.Mutex
	trajectories map[string]*memTrajectory
}

func newInMemoryTraversalStore() *inMemoryTraversalStore {
	return &inMemoryTraversalStore{trajectories: make(map[string]*memTrajectory)}
}

func (m *inMemoryTraversalStore) RegisterTrajectory(_ context.Context, trajectoryID string, seedCandidateIDs []string, viewQuota int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tr, ok := m.trajectories[trajectoryID]; ok {
		return tr.seedExpansionID, nil
	}
	if viewQuota < 1 {
		viewQuota = 1
	}
	tr := &memTrajectory{
		viewQuota:  viewQuota,
		batches:    make(map[string]*memBatch),
		batchByKey: make(map[string]*memBatch),
		viewedSet:  make(map[string]bool),
	}
	candidates := make([]expandCandidate, 0, len(seedCandidateIDs))
	for _, id := range seedCandidateIDs {
		candidates = append(candidates, expandCandidate{NodeID: id, Via: "seed", Level: -1})
	}
	seed := &memBatch{
		batch: expansionBatch{
			ExpansionID: newExpansionID(),
			Round:       0,
			RequestKey:  "seed",
			Candidates:  candidates,
		},
		viewedSet: make(map[string]bool),
		quota:     viewQuota,
	}
	tr.batches[seed.batch.ExpansionID] = seed
	tr.batchByKey["seed"] = seed
	tr.seedExpansionID = seed.batch.ExpansionID
	m.trajectories[trajectoryID] = tr
	return seed.batch.ExpansionID, nil
}

// newExpansionID returns a unique expansion id (uuid-shaped, crypto random).
func newExpansionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (m *inMemoryTraversalStore) lookup(trajectoryID string) (*memTrajectory, error) {
	tr, ok := m.trajectories[trajectoryID]
	if !ok {
		return nil, ErrTrajectoryNotFound
	}
	return tr, nil
}

func (m *inMemoryTraversalStore) Expand(_ context.Context, trajectoryID, anchor, relation, requestKey string, maxRounds int, gen func() ([]expandCandidate, error)) (expandOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return expandOutcome{}, err
	}
	// Request-key idempotency (A24): an exact replay returns the original
	// batch without consuming a round; a conflicting reuse is rejected.
	if prev, ok := tr.batchByKey[requestKey]; ok {
		if prev.batch.Anchor != anchor || prev.batch.Relation != relation {
			return expandOutcome{}, fmt.Errorf("%w: request key %q", ErrRequestKeyConflict, requestKey)
		}
		return expandOutcome{Batch: prev.batch, BudgetExceeded: prev.batch.Round >= maxRounds}, nil
	}
	// Anchor eligibility (A3): only a previously viewed node expands.
	if !tr.viewedSet[anchor] {
		return expandOutcome{}, fmt.Errorf("%w: %q", ErrAnchorNotViewed, anchor)
	}
	// Budget enforcement (Q15/A6): expanding past the round budget is a
	// violation — the trajectory is marked budget-blown and no batch is
	// created. The final allowed expansion itself is not a violation.
	if tr.rounds >= maxRounds {
		tr.budgetBlown = true
		return expandOutcome{
			Batch:          expansionBatch{Round: tr.rounds},
			BudgetExceeded: true,
		}, nil
	}
	candidates, err := gen()
	if err != nil {
		return expandOutcome{}, err
	}
	tr.rounds++
	batch := &memBatch{
		batch: expansionBatch{
			ExpansionID: newExpansionID(),
			Round:       tr.rounds,
			Anchor:      anchor,
			Relation:    relation,
			RequestKey:  requestKey,
			Candidates:  candidates,
		},
		viewedSet: make(map[string]bool),
		quota:     tr.viewQuota,
	}
	tr.batches[batch.batch.ExpansionID] = batch
	tr.batchByKey[requestKey] = batch
	return expandOutcome{Batch: batch.batch, BudgetExceeded: batch.batch.Round >= maxRounds}, nil
}

func (m *inMemoryTraversalStore) RecordView(_ context.Context, trajectoryID, expansionID, nodeID string, load func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return err
	}
	batch, ok := tr.batches[expansionID]
	if !ok {
		return ErrExpansionNotFound
	}
	isCandidate := false
	for _, c := range batch.batch.Candidates {
		if c.NodeID == nodeID {
			isCandidate = true
			break
		}
	}
	if !isCandidate {
		return fmt.Errorf("%w: %q", ErrViewNotInBatch, nodeID)
	}
	if batch.viewedSet[nodeID] {
		// Idempotent re-view: no additional slot consumed (A2). The load
		// still runs so the caller gets the body.
		return load()
	}
	if len(batch.viewedSet) >= batch.quota {
		return fmt.Errorf("%w: %q", ErrViewQuotaExceeded, nodeID)
	}
	// The reservation commits only on a successful load; a failed load
	// releases it (A24).
	if err := load(); err != nil {
		return err
	}
	batch.viewedSet[nodeID] = true
	if !tr.viewedSet[nodeID] {
		tr.viewedSet[nodeID] = true
		tr.viewed = append(tr.viewed, nodeID)
	}
	return nil
}

func (m *inMemoryTraversalStore) Submit(_ context.Context, trajectoryID string, found bool, summary string, nodeIDs []string) (submitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return submitRecord{}, err
	}
	if seen := map[string]bool{}; len(nodeIDs) > 0 {
		for _, id := range nodeIDs {
			if seen[id] {
				return submitRecord{}, fmt.Errorf("%w: %q", ErrSubmitDuplicateNodes, id)
			}
			seen[id] = true
		}
	}
	for _, id := range nodeIDs {
		if !tr.viewedSet[id] {
			return submitRecord{}, fmt.Errorf("%w: %q", ErrSubmitNodeNotViewed, id)
		}
	}
	hash := submissionHash(found, summary, nodeIDs)
	if tr.submission != nil {
		if tr.submissionHash == hash {
			return *tr.submission, nil
		}
		return submitRecord{}, ErrSubmitConflict
	}
	var warnings []string
	// Budget-blown trajectories (design Q15/A6): the submission is recorded
	// for audit, but Found is forced to false no matter what the agent
	// reported — a trajectory that blew the exploration budget cannot claim
	// a find.
	if tr.budgetBlown && found {
		found = false
		warnings = append(warnings, "exploration budget exceeded: submission recorded with found=false")
	}
	rec := submitRecord{Found: found, Summary: summary, NodeIDs: nodeIDs, Warnings: warnings}
	tr.submission = &rec
	tr.submissionHash = hash
	return rec, nil
}

// submissionHash fingerprints one submission payload for idempotent replay.
func submissionHash(found bool, summary string, nodeIDs []string) string {
	body, _ := json.Marshal(struct {
		Found   bool     `json:"found"`
		Summary string   `json:"summary"`
		NodeIDs []string `json:"node_ids"`
	}{found, summary, nodeIDs})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (m *inMemoryTraversalStore) Serve(_ context.Context, trajectoryID string, nodeIDs []string, maxRounds int) (served, rounds int, budgetExceeded bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return 0, 0, false, err
	}
	if tr.rounds >= maxRounds {
		tr.budgetBlown = true
		return 0, tr.rounds, true, nil
	}
	remaining := maxRounds - tr.rounds
	served = len(nodeIDs)
	if served > remaining {
		served = remaining
		tr.budgetBlown = true
	}
	tr.rounds += served
	// The handler has already resolved each served id against the pinned graph.
	// Synthetic IDs preserve submission validation while retaining observed order.
	for _, id := range nodeIDs[:served] {
		if !tr.viewedSet[id] {
			tr.viewedSet[id] = true
			tr.viewed = append(tr.viewed, id)
		}
	}
	return served, tr.rounds, tr.rounds >= maxRounds, nil
}

func (m *inMemoryTraversalStore) Rounds(_ context.Context, trajectoryID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return 0, err
	}
	return tr.rounds, nil
}

func (m *inMemoryTraversalStore) BudgetBlown(_ context.Context, trajectoryID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return false, err
	}
	return tr.budgetBlown, nil
}

func (m *inMemoryTraversalStore) Submission(_ context.Context, trajectoryID string) (*submitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return nil, err
	}
	return tr.submission, nil
}

func (m *inMemoryTraversalStore) Viewed(_ context.Context, trajectoryID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, err := m.lookup(trajectoryID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(tr.viewed))
	copy(out, tr.viewed)
	return out, nil
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

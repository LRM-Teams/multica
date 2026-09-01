package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrGraphMemoryAgentGatewayForbidden = errors.New("graph memory agent gateway forbidden")
	ErrGraphMemoryAgentGatewayOperation = errors.New("graph memory agent gateway operation invalid")
)

var graphMemoryAgentOperations = map[string]struct{}{
	"start": {}, "explore": {}, "redirect": {}, "submit": {}, "checkpoint": {},
}

// GraphMemoryAgentGateway is the public Agent-data-plane adapter for the five
// server-authoritative Graph operations. Scope and graph version come only
// from managed identity, channel route, and the durable active claim.
//
// Protocol generations (plan Task 12): generation 1 serves the five
// operations from the versioned graph store exactly as before; generation 2
// is negotiated per workspace from durable state (the daemon's advertised
// memory_explore_v2 capability on the managed agent's runtime row AND a
// green memory_explore_v2 phase gate) and serves the same five operation
// names from the canonical Interaction DAG through MemoryExploreV2Service.
type GraphMemoryAgentGateway struct {
	pool   *pgxpool.Pool
	runs   *GraphMemoryAgentRunStore
	policy *MemoryProviderPolicyResolver
	v2     *MemoryExploreV2Service
}

func NewGraphMemoryAgentGateway(pool *pgxpool.Pool, policy *MemoryProviderPolicyResolver) *GraphMemoryAgentGateway {
	return &GraphMemoryAgentGateway{
		pool: pool, runs: NewGraphMemoryAgentRunStore(pool), policy: policy,
		v2: NewMemoryExploreV2Service(pool),
	}
}

// NegotiatedProtocolGeneration resolves the protocol generation a NEW run
// would use, from durable state only: the capability the daemon advertised
// at registration (persisted on the managed agent's runtime row) AND the
// workspace's explore phase gate. Any missing side, unbound runtime, or read
// error means generation 1 — capability alone never authorizes a disabled
// server path.
func (g *GraphMemoryAgentGateway) NegotiatedProtocolGeneration(ctx context.Context, workspaceID, channelID string) int {
	if g == nil || g.pool == nil {
		return 1
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return 1
	}
	channelUUID, err := util.ParseUUID(channelID)
	if err != nil {
		return 1
	}
	var capabilities []string
	if err := g.pool.QueryRow(ctx, `
		SELECT rt.metadata->'capabilities'
		FROM graph_memory_channel_agent managed
		JOIN agent_runtime rt ON rt.id=managed.runtime_id
		WHERE managed.workspace_id=$1::uuid AND managed.channel_id=$2::uuid
		  AND managed.status='active'`, workspaceUUID, channelUUID).Scan(&capabilities); err != nil {
		return 1
	}
	return ResolveGraphMemoryAgentProtocol(ctx, capabilities, g.pool, workspaceUUID)
}

// protocolForRun returns the generation pinned for an active run: 2 iff the
// run's trajectory carries a persisted Explore plan (written by a start that
// negotiated generation 2). The pin survives a later gate flip — the next v2
// operation then fails closed instead of falling back to v1 payloads — and a
// run started under v1 never switches to v2 mid-run.
func (g *GraphMemoryAgentGateway) protocolForRun(ctx context.Context, workspaceID pgtype.UUID, trajectoryID string) int {
	if g == nil || g.pool == nil || strings.TrimSpace(trajectoryID) == "" {
		return 1
	}
	var pinned bool
	if err := g.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memory_explore_plan
		WHERE workspace_id=$1 AND trajectory_id=$2)`, workspaceID, trajectoryID).Scan(&pinned); err != nil || !pinned {
		return 1
	}
	return 2
}

// SetSubmittedRunSink wires the submitted-run segment sink on the run store
// (see GraphMemorySubmittedRunSink). Nil-safe.
func (g *GraphMemoryAgentGateway) SetSubmittedRunSink(sink GraphMemorySubmittedRunSink) {
	if g == nil || g.runs == nil {
		return
	}
	g.runs.SetSubmittedRunSink(sink)
}

func (g *GraphMemoryAgentGateway) AddUsage(ctx context.Context, workspaceID, agentID, channelID string, inputTokens, outputTokens int64) error {
	if g == nil || g.pool == nil {
		return ErrGraphMemoryAgentRunUnavailable
	}
	if inputTokens < 0 || outputTokens < 0 {
		return errors.New("graph memory agent usage cannot be negative")
	}
	var authorized bool
	if err := g.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM graph_memory_channel_agent managed
		  JOIN agent ON agent.id=managed.agent_id
		  WHERE managed.workspace_id=$1::uuid AND managed.channel_id=$2::uuid
		    AND managed.agent_id=$3::uuid AND managed.status='active'
		    AND agent.managed_role='graph_memory_channel' AND agent.archived_at IS NULL
		)`, workspaceID, channelID, agentID).Scan(&authorized); err != nil {
		return err
	}
	if !authorized {
		return ErrGraphMemoryAgentGatewayForbidden
	}
	runContext, err := g.runs.ActiveClaim(ctx, workspaceID, channelID)
	if err == nil {
		return g.runs.AddUsage(ctx, runContext.Claim.RunID, runContext.Claim.FencingToken, inputTokens, outputTokens)
	}
	claim, latestErr := g.runs.LatestClaim(ctx, workspaceID, channelID)
	if latestErr != nil {
		return err
	}
	return g.runs.AddUsage(ctx, claim.RunID, claim.FencingToken, inputTokens, outputTokens)
}
func (g *GraphMemoryAgentGateway) ServeHTTP(w http.ResponseWriter, r *http.Request, workspaceID, agentID, channelID, operation string) error {
	if g == nil || g.pool == nil {
		return ErrGraphMemoryAgentRunUnavailable
	}
	operation = strings.TrimSpace(operation)
	if _, ok := graphMemoryAgentOperations[operation]; !ok {
		return ErrGraphMemoryAgentGatewayOperation
	}
	var authorized bool
	if err := g.pool.QueryRow(r.Context(), `
		SELECT EXISTS(
		  SELECT 1 FROM graph_memory_channel_agent managed
		  JOIN agent ON agent.id=managed.agent_id
		  JOIN channel_member member ON member.channel_id=managed.channel_id
		    AND member.workspace_id=managed.workspace_id AND member.member_type='agent' AND member.member_id=managed.agent_id
		  WHERE managed.workspace_id=$1::uuid AND managed.channel_id=$2::uuid
		    AND managed.agent_id=$3::uuid AND managed.status='active'
		    AND agent.managed_role='graph_memory_channel' AND agent.archived_at IS NULL
		)`, workspaceID, channelID, agentID).Scan(&authorized); err != nil {
		return err
	}
	if !authorized {
		return ErrGraphMemoryAgentGatewayForbidden
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
	if err != nil {
		return err
	}
	if len(body) > 64*1024 {
		return fmt.Errorf("graph memory agent tool body exceeds 64 KiB")
	}
	if !json.Valid(body) {
		return fmt.Errorf("graph memory agent tool body must be valid JSON")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return err
	}

	var runContext GraphMemoryAgentRunContext
	startQuery := ""
	if operation == "start" {
		var start struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(body, &start); err != nil || strings.TrimSpace(start.Query) == "" {
			return errors.New("graph memory agent start query is required")
		}
		startQuery = start.Query
		if g.NegotiatedProtocolGeneration(r.Context(), workspaceID, channelID) == 2 {
			return g.serveExploreV2Start(w, r, body, workspaceUUID, channelID, startQuery)
		}
	} else {
		runContext, err = g.runs.ActiveClaim(r.Context(), workspaceID, channelID)
		if err != nil {
			return err
		}
		if g.protocolForRun(r.Context(), workspaceUUID, runContext.Claim.TrajectoryID) == 2 {
			return g.serveExploreV2Operation(w, r, body, workspaceUUID, runContext, operation)
		}
	}

	root, err := graphMemoryWorkspacesRoot()
	if err != nil {
		return err
	}
	route, err := ResolveChannelRoute(r.Context(), g.pool, workspaceID, channelID)
	if err != nil {
		return err
	}
	storeDir, err := memorygraph.EnsureScopedDir(root, workspaceID, memorygraph.GraphDirKind(route.GraphKind), route.GraphOwnerID)
	if err != nil {
		return err
	}
	store := memorygraph.NewStore(storeDir)
	if err := store.Init(); err != nil {
		return err
	}

	if operation == "start" {
		wsUUID, wsErr := util.ParseUUID(workspaceID)
		version, err := func() (int, error) {
			if wsErr != nil {
				return store.CurrentVersion()
			}
			return activeGraphVersionForStore(r.Context(), g.pool, wsUUID, route.GraphKind, route.GraphOwnerID, store)
		}()
		if err != nil {
			return err
		}
		claim, err := g.runs.Claim(r.Context(), workspaceID, channelID, "channel", channelID, startQuery, int64(version))
		if err != nil {
			return err
		}
		runContext, err = g.runs.ActiveClaim(r.Context(), workspaceID, channelID)
		if err != nil {
			return err
		}
		runContext.Claim = claim
	}

	var embedder *memorygraph.CachedEmbedder
	resolvedEmbed, policyErr := g.policy.Resolve(r.Context(), workspaceUUID, ProviderEmbed)
	switch {
	case policyErr == nil:
		scope := resolvedMemoryProviderScope(workspaceID, ProviderEmbed, resolvedEmbed)
		provider, err := memorygraph.NewOpenAIEmbedderFromEnv(scope)
		if err == nil {
			embedder, err = memorygraph.NewCachedEmbedder(provider, store, scope)
		}
		if err != nil && !errors.Is(err, memorygraph.ErrEmbedNotConfigured) {
			return err
		}
	case MemoryProviderPolicyAllowsDegradation(policyErr, ProviderEmbed, DegradeBM25):
		// Disabled or unavailable embedding deterministically uses BM25.
	default:
		return policyErr
	}

	retrievalConfig := memorygraph.DefaultRetrievalConfig()
	if route.GraphKind == string(memorygraph.GraphDirKindProject) {
		retrievalConfig.View = memorygraph.GraphView{AllowProject: true, ChannelID: channelID}
	} else {
		retrievalConfig.View = memorygraph.GraphView{ChannelID: channelID}
	}
	retriever := memorygraph.NewHybridRetriever(store, embedder, retrievalConfig)
	if err := retriever.RebuildForVersion(r.Context(), int(runContext.Claim.GraphVersion)); err != nil {
		return err
	}
	profile, err := db.New(g.pool).GetGraphMemoryProfile(r.Context(), workspaceUUID)
	if err != nil {
		return err
	}
	cfg := memorygraph.DefaultExploreConfig()
	cfg.Agents = 1
	cfg.MaxRounds = int(profile.ExploreMaxRounds)
	cfg.MaxExpandPerRound = int(profile.ExploreNodesPerExpansion)
	ledger := NewGraphMemoryAgentToolLedger(g.runs, runContext.Claim, runContext.ConsumedSeq)
	toolServer, err := memorygraph.NewExploreToolServerWithAgentLedger(store, retriever, cfg, int(runContext.Claim.GraphVersion), nil, ledger)
	if err != nil {
		return err
	}
	toolServer.ServeAuthorizedHTTP(w, r, operation)
	return nil
}

// graphMemoryAgentV2Request is the shared shape of the five v2 tool bodies.
// Every field except ref/focus is operation-specific; the trajectory must be
// the active run's own trajectory and the idempotency key is mandatory.
type graphMemoryAgentV2Request struct {
	Query          string                `json:"query"`
	TrajectoryID   string                `json:"trajectory_id"`
	IdempotencyKey string                `json:"idempotency_key"`
	Focus          string                `json:"focus"`
	Ref            memorygraph.MemoryRef `json:"ref"`
}

// serveExploreV2Start executes a generation-2 start: it claims the run
// (server-owned scope, quotas, fencing), pins the Explore plan for the
// trajectory — the durable marker that pins this run at generation 2 — and
// answers with the structured payload (plan + seed refs).
func (g *GraphMemoryAgentGateway) serveExploreV2Start(
	w http.ResponseWriter, r *http.Request, body []byte, workspaceUUID pgtype.UUID, channelID, query string,
) error {
	var request graphMemoryAgentV2Request
	if err := json.Unmarshal(body, &request); err != nil {
		return err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return errors.New("graph memory agent start idempotency_key is required")
	}
	route, err := ResolveChannelRoute(r.Context(), g.pool, workspaceUUID.String(), channelID)
	if err != nil {
		return err
	}
	graphs := []PinnedGraph{{
		Kind: route.GraphKind, OwnerID: route.GraphOwnerID, Generation: route.Generation,
	}}
	claim, err := g.runs.Claim(r.Context(), workspaceUUID.String(), channelID, "channel", channelID, query, 0)
	if err != nil {
		return err
	}
	reservation, err := g.runs.ReserveToolOperation(r.Context(), claim.RunID, claim.FencingToken,
		request.IdempotencyKey, "start", body)
	if err != nil {
		return err
	}
	if reservation.Pending {
		return errors.New("graph memory agent start operation is already in progress")
	}
	if reservation.Replay {
		writeGraphMemoryAgentJSON(w, reservation.Response)
		return nil
	}
	started, startErr := g.v2.Start(r.Context(), workspaceUUID, claim.TrajectoryID, graphs)
	if startErr != nil {
		// A gate flip between claim and Start (or any refused start) leaves
		// a claimed run that can never serve v2: terminalize it so the
		// channel is not wedged, then surface the refusal — never a v1
		// fallback payload for a negotiated v2 start.
		_ = g.runs.Finish(r.Context(), claim.RunID, claim.FencingToken, "failed", 0, json.RawMessage("{}"), nil)
		_ = g.runs.CompleteToolOperation(r.Context(), claim.RunID, claim.FencingToken,
			reservation.OperationID, json.RawMessage(`{}`), startErr.Error())
		return startErr
	}
	raw, err := json.Marshal(map[string]any{
		"protocol_generation": 2,
		"trajectory_id":       claim.TrajectoryID,
		"run_id":              claim.RunID,
		"resumed":             claim.Resumed,
		"plan":                started.Plan,
		"seeds":               started.Seeds,
	})
	if err != nil {
		return err
	}
	if err := g.runs.CompleteToolOperation(r.Context(), claim.RunID, claim.FencingToken,
		reservation.OperationID, raw, ""); err != nil {
		return err
	}
	writeGraphMemoryAgentJSON(w, raw)
	return nil
}

// serveExploreV2Operation executes explore/redirect/submit/checkpoint for a
// run pinned at generation 2. The v2 service rechecks the phase gate and the
// Task 8A fence on every operation: a red gate or a mid-walk retraction
// refuses the operation closed — v2 unavailable, never v1 fallback exposure.
func (g *GraphMemoryAgentGateway) serveExploreV2Operation(
	w http.ResponseWriter, r *http.Request, body []byte, workspaceUUID pgtype.UUID,
	runContext GraphMemoryAgentRunContext, operation string,
) error {
	claim := runContext.Claim
	var request graphMemoryAgentV2Request
	if err := json.Unmarshal(body, &request); err != nil {
		return err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("graph memory agent %s idempotency_key is required", operation)
	}
	if strings.TrimSpace(request.TrajectoryID) != claim.TrajectoryID {
		return ErrGraphMemoryAgentGatewayForbidden
	}
	reservation, err := g.runs.ReserveToolOperation(r.Context(), claim.RunID, claim.FencingToken,
		request.IdempotencyKey, operation, body)
	if err != nil {
		return err
	}
	if reservation.Pending {
		return fmt.Errorf("graph memory agent %s operation is already in progress", operation)
	}
	if reservation.Replay {
		writeGraphMemoryAgentJSON(w, reservation.Response)
		return nil
	}

	var response any
	switch operation {
	case "explore":
		if err := memorygraph.ValidateMemoryRef(request.Ref); err != nil {
			return g.completeV2Refusal(r.Context(), claim, reservation, err)
		}
		neighbors, exploreErr := g.v2.Explore(r.Context(), workspaceUUID, claim.TrajectoryID, request.Ref)
		if exploreErr != nil {
			return g.completeV2Refusal(r.Context(), claim, reservation, exploreErr)
		}
		response = map[string]any{
			"protocol_generation": 2, "trajectory_id": claim.TrajectoryID, "neighbors": neighbors,
		}
	case "redirect":
		if strings.TrimSpace(request.Focus) == "" {
			return g.completeV2Refusal(r.Context(), claim, reservation,
				errors.New("graph memory agent redirect focus is required"))
		}
		if redirectErr := g.v2.Redirect(r.Context(), workspaceUUID, claim.TrajectoryID, request.Focus); redirectErr != nil {
			return g.completeV2Refusal(r.Context(), claim, reservation, redirectErr)
		}
		response = map[string]any{
			"protocol_generation": 2, "trajectory_id": claim.TrajectoryID, "redirected": true, "focus": request.Focus,
		}
	case "submit":
		if submitErr := g.v2.Submit(r.Context(), workspaceUUID, claim.TrajectoryID); submitErr != nil {
			return g.completeV2Refusal(r.Context(), claim, reservation, submitErr)
		}
		return g.finishV2Terminal(r.Context(), w, claim, runContext, reservation,
			"submitted", map[string]any{"protocol_generation": 2, "submitted": true})
	case "checkpoint":
		checkpoint, checkpointErr := g.v2.Checkpoint(r.Context(), workspaceUUID, claim.TrajectoryID)
		if checkpointErr != nil {
			return g.completeV2Refusal(r.Context(), claim, reservation, checkpointErr)
		}
		return g.finishV2Terminal(r.Context(), w, claim, runContext, reservation,
			"checkpointed", map[string]any{"protocol_generation": 2, "checkpoint": checkpoint})
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if err := g.runs.CompleteToolOperation(r.Context(), claim.RunID, claim.FencingToken,
		reservation.OperationID, raw, ""); err != nil {
		return err
	}
	writeGraphMemoryAgentJSON(w, raw)
	return nil
}

// completeV2Refusal records a refused v2 operation as failed in the
// idempotency ledger and returns the refusal error to the caller.
func (g *GraphMemoryAgentGateway) completeV2Refusal(
	ctx context.Context, claim GraphMemoryAgentRunClaim,
	reservation GraphMemoryAgentToolReservation, cause error,
) error {
	_ = g.runs.CompleteToolOperation(ctx, claim.RunID, claim.FencingToken,
		reservation.OperationID, json.RawMessage(`{}`), cause.Error())
	return cause
}

// finishV2Terminal terminalizes a v2 run (submit/checkpoint) atomically with
// the idempotent operation that produced the terminal response, mirroring the
// v1 ledger contract.
func (g *GraphMemoryAgentGateway) finishV2Terminal(
	ctx context.Context, w http.ResponseWriter, claim GraphMemoryAgentRunClaim,
	runContext GraphMemoryAgentRunContext, reservation GraphMemoryAgentToolReservation,
	terminalStatus string, response any,
) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if err := g.runs.FinishToolOperation(ctx, claim.RunID, claim.FencingToken,
		reservation.OperationID, terminalStatus, runContext.ConsumedSeq, json.RawMessage("{}"), nil, raw); err != nil {
		return err
	}
	writeGraphMemoryAgentJSON(w, raw)
	return nil
}

// writeGraphMemoryAgentJSON writes one already-marshaled tool response.
func writeGraphMemoryAgentJSON(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

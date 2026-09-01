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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrGraphMemoryAgentGatewayForbidden = errors.New("graph memory agent gateway forbidden")
	ErrGraphMemoryAgentGatewayOperation = errors.New("graph memory agent gateway operation invalid")
	ErrGraphMemoryAgentGatewayGraph     = errors.New("graph memory agent gateway graph unavailable")
)

var graphMemoryAgentOperations = map[string]struct{}{
	"start": {}, "explore": {}, "redirect": {}, "submit": {}, "checkpoint": {},
}

const (
	graphMemoryAgentGraphChannel  = "channel"
	graphMemoryAgentGraphResearch = "research"
)

// GraphMemoryAgentGateway is the public Agent-data-plane adapter for the five
// server-authoritative Graph operations. Scope and graph version come only
// from managed identity, channel route, and the durable active claim.
type GraphMemoryAgentGateway struct {
	pool *pgxpool.Pool
	runs *GraphMemoryAgentRunStore
}

func NewGraphMemoryAgentGateway(pool *pgxpool.Pool) *GraphMemoryAgentGateway {
	return &GraphMemoryAgentGateway{pool: pool, runs: NewGraphMemoryAgentRunStore(pool)}
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

	// Graph selector (unification spec §4.4): the gateway serves the
	// channel-route graph (default, for backwards compatibility) and the
	// federated workspace research graph. Anything else fails closed.
	var selector struct {
		Graph string `json:"graph"`
	}
	if err := json.Unmarshal(body, &selector); err != nil {
		return fmt.Errorf("graph memory agent tool body must be a JSON object: %w", err)
	}
	graphSel := strings.TrimSpace(selector.Graph)
	if graphSel == "" {
		graphSel = graphMemoryAgentGraphChannel
	}
	if graphSel != graphMemoryAgentGraphChannel && graphSel != graphMemoryAgentGraphResearch {
		return fmt.Errorf("%w: unknown graph %q", ErrGraphMemoryAgentGatewayGraph, graphSel)
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
	routeGraphVersion, err := store.CurrentVersion()
	if err != nil {
		return err
	}
	routeIdentity := route.GraphKind + ":" + route.GraphOwnerID

	var runContext GraphMemoryAgentRunContext
	if operation == "start" {
		var start struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(body, &start); err != nil || strings.TrimSpace(start.Query) == "" {
			return errors.New("graph memory agent start query is required")
		}
		// The run claim always carries the channel-route graph identity: the
		// research graph is a federated supplement, never the run's primary
		// graph (spec §4.4).
		claim, err := g.runs.Claim(r.Context(), workspaceID, channelID, "channel", channelID, start.Query, int64(routeGraphVersion))
		if err != nil {
			return err
		}
		runContext, err = g.runs.ActiveClaim(r.Context(), workspaceID, channelID)
		if err != nil {
			return err
		}
		runContext.Claim = claim
	} else {
		runContext, err = g.runs.ActiveClaim(r.Context(), workspaceID, channelID)
		if err != nil {
			return err
		}
	}

	// Build the tool server for the selected graph. The channel-route graph
	// pins the claim's version; the research graph pins its own current
	// version under a research-only view — research visibility is never
	// implied by project/channel grants (fail closed, spec §4.4).
	var targetStore *memorygraph.Store
	var targetView memorygraph.GraphView
	var targetVersion int
	var targetIdentity string
	if graphSel == graphMemoryAgentGraphResearch {
		researchDir, err := memorygraph.DirForScope(root, workspaceID, memorygraph.GraphDirKindResearch, workspaceID)
		if err != nil {
			return err
		}
		if err := memorygraph.VerifyGraphIdentity(researchDir, memorygraph.GraphIdentity{
			WorkspaceID: workspaceID, Kind: string(memorygraph.GraphDirKindResearch), OwnerID: workspaceID,
		}); err != nil {
			return fmt.Errorf("%w: research graph is not initialized", ErrGraphMemoryAgentGatewayGraph)
		}
		researchStore := memorygraph.NewStore(researchDir)
		researchVersion, err := researchStore.CurrentVersion()
		if err != nil {
			return fmt.Errorf("%w: research graph version: %v", ErrGraphMemoryAgentGatewayGraph, err)
		}
		targetStore, targetView, targetVersion = researchStore, memorygraph.GraphView{AllowResearch: true}, researchVersion
		targetIdentity = "research:" + workspaceID
	} else {
		if route.GraphKind == string(memorygraph.GraphDirKindProject) {
			targetView = memorygraph.GraphView{AllowProject: true, ChannelID: channelID}
		} else {
			targetView = memorygraph.GraphView{ChannelID: channelID}
		}
		targetStore, targetVersion, targetIdentity = store, int(runContext.Claim.GraphVersion), routeIdentity
	}

	retrievalConfig := memorygraph.DefaultRetrievalConfig()
	retrievalConfig.View = targetView
	retriever := memorygraph.NewHybridRetriever(targetStore, nil, retrievalConfig)
	if err := retriever.RebuildForVersion(r.Context(), targetVersion); err != nil {
		return err
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
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
	ledger := NewGraphMemoryAgentToolLedger(g.runs, runContext.Claim, runContext.ConsumedSeq, targetIdentity)
	toolServer, err := memorygraph.NewExploreToolServerWithAgentLedger(targetStore, retriever, cfg, targetVersion, nil, ledger)
	if err != nil {
		return err
	}
	toolServer.ServeAuthorizedHTTP(w, r, operation)
	return nil
}

package service

import (
	"context"
	"errors"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// GraphMemoryAgentExecutionRequest contains only the authority required for one
// Channel-scoped, single-trajectory run. The caller resolves the graph store
// and pinned version before handing ownership to this service.
type GraphMemoryAgentExecutionRequest struct {
	WorkspaceID   string
	ChannelID     string
	TargetKind    string
	TargetID      string
	InitialQuery  string
	GraphVersion  int64
	ConsumedSeq   int64
	Store         *memorygraph.Store
	Retriever     *memorygraph.HybridRetriever
	ExploreConfig memorygraph.ExploreConfig
}

// GraphMemoryAgentExecution owns the loopback native tools for one fenced run.
// Token is a per-execution delegated credential valid only for these five
// operations; it grants no Channel or workspace API access.
type GraphMemoryAgentExecution struct {
	Claim   GraphMemoryAgentRunClaim
	BaseURL string
	Token   string
	server  *memorygraph.ExploreToolServer
}

func (e *GraphMemoryAgentExecution) Shutdown(ctx context.Context) error {
	if e == nil || e.server == nil {
		return nil
	}
	return e.server.Shutdown(ctx)
}

// GraphMemoryAgentExecutionOwner claims durable state and constructs the
// native five-tool server with its fenced PostgreSQL ledger.
type GraphMemoryAgentExecutionOwner struct {
	runs *GraphMemoryAgentRunStore
}

func NewGraphMemoryAgentExecutionOwner(runs *GraphMemoryAgentRunStore) *GraphMemoryAgentExecutionOwner {
	return &GraphMemoryAgentExecutionOwner{runs: runs}
}

func (o *GraphMemoryAgentExecutionOwner) ClaimAndStart(ctx context.Context, req GraphMemoryAgentExecutionRequest) (*GraphMemoryAgentExecution, error) {
	if o == nil || o.runs == nil || req.Store == nil {
		return nil, ErrGraphMemoryAgentRunUnavailable
	}
	if err := req.Store.Init(); err != nil {
		return nil, err
	}
	claim, err := o.runs.Claim(ctx, req.WorkspaceID, req.ChannelID, req.TargetKind, req.TargetID, req.InitialQuery, req.GraphVersion)
	if err != nil {
		return nil, err
	}
	ledger := NewGraphMemoryAgentToolLedger(o.runs, claim, req.ConsumedSeq)
	server, err := memorygraph.NewExploreToolServerWithAgentLedger(req.Store, req.Retriever, req.ExploreConfig, int(claim.GraphVersion), nil, ledger)
	if err != nil {
		return nil, err
	}
	baseURL, token, err := server.Start(ctx)
	if err != nil {
		return nil, err
	}
	if baseURL == "" || token == "" {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil, errors.New("graph memory agent tool server returned empty delegated endpoint")
	}
	return &GraphMemoryAgentExecution{Claim: claim, BaseURL: baseURL, Token: token, server: server}, nil
}

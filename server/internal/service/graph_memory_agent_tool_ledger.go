package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// GraphMemoryAgentToolLedger binds the native Graph tool server to one fenced
// PostgreSQL run claim. It contains no ambient workspace authority.
//
// Every durable row is scoped to one physical graph (graphIdentity, spec
// §4.4): idempotency keys and viewed-node provenance are namespaced by graph,
// so the channel-route graph and the federated research graph share a run
// without sharing an idempotency or quota namespace.
type GraphMemoryAgentToolLedger struct {
	store       *GraphMemoryAgentRunStore
	claim       GraphMemoryAgentRunClaim
	consumedSeq int64
	graph       string
}

// graphScopedToolKey namespaces one client idempotency key by graph identity.
func graphScopedToolKey(graphIdentity, key string) string {
	return graphIdentity + "|" + strings.TrimSpace(key)
}

func NewGraphMemoryAgentToolLedger(store *GraphMemoryAgentRunStore, claim GraphMemoryAgentRunClaim, consumedSeq int64, graphIdentity string) *GraphMemoryAgentToolLedger {
	if claim.TargetSeq > consumedSeq {
		consumedSeq = claim.TargetSeq
	}
	return &GraphMemoryAgentToolLedger{store: store, claim: claim, consumedSeq: consumedSeq, graph: graphIdentity}
}

func (l *GraphMemoryAgentToolLedger) TrajectoryID() string { return l.claim.TrajectoryID }

func (l *GraphMemoryAgentToolLedger) ValidateOperation(ctx context.Context, operation, key string, request json.RawMessage) error {
	return l.store.ValidateToolOperationQuota(ctx, l.claim.RunID, l.claim.FencingToken, operation, key, l.graph, request)
}
func (l *GraphMemoryAgentToolLedger) Reserve(ctx context.Context, key, operation string, request json.RawMessage) (memorygraph.AgentToolOperationReservation, error) {
	reservation, err := l.store.ReserveToolOperation(ctx, l.claim.RunID, l.claim.FencingToken, graphScopedToolKey(l.graph, key), l.graph, operation, request)
	return memorygraph.AgentToolOperationReservation{
		OperationID: reservation.OperationID,
		Replay:      reservation.Replay,
		Pending:     reservation.Pending,
		Response:    reservation.Response,
		Error:       reservation.Error,
	}, err
}

func (l *GraphMemoryAgentToolLedger) Complete(ctx context.Context, operationID string, response json.RawMessage, operationError string) error {
	return l.store.CompleteToolOperation(ctx, l.claim.RunID, l.claim.FencingToken, operationID, response, operationError)
}

func (l *GraphMemoryAgentToolLedger) ExplorationRounds(ctx context.Context) (int, error) {
	return l.store.ExplorationRounds(ctx, l.claim.RunID, l.claim.FencingToken, l.graph)
}

func (l *GraphMemoryAgentToolLedger) RecordViewed(ctx context.Context, nodeIDs []string) error {
	// Graph-qualified so a view on one graph cannot attest a citation on
	// another graph with the same node id.
	qualified := make([]string, len(nodeIDs))
	for i, nodeID := range nodeIDs {
		qualified[i] = l.graph + "|" + nodeID
	}
	return l.store.RecordViewedNodes(ctx, l.claim.RunID, l.claim.FencingToken, qualified)
}

func (l *GraphMemoryAgentToolLedger) Finish(ctx context.Context, operationID, status string, state json.RawMessage, citations []memorygraph.Citation, response json.RawMessage) error {
	inputs := make([]GraphMemoryAgentCitationInput, 0, len(citations))
	for _, citation := range citations {
		inputs = append(inputs, GraphMemoryAgentCitationInput{
			NodeID: citation.NodeID, GraphIdentity: l.graph, GraphVersion: int64(citation.GraphVersion), Level: strconv.Itoa(citation.Level),
			EpistemicStatus: citation.Epistemic, Tags: citation.Tags, Title: citation.Title,
			FirstParagraph: citation.FirstParagraph, Excerpt: citation.Excerpt, ContentHash: citation.ContentHash,
		})
	}
	return l.store.FinishToolOperation(ctx, l.claim.RunID, l.claim.FencingToken, operationID, status, l.consumedSeq, state, inputs, response)
}

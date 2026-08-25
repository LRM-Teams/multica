package service

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// GraphMemoryAgentToolLedger binds the native Graph tool server to one fenced
// PostgreSQL run claim. It contains no ambient workspace authority.
type GraphMemoryAgentToolLedger struct {
	store       *GraphMemoryAgentRunStore
	claim       GraphMemoryAgentRunClaim
	consumedSeq int64
}

func NewGraphMemoryAgentToolLedger(store *GraphMemoryAgentRunStore, claim GraphMemoryAgentRunClaim, consumedSeq int64) *GraphMemoryAgentToolLedger {
	if claim.TargetSeq > consumedSeq {
		consumedSeq = claim.TargetSeq
	}
	return &GraphMemoryAgentToolLedger{store: store, claim: claim, consumedSeq: consumedSeq}
}

func (l *GraphMemoryAgentToolLedger) TrajectoryID() string { return l.claim.TrajectoryID }

func (l *GraphMemoryAgentToolLedger) ValidateOperation(ctx context.Context, operation, key string, request json.RawMessage) error {
	return l.store.ValidateToolOperationQuota(ctx, l.claim.RunID, l.claim.FencingToken, operation, key, request)
}
func (l *GraphMemoryAgentToolLedger) Reserve(ctx context.Context, key, operation string, request json.RawMessage) (memorygraph.AgentToolOperationReservation, error) {
	reservation, err := l.store.ReserveToolOperation(ctx, l.claim.RunID, l.claim.FencingToken, key, operation, request)
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
	return l.store.ExplorationRounds(ctx, l.claim.RunID, l.claim.FencingToken)
}

func (l *GraphMemoryAgentToolLedger) RecordViewed(ctx context.Context, nodeIDs []string) error {
	return l.store.RecordViewedNodes(ctx, l.claim.RunID, l.claim.FencingToken, nodeIDs)
}

func (l *GraphMemoryAgentToolLedger) Finish(ctx context.Context, operationID, status string, state json.RawMessage, citations []memorygraph.Citation, response json.RawMessage) error {
	inputs := make([]GraphMemoryAgentCitationInput, 0, len(citations))
	for _, citation := range citations {
		inputs = append(inputs, GraphMemoryAgentCitationInput{
			NodeID: citation.NodeID, GraphVersion: int64(citation.GraphVersion), Level: strconv.Itoa(citation.Level),
			EpistemicStatus: citation.Epistemic, Tags: citation.Tags, Title: citation.Title,
			FirstParagraph: citation.FirstParagraph, Excerpt: citation.Excerpt, ContentHash: citation.ContentHash,
		})
	}
	return l.store.FinishToolOperation(ctx, l.claim.RunID, l.claim.FencingToken, operationID, status, l.consumedSeq, state, inputs, response)
}

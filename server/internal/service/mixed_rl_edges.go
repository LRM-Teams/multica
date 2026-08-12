package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// finalizeMixedRLCausalEdges derives channel_message, reaction, and
// session_continuation edges from provisional capture evidence. Ownership and
// consumptions are already durable; freeze is the moment edges become part of
// the immutable snapshot.
func finalizeMixedRLCausalEdges(ctx context.Context, qtx *db.Queries, ledger *ProviderCallLedger, runID pgtype.UUID) error {
	existing, err := qtx.ListMixedRLCausalEdgesCanonical(ctx, runID)
	if err != nil {
		return err
	}
	existingKeys := make(map[string]struct{}, len(existing))
	for _, edge := range existing {
		existingKeys[mixedRLEdgeKey(edge.Type, edge.SrcSegmentID, edge.DstSegmentID, mixedRLTextValue(edge.DstCallID), edge.TriggerMessageID)] = struct{}{}
	}
	consumptions, err := qtx.ListMixedRLMessageConsumptions(ctx, runID)
	if err != nil {
		return err
	}
	associations, err := qtx.ListMixedRLSegmentCallsCanonical(ctx, runID)
	if err != nil {
		return err
	}
	ownedByCall := make(map[string]string, len(associations))
	for _, association := range associations {
		if association.AssociationKind == "owned" {
			ownedByCall[association.ProviderCallID] = association.SegmentID
		}
	}
	segments, err := qtx.ListMixedRLRunSegmentsCanonical(ctx, runID)
	if err != nil {
		return err
	}
	segmentByID := make(map[string]db.InteractionDagRunSegment, len(segments))
	for _, segment := range segments {
		segmentByID[segment.SegmentID] = segment
	}
	calls, err := qtx.ListMixedRLProviderCallsCanonical(ctx, runID)
	if err != nil {
		return err
	}
	nextOrdinal := int64(len(existing))
	insertEdge := func(input CausalEdgeInput) error {
		key := mixedRLEdgeKey(input.Type, input.SourceSegmentID, input.DestinationSegmentID, input.DestinationCallID, input.TriggerMessageID)
		if _, exists := existingKeys[key]; exists {
			return nil
		}
		nextOrdinal++
		input.EdgeOrdinal = nextOrdinal
		if !input.EdgeID.Valid {
			input.EdgeID = pgtype.UUID{Bytes: uuid.NewSHA1(uuid.NameSpaceURL, []byte(
				runID.String()+":"+key,
			)), Valid: true}
		}
		input.RunID = runID
		if _, err := ledger.InsertCausalEdge(ctx, input); err != nil {
			return err
		}
		existingKeys[key] = struct{}{}
		return nil
	}

	for _, consumption := range consumptions {
		dstSegment := ownedByCall[consumption.EffectiveFromCallID]
		if dstSegment == "" {
			continue
		}
		srcSegment := "message:" + consumption.ChannelMessageID.String()
		if _, ok := segmentByID[srcSegment]; !ok {
			continue
		}
		if err := insertEdge(CausalEdgeInput{
			SourceSegmentID: srcSegment, DestinationSegmentID: dstSegment,
			Type: "channel_message", TriggerMessageID: consumption.ChannelMessageID,
			DestinationCallID: consumption.EffectiveFromCallID,
		}); err != nil {
			return err
		}
	}

	for _, segment := range segments {
		if segment.Kind != "reaction" || !segment.CanonicalActionID.Valid {
			continue
		}
		reactedMessageID, err := qtx.GetChannelMessageReactionMessageID(ctx, segment.CanonicalActionID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		srcSegment := "message:" + reactedMessageID.String()
		if _, ok := segmentByID[srcSegment]; !ok {
			continue
		}
		if err := insertEdge(CausalEdgeInput{
			SourceSegmentID: srcSegment, DestinationSegmentID: segment.SegmentID,
			Type: "reaction", TriggerMessageID: reactedMessageID,
		}); err != nil {
			return err
		}
	}

	type ownedCall struct {
		CallID    string
		SegmentID string
		Ordinal   int64
		RunAgent  pgtype.UUID
		Session   string
	}
	ownedCalls := make([]ownedCall, 0)
	for _, call := range calls {
		segmentID, ok := ownedByCall[call.CallID]
		if !ok {
			continue
		}
		ownedCalls = append(ownedCalls, ownedCall{
			CallID: call.CallID, SegmentID: segmentID, Ordinal: call.CallOrdinal,
			RunAgent: call.RunAgentID, Session: call.PiSessionID,
		})
	}
	seenContinuation := map[string]struct{}{}
	for i := 1; i < len(ownedCalls); i++ {
		prev, cur := ownedCalls[i-1], ownedCalls[i]
		if prev.RunAgent != cur.RunAgent || prev.Session != cur.Session {
			continue
		}
		if prev.SegmentID == cur.SegmentID {
			continue
		}
		if prev.Ordinal+1 != cur.Ordinal {
			continue
		}
		key := prev.SegmentID + "->" + cur.SegmentID
		if _, seen := seenContinuation[key]; seen {
			continue
		}
		seenContinuation[key] = struct{}{}
		if err := insertEdge(CausalEdgeInput{
			SourceSegmentID: prev.SegmentID, DestinationSegmentID: cur.SegmentID,
			Type: "session_continuation", DestinationCallID: cur.CallID,
		}); err != nil {
			return fmt.Errorf("session continuation edge: %w", err)
		}
	}
	return nil
}

func mixedRLEdgeKey(edgeType, src, dst, dstCall string, trigger pgtype.UUID) string {
	triggerKey := ""
	if trigger.Valid {
		triggerKey = trigger.String()
	}
	return edgeType + "|" + src + "|" + dst + "|" + dstCall + "|" + triggerKey
}

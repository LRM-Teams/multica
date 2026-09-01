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
// the immutable snapshot. Endpoints resolve through the canonical projection
// identity maps — never through legacy segment-id string conventions — and an
// edge whose trigger message has exactly one matching canonical Edge carries
// that mapping for provenance.
func finalizeMixedRLCausalEdges(ctx context.Context, qtx *db.Queries, runID pgtype.UUID, projected ProjectedRunSnapshot) error {
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
	// On canonical stores the projection already loaded the run's frozen rows
	// with their mapping column; pre-454 repository fixtures fall back to the
	// migration 315 listing, which cannot carry a mapping.
	segments, err := mixedRLEdgeSegments(ctx, qtx, runID, projected)
	if err != nil {
		return err
	}
	segmentByID := make(map[string]mixedRLEdgeSegment, len(segments))
	for _, segment := range segments {
		segmentByID[segment.SegmentID] = segment
	}
	calls, err := qtx.ListMixedRLProviderCallsCanonical(ctx, runID)
	if err != nil {
		return err
	}
	nextOrdinal := int64(len(existing))
	insertEdge := func(edgeType, sourceSegmentID, destinationSegmentID string, trigger pgtype.UUID, destinationCallID string) error {
		key := mixedRLEdgeKey(edgeType, sourceSegmentID, destinationSegmentID, destinationCallID, trigger)
		if _, exists := existingKeys[key]; exists {
			return nil
		}
		nextOrdinal++
		var universalEdgeID pgtype.Int8
		if projected.StorePresent && trigger.Valid {
			if source, ok := segmentByID[sourceSegmentID]; ok && source.UniversalSegmentID.Valid {
				candidates, err := qtx.FindUniversalDAGEdgesByTriggerAndDestination(ctx, db.FindUniversalDAGEdgesByTriggerAndDestinationParams{
					WorkspaceID:      projected.WorkspaceID,
					TriggerMessageID: trigger,
					DstSegmentID:     source.UniversalSegmentID.String,
				})
				if err != nil {
					return err
				}
				// Ambiguous provenance stays unmapped rather than guessed.
				if len(candidates) == 1 {
					universalEdgeID = pgtype.Int8{Int64: candidates[0], Valid: true}
				}
			}
		}
		edgeID := pgtype.UUID{Bytes: uuid.NewSHA1(uuid.NameSpaceURL, []byte(
			runID.String()+":"+key,
		)), Valid: true}
		if projected.StorePresent {
			if _, err := qtx.InsertMixedRLCausalEdgeWithUniversal(ctx, db.InsertMixedRLCausalEdgeWithUniversalParams{
				EdgeID: edgeID, RunID: runID,
				SrcSegmentID: sourceSegmentID, DstSegmentID: destinationSegmentID,
				Type: edgeType, TriggerMessageID: trigger,
				DstCallID: destinationCallID, EdgeOrdinal: nextOrdinal,
				UniversalEdgeID: universalEdgeID,
			}); err != nil {
				return err
			}
		} else if _, err := qtx.InsertMixedRLCausalEdge(ctx, db.InsertMixedRLCausalEdgeParams{
			EdgeID: edgeID, RunID: runID,
			SrcSegmentID: sourceSegmentID, DstSegmentID: destinationSegmentID,
			Type: edgeType, TriggerMessageID: trigger,
			DstCallID: destinationCallID, EdgeOrdinal: nextOrdinal,
		}); err != nil {
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
		srcSegment, ok := projected.ByCanonicalID[consumption.ChannelMessageID]
		if !ok {
			continue
		}
		if err := insertEdge("channel_message", srcSegment, dstSegment, consumption.ChannelMessageID, consumption.EffectiveFromCallID); err != nil {
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
		srcSegment, ok := projected.ByCanonicalID[reactedMessageID]
		if !ok {
			continue
		}
		if err := insertEdge("reaction", srcSegment, segment.SegmentID, reactedMessageID, ""); err != nil {
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
		prevSeg, okPrev := segmentByID[prev.SegmentID]
		curSeg, okCur := segmentByID[cur.SegmentID]
		// Terminal ownership is a freeze-time persistence artifact, not a
		// session-continuation cause. Only message segments participate.
		if !okPrev || !okCur || prevSeg.Kind != "message" || curSeg.Kind != "message" {
			continue
		}
		key := prev.SegmentID + "->" + cur.SegmentID
		if _, seen := seenContinuation[key]; seen {
			continue
		}
		seenContinuation[key] = struct{}{}
		if err := insertEdge("session_continuation", prev.SegmentID, cur.SegmentID, pgtype.UUID{}, cur.CallID); err != nil {
			return fmt.Errorf("session continuation edge: %w", err)
		}
	}
	return nil
}

// mixedRLEdgeSegment is the migration 315 view of one frozen run segment the
// edge finalizer needs, with the canonical mapping when the store provides it.
type mixedRLEdgeSegment struct {
	SegmentID          string
	Kind               string
	CanonicalActionID  pgtype.UUID
	UniversalSegmentID pgtype.Text
}

func mixedRLEdgeSegments(ctx context.Context, qtx *db.Queries, runID pgtype.UUID, projected ProjectedRunSnapshot) ([]mixedRLEdgeSegment, error) {
	if projected.StorePresent {
		out := make([]mixedRLEdgeSegment, 0, len(projected.Segments))
		for _, row := range projected.Segments {
			out = append(out, mixedRLEdgeSegment{
				SegmentID: row.SegmentID, Kind: row.Kind,
				CanonicalActionID: row.CanonicalActionID, UniversalSegmentID: row.UniversalSegmentID,
			})
		}
		return out, nil
	}
	rows, err := qtx.ListMixedRLRunSegmentsCanonical(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]mixedRLEdgeSegment, 0, len(rows))
	for _, row := range rows {
		out = append(out, mixedRLEdgeSegment{
			SegmentID: row.SegmentID, Kind: row.Kind, CanonicalActionID: row.CanonicalActionID,
		})
	}
	return out, nil
}

func mixedRLEdgeKey(edgeType, src, dst, dstCall string, trigger pgtype.UUID) string {
	triggerKey := ""
	if trigger.Valid {
		triggerKey = trigger.String()
	}
	return edgeType + "|" + src + "|" + dst + "|" + dstCall + "|" + triggerKey
}

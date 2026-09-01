package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrDAGProjectionCapturePending  = errors.New("universal DAG projection capture is pending")
	ErrDAGProjectionCaptureConflict = errors.New("universal DAG projection capture is conflicted")
	ErrDAGProjectionCaptureMissing  = errors.New("universal DAG projection capture is missing its owned provider call")
	ErrDAGProjectionMismatch        = errors.New("universal DAG projection mismatch")
)

// ProjectedRunSnapshot is the deterministic image of one dispatch run's
// canonical Universal DAG inside the frozen-snapshot projection tables. The
// identity maps are what later freeze stages (terminal ownership, causal
// edges) resolve endpoints through, so no stage re-derives facts on its own.
type ProjectedRunSnapshot struct {
	WorkspaceID pgtype.UUID
	RunID       pgtype.UUID
	// Segments holds every frozen row of the run after projection, including
	// healed legacy rows and pre-existing synthetic terminal buckets.
	Segments []db.InteractionDagRunSegment
	// StorePresent reports whether the canonical Universal DAG store with its
	// migration 465 projection mapping exists in this database. When false the
	// projection is a no-op and freeze keeps the migration 315 semantics.
	StorePresent bool
	// ByCanonicalID maps a canonical visible-action id to its frozen row id.
	ByCanonicalID map[pgtype.UUID]string
	// ByUniversalID maps a canonical Universal Segment id to its frozen row id.
	ByUniversalID map[string]string
	// TerminalByRunAgent maps a run agent to the frozen row id of its terminal
	// segment, whether projected from a canonical terminal close or adopted
	// from a legacy synthetic bucket.
	TerminalByRunAgent map[pgtype.UUID]string
}

// UniversalDAGFrozenProjector derives the Mixed-RL frozen run projection
// from the canonical Universal DAG. It is the only writer of frozen run
// segments, associations, and causal edges for canonical runs: capture paths
// record canonical facts first and call the projector in the same
// transaction, and freeze runs it again before any count, hash, or snapshot
// publication. The projection reads structured fields only — close action
// kind, canonical action identity, and provider-call associations — and never
// parses trajectory text.
type UniversalDAGFrozenProjector struct{}

func NewUniversalDAGFrozenProjector() *UniversalDAGFrozenProjector {
	return &UniversalDAGFrozenProjector{}
}

// ProjectRunSnapshot gates, heals, and projects one run inside the caller's
// transaction. It is idempotent: repeated passes converge on the same rows.
// Any pending, conflicted, or owned-less expected capture and any frozen row
// the canonical store cannot confirm fail closed with a projection error —
// there is no fallback to an independent writer.
func (p *UniversalDAGFrozenProjector) ProjectRunSnapshot(
	ctx context.Context,
	qtx *db.Queries,
	workspaceID, runID pgtype.UUID,
) (ProjectedRunSnapshot, error) {
	if p == nil || qtx == nil {
		return ProjectedRunSnapshot{}, errors.New("frozen projector requires queries")
	}
	out := ProjectedRunSnapshot{
		WorkspaceID: workspaceID, RunID: runID,
		ByCanonicalID:      make(map[pgtype.UUID]string),
		ByUniversalID:      make(map[string]string),
		TerminalByRunAgent: make(map[pgtype.UUID]string),
	}
	// Pre-454 repository fixtures have no canonical store; their frozen
	// snapshots remain governed by the migration 315 semantics alone.
	storePresent, err := qtx.UniversalDAGStorePresent(ctx)
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	out.StorePresent = storePresent
	if !storePresent {
		// Pre-454 repository fixtures have no canonical store, so projection
		// writes nothing. ByCanonicalID still describes the run's existing
		// frozen rows so edge derivation resolves endpoints exactly as the
		// migration 315 semantics did; terminal bucket handling stays with
		// the freeze stage on those schemas.
		rows, err := qtx.ListMixedRLRunSegmentsCanonical(ctx, runID)
		if err != nil {
			return ProjectedRunSnapshot{}, err
		}
		for _, row := range rows {
			if row.CanonicalActionID.Valid {
				out.ByCanonicalID[row.CanonicalActionID] = row.SegmentID
			}
		}
		return out, nil
	}

	universal, err := qtx.ListUniversalDAGSegmentsByRun(ctx, db.ListUniversalDAGSegmentsByRunParams{
		WorkspaceID: workspaceID, RunID: runID,
	})
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	links, err := qtx.ListUniversalDAGProviderCallLinksByRun(ctx, runID)
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	ownedLinks := make(map[string]bool, len(links))
	for _, link := range links {
		if link.Role == "owned" {
			ownedLinks[link.SegmentID] = true
		}
	}

	// Capture gates run before any projection write: an expected capture must
	// be settled and carry its owned provider call.
	universalByID := make(map[string]db.InteractionDagSegment, len(universal))
	universalByCanonical := make(map[pgtype.UUID]db.InteractionDagSegment, len(universal))
	for _, segment := range universal {
		universalByID[segment.SegmentID] = segment
		if segment.CanonicalActionID.Valid {
			universalByCanonical[segment.CanonicalActionID] = segment
		}
		switch segment.ProviderCaptureStatus {
		case "pending":
			return ProjectedRunSnapshot{}, fmt.Errorf("%w: segment %s", ErrDAGProjectionCapturePending, segment.SegmentID)
		case "conflict":
			return ProjectedRunSnapshot{}, fmt.Errorf("%w: segment %s", ErrDAGProjectionCaptureConflict, segment.SegmentID)
		case "finalized":
			if !ownedLinks[segment.SegmentID] {
				return ProjectedRunSnapshot{}, fmt.Errorf("%w: segment %s", ErrDAGProjectionCaptureMissing, segment.SegmentID)
			}
		case "not_expected":
		default:
			return ProjectedRunSnapshot{}, fmt.Errorf("%w: unknown capture status %q on segment %s",
				ErrDAGProjectionMismatch, segment.ProviderCaptureStatus, segment.SegmentID)
		}
		if !segment.CloseActionKind.Valid {
			return ProjectedRunSnapshot{}, fmt.Errorf("%w: segment %s has no close action kind",
				ErrDAGProjectionMismatch, segment.SegmentID)
		}
	}

	// Existing frozen rows: mapped rows must stay inside this run's canonical
	// set; unmapped rows with a canonical action are healed onto the canonical
	// segment describing the same fact, or rejected as dual-write residue.
	existing, err := qtx.ListMixedRLRunSegmentsWithMapping(ctx, runID)
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	existingByCanonical := make(map[pgtype.UUID][]db.InteractionDagRunSegment)
	existingTerminalByAgent := make(map[pgtype.UUID]db.InteractionDagRunSegment)
	frozenByUniversal := make(map[string]string, len(existing))
	for _, row := range existing {
		if row.UniversalSegmentID.Valid {
			mapped := row.UniversalSegmentID.String
			canonical, ok := universalByID[mapped]
			if !ok {
				return ProjectedRunSnapshot{}, fmt.Errorf(
					"%w: frozen row %s maps to segment %s outside the run's canonical set",
					ErrDAGProjectionMismatch, row.SegmentID, mapped)
			}
			if canonical.RunAgentID != row.RunAgentID {
				return ProjectedRunSnapshot{}, fmt.Errorf(
					"%w: frozen row %s and canonical segment %s disagree on the run agent",
					ErrDAGProjectionMismatch, row.SegmentID, mapped)
			}
			frozenByUniversal[mapped] = row.SegmentID
			continue
		}
		if row.CanonicalActionID.Valid {
			existingByCanonical[row.CanonicalActionID] = append(existingByCanonical[row.CanonicalActionID], row)
		} else if row.Kind == "terminal" {
			if _, seen := existingTerminalByAgent[row.RunAgentID]; seen {
				return ProjectedRunSnapshot{}, fmt.Errorf(
					"%w: run agent has more than one unmapped terminal row", ErrDAGProjectionMismatch)
			}
			existingTerminalByAgent[row.RunAgentID] = row
		}
	}
	for canonicalID, rows := range existingByCanonical {
		canonical, ok := universalByCanonical[canonicalID]
		if !ok {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: frozen row %s claims canonical action %s the canonical store never recorded for this run",
				ErrDAGProjectionMismatch, rows[0].SegmentID, canonicalID)
		}
		if len(rows) > 1 {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: canonical action %s claimed by multiple frozen rows", ErrDAGProjectionMismatch, canonicalID)
		}
		row := rows[0]
		if row.Kind != canonical.CloseActionKind.String || row.RunAgentID != canonical.RunAgentID {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: frozen row %s disagrees with canonical segment %s on kind or run agent",
				ErrDAGProjectionMismatch, row.SegmentID, canonical.SegmentID)
		}
		affected, err := qtx.SetMixedRLRunSegmentUniversalMapping(ctx, db.SetMixedRLRunSegmentUniversalMappingParams{
			RunID: runID, SegmentID: row.SegmentID, UniversalSegmentID: text(canonical.SegmentID),
		})
		if err != nil {
			return ProjectedRunSnapshot{}, err
		}
		if affected != 1 {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: frozen row %s could not adopt canonical segment %s",
				ErrDAGProjectionMismatch, row.SegmentID, canonical.SegmentID)
		}
		frozenByUniversal[canonical.SegmentID] = row.SegmentID
	}

	// Projection: canonical segments become frozen rows under their canonical
	// identity, in the deterministic canonical order. metadata-only closures
	// carry no Mixed-RL projection and are skipped.
	nextOrdinal := int64(len(existing))
	for _, canonical := range universal {
		kind := canonical.CloseActionKind.String
		if kind == string(DAGCloseMetadataOnly) {
			continue
		}
		if kind != "message" && kind != "reaction" && kind != "terminal" {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: segment %s has unprojectable close action kind %q",
				ErrDAGProjectionMismatch, canonical.SegmentID, kind)
		}
		// Migration 315 freezes the action-shape rule: message and reaction
		// segments carry a canonical action, terminal segments never do.
		if (kind == "message" || kind == "reaction") && !canonical.CanonicalActionID.Valid {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: %s segment %s has no canonical action id",
				ErrDAGProjectionMismatch, kind, canonical.SegmentID)
		}
		if kind == "terminal" && canonical.CanonicalActionID.Valid {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: terminal segment %s carries a canonical action id",
				ErrDAGProjectionMismatch, canonical.SegmentID)
		}

		frozenID := frozenByUniversal[canonical.SegmentID]
		if frozenID == "" && kind == "terminal" {
			// A legacy synthetic terminal bucket for the same run agent adopts
			// the canonical terminal close instead of a second terminal row.
			if row, ok := existingTerminalByAgent[canonical.RunAgentID]; ok {
				affected, err := qtx.SetMixedRLRunSegmentUniversalMapping(ctx, db.SetMixedRLRunSegmentUniversalMappingParams{
					RunID: runID, SegmentID: row.SegmentID, UniversalSegmentID: text(canonical.SegmentID),
				})
				if err != nil {
					return ProjectedRunSnapshot{}, err
				}
				if affected != 1 {
					return ProjectedRunSnapshot{}, fmt.Errorf(
						"%w: terminal bucket %s could not adopt canonical segment %s",
						ErrDAGProjectionMismatch, row.SegmentID, canonical.SegmentID)
				}
				frozenID = row.SegmentID
			}
		}
		if frozenID == "" {
			nextOrdinal++
			row, err := qtx.UpsertMixedRLRunSegmentWithMapping(ctx, db.UpsertMixedRLRunSegmentWithMappingParams{
				SegmentID: canonical.SegmentID, RunID: runID, RunAgentID: canonical.RunAgentID,
				Kind: kind, CanonicalActionID: canonical.CanonicalActionID,
				SegmentOrdinal: nextOrdinal, ProvisionalAt: timestamptz(time.Now().UTC()),
				UniversalSegmentID: text(canonical.SegmentID),
			})
			if err != nil {
				return ProjectedRunSnapshot{}, err
			}
			frozenID = row.SegmentID
		}
		out.ByUniversalID[canonical.SegmentID] = frozenID
		if canonical.CanonicalActionID.Valid {
			out.ByCanonicalID[canonical.CanonicalActionID] = frozenID
		}
		if kind == "terminal" {
			out.TerminalByRunAgent[canonical.RunAgentID] = frozenID
		}
	}
	// A legacy terminal bucket without a canonical terminal close still names
	// its run agent's terminal row for the freeze stages that follow.
	for agent, row := range existingTerminalByAgent {
		if _, ok := out.TerminalByRunAgent[agent]; !ok {
			out.TerminalByRunAgent[agent] = row.SegmentID
		}
	}

	// Association mirroring: every canonical provider-call link of the run is
	// projected onto its segment's frozen row; drift fails closed. Owned links
	// are projected first so a shared_producer link always finds its same-run
	// owner already in place, matching the association guard's expectation.
	projectionRoleRank := map[string]int{"owned": 0, "shared_producer": 1, "audit": 2}
	orderedLinks := append([]db.InteractionDagUniversalProviderCall(nil), links...)
	sort.SliceStable(orderedLinks, func(i, j int) bool {
		return projectionRoleRank[orderedLinks[i].Role] < projectionRoleRank[orderedLinks[j].Role]
	})
	for _, link := range orderedLinks {
		frozenID, ok := out.ByUniversalID[link.SegmentID]
		if !ok {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: provider call %s links to segment %s outside the projected set",
				ErrDAGProjectionMismatch, link.ProviderCallID, link.SegmentID)
		}
		affected, err := qtx.AssociateMixedRLProviderCallIdempotent(ctx, db.AssociateMixedRLProviderCallIdempotentParams{
			SegmentID: frozenID, ProviderCallID: link.ProviderCallID,
			CallOrdinal: link.Ordinal, AssociationKind: link.Role,
		})
		if err != nil {
			return ProjectedRunSnapshot{}, err
		}
		if affected != 1 {
			return ProjectedRunSnapshot{}, fmt.Errorf(
				"%w: provider call %s association on %s drifted from the canonical link",
				ErrDAGProjectionMismatch, link.ProviderCallID, frozenID)
		}
	}

	rows, err := qtx.ListMixedRLRunSegmentsWithMapping(ctx, runID)
	if err != nil {
		return ProjectedRunSnapshot{}, err
	}
	out.Segments = rows
	return out, nil
}

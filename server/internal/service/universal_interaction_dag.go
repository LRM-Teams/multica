// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type DAGBoundaryKind string

const (
	DAGBoundaryInbound  DAGBoundaryKind = "inbound"
	DAGBoundaryVisible  DAGBoundaryKind = "visible"
	DAGBoundaryTerminal DAGBoundaryKind = "terminal"
)

type DAGCloseActionKind string

const (
	DAGCloseMessage      DAGCloseActionKind = "message"
	DAGCloseReaction     DAGCloseActionKind = "reaction"
	DAGCloseTerminal     DAGCloseActionKind = "terminal"
	DAGCloseMetadataOnly DAGCloseActionKind = "metadata_only"
)

type DAGBoundaryInput struct {
	WorkspaceID                   pgtype.UUID
	Task                          db.AgentInboxEvent
	BoundaryKind                  DAGBoundaryKind
	CloseActionKind               DAGCloseActionKind
	EndSeq                        int32
	ActionID                      pgtype.UUID
	ActionKey                     string
	ProjectID                     pgtype.UUID
	ChannelID                     pgtype.UUID
	RouteGeneration               int64
	MemoryTypeAtEvent             string
	RunID                         pgtype.UUID
	RunAgentID                    pgtype.UUID
	ProviderCaptureExpected       bool
	ProviderCaptureCorrelationKey string
	Derivative                    bool
}

type DAGLinkageInput struct {
	WorkspaceID     pgtype.UUID
	SourceSegmentID string
	TargetRunID     pgtype.UUID
	Type            string
	DurableEventID  pgtype.UUID
}

type DAGBoundaryResult struct {
	SegmentID  string
	Generation int64
	Closed     bool
	StartSeq   int32
	EndSeq     int32
}

// ProviderCallAssociation is the trusted capture identity attached to one
// canonical Segment. CaptureVersion and CorrelationKey must be identical for
// every association supplied in one attachment.
type ProviderCallAssociation struct {
	ProviderCallID string
	Role           string
	Ordinal        int64
	RunID          pgtype.UUID
	RunAgentID     pgtype.UUID
	CaptureVersion int64
	CorrelationKey string
}

type UniversalInteractionDAG struct{}

var (
	ErrDAGSequenceGap             = errors.New("universal interaction DAG sequence gap")
	ErrDAGBoundaryConflict        = errors.New("universal interaction DAG boundary conflict")
	ErrDAGProviderCaptureConflict = errors.New("universal interaction DAG provider capture conflict")
)

const (
	universalDAGSanitizerVersion = "dag-redaction-v1"
	universalDAGPolicyVersion    = "universal-dag-v1"
)

func NewUniversalInteractionDAG() *UniversalInteractionDAG {
	return &UniversalInteractionDAG{}
}

func (d *UniversalInteractionDAG) RecordBoundaryTx(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	in DAGBoundaryInput,
) (DAGBoundaryResult, error) {
	if d == nil || q == nil || tx == nil {
		return DAGBoundaryResult{}, errors.New("universal interaction DAG requires an active transaction")
	}
	if !in.WorkspaceID.Valid || !in.Task.ID.Valid || !in.Task.WorkspaceID.Valid {
		return DAGBoundaryResult{}, errors.New("universal interaction DAG requires workspace and task identity")
	}
	if in.WorkspaceID != in.Task.WorkspaceID {
		return DAGBoundaryResult{}, errors.New("universal interaction DAG task belongs to another workspace")
	}
	if strings.TrimSpace(in.MemoryTypeAtEvent) == "" {
		return DAGBoundaryResult{}, errors.New("universal interaction DAG requires memory type provenance")
	}
	if in.RunID.Valid != in.RunAgentID.Valid {
		return DAGBoundaryResult{}, errors.New("universal interaction DAG requires complete run identity")
	}
	if in.BoundaryKind == DAGBoundaryTerminal && strings.TrimSpace(in.ActionKey) == "" {
		in.ActionKey = "terminal:" + in.Task.ID.String()
	}
	correlationKey := strings.TrimSpace(in.ProviderCaptureCorrelationKey)
	if in.ProviderCaptureExpected {
		if correlationKey == "" {
			in.ProviderCaptureCorrelationKey = ""
		} else if !in.RunID.Valid {
			return DAGBoundaryResult{}, errors.New("expected provider capture requires run identity")
		}
	} else if correlationKey != "" {
		return DAGBoundaryResult{}, errors.New("unexpected provider capture correlation key")
	}
	if err := validateDAGBoundaryShape(in); err != nil {
		return DAGBoundaryResult{}, err
	}

	if in.ActionKey != "" {
		if err := q.LockUniversalDAGBoundaryActionKey(ctx, db.LockUniversalDAGBoundaryActionKeyParams{
			WorkspaceID: in.WorkspaceID.String(), VisibleActionKey: in.ActionKey,
		}); err != nil {
			return DAGBoundaryResult{}, fmt.Errorf("lock boundary action: %w", err)
		}
	}
	if err := q.EnsureUniversalDAGTaskCursor(ctx, db.EnsureUniversalDAGTaskCursorParams{
		WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID,
	}); err != nil {
		return DAGBoundaryResult{}, fmt.Errorf("ensure task cursor: %w", err)
	}
	cursor, err := q.LockUniversalDAGTaskCursor(ctx, db.LockUniversalDAGTaskCursorParams{
		WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID,
	})
	if err != nil {
		return DAGBoundaryResult{}, fmt.Errorf("lock task cursor: %w", err)
	}

	if in.ActionKey != "" {
		replayed, err := q.GetUniversalDAGSegmentByVisibleAction(ctx, db.GetUniversalDAGSegmentByVisibleActionParams{
			WorkspaceID:      in.WorkspaceID,
			VisibleActionKey: pgtype.Text{String: in.ActionKey, Valid: true},
		})
		if err == nil {
			return validateDAGBoundaryReplay(in, replayed)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return DAGBoundaryResult{}, fmt.Errorf("resolve boundary replay: %w", err)
		}
	}

	switch in.BoundaryKind {
	case DAGBoundaryInbound:
		return d.recordInboundBoundary(ctx, q, cursor, in)
	case DAGBoundaryVisible:
		return d.closeVisibleBoundary(ctx, q, cursor, in)
	case DAGBoundaryTerminal:
		return d.closeTerminalBoundary(ctx, q, cursor, in)
	default:
		return DAGBoundaryResult{}, fmt.Errorf("unknown DAG boundary kind %q", in.BoundaryKind)
	}
}

func (d *UniversalInteractionDAG) recordInboundBoundary(
	ctx context.Context,
	q *db.Queries,
	cursor db.InteractionDagTaskCursor,
	in DAGBoundaryInput,
) (DAGBoundaryResult, error) {
	expected := cursor.LastClosedSeq + 1
	start := in.EndSeq
	generation := cursor.NextGeneration
	if cursor.OpenGeneration.Valid {
		expected = cursor.OpenEndSeq.Int32 + 1
		start = cursor.OpenStartSeq.Int32
		generation = cursor.OpenGeneration.Int64
	}
	if in.EndSeq != expected {
		return DAGBoundaryResult{}, fmt.Errorf("%w: got %d want %d", ErrDAGSequenceGap, in.EndSeq, expected)
	}
	_, err := q.UpsertUniversalDAGTaskCursor(ctx, db.UpsertUniversalDAGTaskCursorParams{
		WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID,
		NextGeneration: cursor.NextGeneration,
		OpenStartSeq:   pgtype.Int4{Int32: start, Valid: true},
		LastClosedSeq:  cursor.LastClosedSeq,
		OpenGeneration: pgtype.Int8{Int64: generation, Valid: true},
		OpenEndSeq:     pgtype.Int4{Int32: in.EndSeq, Valid: true},
	})
	if err != nil {
		return DAGBoundaryResult{}, fmt.Errorf("advance inbound cursor: %w", err)
	}
	return DAGBoundaryResult{Generation: generation, StartSeq: start, EndSeq: in.EndSeq}, nil
}

func (d *UniversalInteractionDAG) closeVisibleBoundary(
	ctx context.Context,
	q *db.Queries,
	cursor db.InteractionDagTaskCursor,
	in DAGBoundaryInput,
) (DAGBoundaryResult, error) {
	expected := cursor.LastClosedSeq + 1
	start := in.EndSeq
	if cursor.OpenGeneration.Valid {
		expected = cursor.OpenEndSeq.Int32 + 1
		start = cursor.OpenStartSeq.Int32
	}
	if in.EndSeq != expected {
		return DAGBoundaryResult{}, fmt.Errorf("%w: got %d want %d", ErrDAGSequenceGap, in.EndSeq, expected)
	}
	return d.persistClosedBoundary(ctx, q, cursor, in, cursor.NextGeneration, start, in.EndSeq, in.CloseActionKind, in.ActionKey)
}

func (d *UniversalInteractionDAG) closeTerminalBoundary(
	ctx context.Context,
	q *db.Queries,
	cursor db.InteractionDagTaskCursor,
	in DAGBoundaryInput,
) (DAGBoundaryResult, error) {
	if cursor.OpenGeneration.Valid {
		if in.EndSeq != 0 && in.EndSeq != cursor.OpenEndSeq.Int32 {
			return DAGBoundaryResult{}, fmt.Errorf("%w: terminal got %d want %d", ErrDAGSequenceGap, in.EndSeq, cursor.OpenEndSeq.Int32)
		}
		return d.persistClosedBoundary(
			ctx, q, cursor, in, cursor.OpenGeneration.Int64,
			cursor.OpenStartSeq.Int32, cursor.OpenEndSeq.Int32, DAGCloseTerminal, in.ActionKey,
		)
	}

	if cursor.NextGeneration > 1 {
		previous, err := q.GetUniversalDAGSegmentByTaskGeneration(ctx, db.GetUniversalDAGSegmentByTaskGenerationParams{
			WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID, Generation: cursor.NextGeneration - 1,
		})
		if err != nil {
			return DAGBoundaryResult{}, fmt.Errorf("resolve terminal task lifecycle: %w", err)
		}
		return DAGBoundaryResult{
			SegmentID: previous.SegmentID, Generation: previous.Generation,
			Closed: false, StartSeq: previous.StartSeq, EndSeq: previous.EndSeq,
		}, nil
	}

	return d.persistClosedBoundary(
		ctx, q, cursor, in, cursor.NextGeneration, 0, 0, DAGCloseMetadataOnly, "",
	)
}

func (d *UniversalInteractionDAG) persistClosedBoundary(
	ctx context.Context,
	q *db.Queries,
	cursor db.InteractionDagTaskCursor,
	in DAGBoundaryInput,
	generation int64,
	startSeq int32,
	endSeq int32,
	closeKind DAGCloseActionKind,
	actionKey string,
) (DAGBoundaryResult, error) {
	segmentID := universalDAGSegmentID(in.WorkspaceID, in.Task.ID, generation)
	issueID := ""
	if in.Task.IssueID.Valid {
		issueID = in.Task.IssueID.String()
	}
	actionID := in.ActionID
	if closeKind == DAGCloseTerminal || closeKind == DAGCloseMetadataOnly {
		actionID = pgtype.UUID{}
	}
	correlationKey := ""
	if in.ProviderCaptureExpected {
		correlationKey = in.ProviderCaptureCorrelationKey
	}
	routeGeneration := pgtype.Int8{}
	if in.RouteGeneration != 0 {
		routeGeneration = pgtype.Int8{Int64: in.RouteGeneration, Valid: true}
	}
	graphEligible := in.MemoryTypeAtEvent == "graph" && (in.ProjectID.Valid || in.ChannelID.Valid) && !in.Derivative
	inserted, err := q.InsertUniversalDAGSegment(ctx, db.InsertUniversalDAGSegmentParams{
		SegmentID: segmentID, WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID,
		Generation: generation, IssueID: issueID, StartSeq: startSeq, EndSeq: endSeq,
		TrainableEligible: !in.Derivative, ProjectIDAtEvent: in.ProjectID,
		ChannelIDAtEvent: in.ChannelID, RouteGenerationAtEvent: routeGeneration,
		MemoryTypeAtEvent:              in.MemoryTypeAtEvent,
		GraphProjectionEligibleAtEvent: graphEligible,
		CloseActionKind:                string(closeKind), CanonicalActionID: actionID,
		VisibleActionKey: actionKey, Derivative: in.Derivative,
		SanitizerVersion: universalDAGSanitizerVersion, PolicyVersion: universalDAGPolicyVersion,
		ProviderCaptureCorrelationKey: correlationKey, RunID: in.RunID, RunAgentID: in.RunAgentID,
	})
	if err != nil {
		return DAGBoundaryResult{}, fmt.Errorf("insert canonical segment: %w", err)
	}
	if inserted.SegmentID != segmentID || inserted.Generation != generation {
		return DAGBoundaryResult{}, errors.New("canonical segment identity mismatch")
	}
	if _, err := q.InsertUniversalDAGPublishOutbox(ctx, db.InsertUniversalDAGPublishOutboxParams{
		WorkspaceID: in.WorkspaceID, SegmentID: segmentID,
		RequestHash: universalDAGOutboxRequestHash(segmentID),
	}); err != nil {
		return DAGBoundaryResult{}, fmt.Errorf("insert segment publish outbox: %w", err)
	}

	if generation > 1 {
		previous, err := q.GetUniversalDAGSegmentByTaskGeneration(ctx, db.GetUniversalDAGSegmentByTaskGenerationParams{
			WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID, Generation: generation - 1,
		})
		if err != nil {
			return DAGBoundaryResult{}, fmt.Errorf("resolve previous generation: %w", err)
		}
		if _, err := q.InsertUniversalDAGEdgeAtomic(ctx, db.InsertUniversalDAGEdgeAtomicParams{
			WorkspaceID: in.WorkspaceID, SrcSegmentID: previous.SegmentID,
			DstSegmentID: segmentID, EdgeType: EdgeTypeContinues,
		}); err != nil {
			return DAGBoundaryResult{}, fmt.Errorf("insert continues edge: %w", err)
		}
	}

	if _, err := q.UpsertUniversalDAGTaskCursor(ctx, db.UpsertUniversalDAGTaskCursorParams{
		WorkspaceID: in.WorkspaceID, AgentRunID: in.Task.ID,
		NextGeneration: generation + 1, LastClosedSeq: endSeq,
	}); err != nil {
		return DAGBoundaryResult{}, fmt.Errorf("close task cursor: %w", err)
	}
	return DAGBoundaryResult{
		SegmentID: segmentID, Generation: generation, Closed: true,
		StartSeq: startSeq, EndSeq: endSeq,
	}, nil
}

func (d *UniversalInteractionDAG) RecordLinkageTx(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	in DAGLinkageInput,
) error {
	if d == nil || q == nil || tx == nil {
		return errors.New("universal interaction DAG linkage requires an active transaction")
	}
	if !in.WorkspaceID.Valid || !in.TargetRunID.Valid || in.SourceSegmentID == "" {
		return errors.New("universal interaction DAG linkage requires canonical anchors")
	}
	if !validEdgeTypes[in.Type] {
		return fmt.Errorf("%w: %q", ErrInvalidEdgeType, in.Type)
	}
	if in.Type == EdgeTypeContinues && in.DurableEventID.Valid {
		return errors.New("continues linkage cannot have a durable event")
	}

	source, err := q.GetUniversalDAGSegment(ctx, db.GetUniversalDAGSegmentParams{
		WorkspaceID: in.WorkspaceID, SegmentID: in.SourceSegmentID,
	})
	if err != nil {
		return fmt.Errorf("resolve linkage source: %w", err)
	}
	messageTriggered := in.Type != EdgeTypeContinues &&
		source.CloseActionKind.Valid && source.CloseActionKind.String == string(DAGCloseMessage)
	if in.Type != EdgeTypeContinues {
		if messageTriggered && !in.DurableEventID.Valid {
			return errors.New("message-closed linkage requires a durable event")
		}
		if !messageTriggered && in.DurableEventID.Valid {
			return errors.New("non-message-closed linkage cannot have a durable event")
		}
	}

	var target db.InteractionDagSegment
	if in.Type == EdgeTypeContinues {
		if source.AgentRunID != in.TargetRunID {
			return errors.New("continues linkage must stay within one task lifecycle")
		}
		target, err = q.GetUniversalDAGSegmentByTaskGeneration(ctx, db.GetUniversalDAGSegmentByTaskGenerationParams{
			WorkspaceID: in.WorkspaceID, AgentRunID: in.TargetRunID,
			Generation: source.Generation + 1,
		})
	} else {
		target, err = q.GetFirstUniversalDAGSegmentByTask(ctx, db.GetFirstUniversalDAGSegmentByTaskParams{
			WorkspaceID: in.WorkspaceID, AgentRunID: in.TargetRunID,
		})
	}
	if err != nil {
		return fmt.Errorf("resolve linkage target: %w", err)
	}
	triggerID := pgtype.UUID{}
	if messageTriggered {
		resolved, err := q.GetUniversalDAGEdgeTriggerMessageID(ctx, db.GetUniversalDAGEdgeTriggerMessageIDParams{
			WorkspaceID: in.WorkspaceID, SegmentID: in.SourceSegmentID,
		})
		if err != nil {
			return fmt.Errorf("resolve linkage durable event: %w", err)
		}
		if resolved != in.DurableEventID {
			return errors.New("linkage durable event is not the source closing message")
		}
		triggerID = resolved
	}
	lockTrigger := pgtype.Text{}
	if triggerID.Valid {
		lockTrigger = pgtype.Text{String: triggerID.String(), Valid: true}
	}
	if err := q.LockUniversalDAGEdgeIdentity(ctx, db.LockUniversalDAGEdgeIdentityParams{
		WorkspaceID: in.WorkspaceID.String(), SrcSegmentID: in.SourceSegmentID,
		DstSegmentID: target.SegmentID, EdgeType: in.Type, TriggerMessageID: lockTrigger,
	}); err != nil {
		return fmt.Errorf("lock linkage identity: %w", err)
	}
	_, err = q.GetUniversalDAGEdgeByIdentity(ctx, db.GetUniversalDAGEdgeByIdentityParams{
		WorkspaceID: in.WorkspaceID, SrcSegmentID: in.SourceSegmentID,
		DstSegmentID: target.SegmentID, EdgeType: in.Type, TriggerMessageID: triggerID,
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("resolve linkage replay: %w", err)
	}

	var inserted db.InteractionDagEdge
	if in.Type == EdgeTypeContinues || triggerID.Valid {
		inserted, err = q.InsertUniversalDAGEdgeAtomic(ctx, db.InsertUniversalDAGEdgeAtomicParams{
			WorkspaceID: in.WorkspaceID, SrcSegmentID: in.SourceSegmentID,
			DstSegmentID: target.SegmentID, EdgeType: in.Type,
		})
	} else {
		var edgeSeq int64
		edgeSeq, err = q.AllocateUniversalDAGEdgeSeq(ctx, in.WorkspaceID)
		if err == nil {
			inserted, err = q.InsertUniversalDAGEdge(ctx, db.InsertUniversalDAGEdgeParams{
				WorkspaceID: in.WorkspaceID, EdgeSeq: edgeSeq,
				SrcSegmentID: in.SourceSegmentID, DstSegmentID: target.SegmentID,
				EdgeType: in.Type, TriggerMessageID: triggerID,
			})
		}
	}
	if err != nil {
		return fmt.Errorf("insert linkage edge: %w", err)
	}
	if inserted.TriggerMessageID != triggerID {
		return errors.New("inserted linkage durable event mismatch")
	}
	return nil
}

// AttachProviderCaptureTx records trusted capture associations and finalizes the
// Segment capture state. ErrDAGProviderCaptureConflict means this transaction
// contains the durable conflict marker; callers must commit that state while
// still treating the capture as unusable.
func (d *UniversalInteractionDAG) AttachProviderCaptureTx(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	segmentID string,
	captureID string,
	calls []ProviderCallAssociation,
) error {
	if d == nil || q == nil || tx == nil {
		return errors.New("provider capture attachment requires an active transaction")
	}
	if segmentID == "" || strings.TrimSpace(captureID) == "" || len(calls) == 0 {
		return errors.New("provider capture attachment requires segment, capture, and calls")
	}
	first := calls[0]
	if first.CaptureVersion <= 0 || strings.TrimSpace(first.CorrelationKey) == "" {
		return errors.New("provider capture attachment requires version and correlation")
	}
	for _, call := range calls {
		if call.ProviderCallID == "" || call.Ordinal <= 0 || !call.RunID.Valid || !call.RunAgentID.Valid {
			return errors.New("provider call association identity is incomplete")
		}
		if call.Role != "owned" && call.Role != "shared_producer" && call.Role != "audit" {
			return fmt.Errorf("invalid provider call association role %q", call.Role)
		}
		if call.CaptureVersion != first.CaptureVersion || call.CorrelationKey != first.CorrelationKey || call.RunID != first.RunID || call.RunAgentID != first.RunAgentID {
			return errors.New("provider call associations disagree on capture identity")
		}
	}

	segment, err := q.LockUniversalDAGSegmentForProviderCapture(ctx, segmentID)
	if err != nil {
		return fmt.Errorf("lock provider capture segment: %w", err)
	}
	if segment.ProviderCaptureStatus == "not_expected" {
		return errors.New("provider capture was not expected for this segment")
	}
	if segment.ProviderCaptureStatus == "conflict" {
		return ErrDAGProviderCaptureConflict
	}
	if segment.RunID != first.RunID || segment.RunAgentID != first.RunAgentID {
		return errors.New("provider capture run identity does not match segment")
	}
	storedCorrelation := segment.ProviderCaptureCorrelationKey.String
	if storedCorrelation != first.CorrelationKey {
		if err := markUniversalDAGProviderConflict(ctx, q, segment, captureID, first.CaptureVersion); err != nil {
			return err
		}
		return ErrDAGProviderCaptureConflict
	}
	if segment.ProviderCaptureStatus == "finalized" &&
		(segment.ProviderCaptureID.String != captureID || segment.ProviderCaptureVersion.Int64 != first.CaptureVersion) {
		if err := markUniversalDAGProviderConflict(ctx, q, segment, captureID, first.CaptureVersion); err != nil {
			return err
		}
		return ErrDAGProviderCaptureConflict
	}
	if segment.ProviderCaptureStatus == "pending" {
		if _, err := q.FinalizeUniversalDAGProviderCapture(ctx, db.FinalizeUniversalDAGProviderCaptureParams{
			CaptureID: captureID, CaptureVersion: first.CaptureVersion,
			WorkspaceID: segment.WorkspaceID, SegmentID: segmentID,
			ProviderCaptureCorrelationKey: storedCorrelation,
		}); err != nil {
			return fmt.Errorf("finalize provider capture: %w", err)
		}
	}
	for _, call := range calls {
		_, err := q.InsertUniversalDAGProviderCallLink(ctx, db.InsertUniversalDAGProviderCallLinkParams{
			SegmentID: segmentID, ProviderCallID: call.ProviderCallID,
			Role: call.Role, Ordinal: call.Ordinal, RunID: call.RunID,
			RunAgentID: call.RunAgentID, CaptureID: captureID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			if markErr := markUniversalDAGProviderConflict(ctx, q, segment, captureID, first.CaptureVersion); markErr != nil {
				return markErr
			}
			return ErrDAGProviderCaptureConflict
		}
		if err != nil {
			return fmt.Errorf("insert provider call association: %w", err)
		}
	}
	return nil
}

func markUniversalDAGProviderConflict(
	ctx context.Context,
	q *db.Queries,
	segment db.InteractionDagSegment,
	captureID string,
	captureVersion int64,
) error {
	if err := q.MarkUniversalDAGProviderCaptureConflict(ctx, db.MarkUniversalDAGProviderCaptureConflictParams{
		CaptureID: captureID, CaptureVersion: captureVersion,
		WorkspaceID: segment.WorkspaceID, SegmentID: segment.SegmentID,
		ProviderCaptureCorrelationKey: segment.ProviderCaptureCorrelationKey.String,
	}); err != nil {
		return fmt.Errorf("mark provider capture conflict: %w", err)
	}
	return nil
}

func validateDAGBoundaryShape(in DAGBoundaryInput) error {
	switch in.BoundaryKind {
	case DAGBoundaryInbound:
		if in.CloseActionKind != "" || in.ActionID.Valid || in.ActionKey != "" || in.EndSeq <= 0 {
			return errors.New("inbound boundary cannot carry a close action")
		}
	case DAGBoundaryVisible:
		if in.CloseActionKind != DAGCloseMessage && in.CloseActionKind != DAGCloseReaction {
			return errors.New("visible boundary requires message or reaction close")
		}
		if !in.ActionID.Valid || strings.TrimSpace(in.ActionKey) == "" || in.EndSeq <= 0 {
			return errors.New("visible boundary requires canonical action identity")
		}
	case DAGBoundaryTerminal:
		if in.CloseActionKind != DAGCloseTerminal && in.CloseActionKind != DAGCloseMetadataOnly {
			return errors.New("terminal boundary requires terminal close metadata")
		}
		if in.ActionID.Valid || in.EndSeq < 0 {
			return errors.New("terminal boundary has invalid action identity")
		}
	default:
		return fmt.Errorf("unknown DAG boundary kind %q", in.BoundaryKind)
	}
	return nil
}

func validateDAGBoundaryReplay(in DAGBoundaryInput, segment db.InteractionDagSegment) (DAGBoundaryResult, error) {
	if segment.AgentRunID != in.Task.ID || !segment.CloseActionKind.Valid {
		return DAGBoundaryResult{}, ErrDAGBoundaryConflict
	}
	kind := DAGCloseActionKind(segment.CloseActionKind.String)
	if in.BoundaryKind == DAGBoundaryVisible {
		if kind != in.CloseActionKind || segment.CanonicalActionID != in.ActionID || segment.EndSeq != in.EndSeq {
			return DAGBoundaryResult{}, ErrDAGBoundaryConflict
		}
	} else if in.BoundaryKind == DAGBoundaryTerminal && kind != DAGCloseTerminal {
		return DAGBoundaryResult{}, ErrDAGBoundaryConflict
	}
	return DAGBoundaryResult{
		SegmentID: segment.SegmentID, Generation: segment.Generation,
		Closed: true, StartSeq: segment.StartSeq, EndSeq: segment.EndSeq,
	}, nil
}

func universalDAGSegmentID(workspaceID, taskID pgtype.UUID, generation int64) string {
	hash := sha256.New()
	hash.Write(workspaceID.Bytes[:])
	hash.Write(taskID.Bytes[:])
	var encodedGeneration [8]byte
	binary.BigEndian.PutUint64(encodedGeneration[:], uint64(generation))
	hash.Write(encodedGeneration[:])
	return hex.EncodeToString(hash.Sum(nil))
}

func universalDAGOutboxRequestHash(segmentID string) string {
	sum := sha256.Sum256([]byte(segmentID))
	return "sha256:" + hex.EncodeToString(sum[:])
}

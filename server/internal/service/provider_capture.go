package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TrustedTurnCapture is the server-side representation of one authenticated,
// complete resident turn upload. The transport boundary is responsible for
// authenticating the runner and proving the run-agent identity; this service
// verifies the persisted hierarchy again before writing any capture data.
type TrustedTurnCapture struct {
	WorkspaceID  pgtype.UUID
	RunID        pgtype.UUID
	RunAgentID   pgtype.UUID
	TurnID       pgtype.UUID
	TurnOrdinal  int64
	Batch        TurnCaptureBatchInput
	Calls        []ProviderCallInput
	Actions      []VisibleActionInput
	Consumptions []MessageConsumptionInput
	CompletedAt  time.Time
	LateEventID  pgtype.UUID
}

type TrustedTurnCaptureResult struct {
	Run        EnvDispatchRunRecord
	Turn       ResidentTurnRecord
	Late       bool
	SnapshotID string
}

// TrustedTurnCaptureGap is a credential-authenticated declaration that a
// resident turn cannot produce a complete provider capture. Unlike an upload,
// it permanently excludes the missing capture from training while retaining a
// durable audit record.
type TrustedTurnCaptureGap struct {
	RunID       pgtype.UUID
	RunAgentID  pgtype.UUID
	TurnID      pgtype.UUID
	TurnOrdinal int64
	Reason      string
	Summary     []byte
	OccurredAt  time.Time
	LateEventID pgtype.UUID
}

// ProviderCaptureService persists a trusted capture batch in one transaction.
// It is deliberately transport-neutral so both the agent credential endpoint
// and the daemon Runner event use the exact same provenance and atomicity
// checks.
type ProviderCaptureService struct {
	queries   *db.Queries
	txStarter TxStarter
}

func NewProviderCaptureService(queries *db.Queries, txStarter TxStarter) *ProviderCaptureService {
	return &ProviderCaptureService{queries: queries, txStarter: txStarter}
}

func (s *ProviderCaptureService) AcceptTrustedTurnCapture(ctx context.Context, input TrustedTurnCapture) (TrustedTurnCaptureResult, error) {
	if err := requireMixedRLQueries(s.queries); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if s.txStarter == nil {
		return TrustedTurnCaptureResult{}, errors.New("transaction starter is required")
	}
	if !input.RunID.Valid || !input.RunAgentID.Valid || !input.TurnID.Valid {
		return TrustedTurnCaptureResult{}, errors.New("run, run-agent, and turn identities are required")
	}
	if input.TurnOrdinal <= 0 {
		return TrustedTurnCaptureResult{}, errors.New("positive turn ordinal is required")
	}
	if input.Batch.TurnID != input.TurnID {
		return TrustedTurnCaptureResult{}, errors.New("capture batch turn does not match trusted turn")
	}
	if err := prepareTrustedTurnCapture(&input); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if input.Batch.CallCount != int32(len(input.Calls)) ||
		input.Batch.ActionCount != int32(len(input.Actions)) ||
		input.Batch.ConsumptionCount != int32(len(input.Consumptions)) {
		return TrustedTurnCaptureResult{}, errors.New("capture batch counts do not match its trusted contents")
	}

	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.queries.WithTx(tx)
	run, err := qtx.LockMixedRLRun(ctx, input.RunID)
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	input.WorkspaceID = run.WorkspaceID
	if run.Status == "completed" || run.Status == "failed_timeout" {
		eventID := input.LateEventID
		if !eventID.Valid {
			eventID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
		}
		if !run.FrozenSnapshotID.Valid {
			return TrustedTurnCaptureResult{}, errors.New("terminal run has no frozen snapshot")
		}
		summary, _ := json.Marshal(map[string]int{
			"call_count": len(input.Calls), "action_count": len(input.Actions), "consumption_count": len(input.Consumptions),
		})
		if err := (&ProviderCallLedger{queries: qtx}).RecordLateEvent(ctx, LateEventInput{
			EventID: eventID, RunID: input.RunID, RunAgentID: input.RunAgentID, TurnID: input.TurnID,
			Reason: "turn_capture_after_freeze", Summary: summary, SnapshotID: run.FrozenSnapshotID.String,
		}); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		return TrustedTurnCaptureResult{Run: mixedRLRunRecord(run), Late: true, SnapshotID: run.FrozenSnapshotID.String}, nil
	}
	if run.Status != "running" && run.Status != "quiet_candidate" {
		return TrustedTurnCaptureResult{}, fmt.Errorf("capture is not accepted while run status is %q", run.Status)
	}
	agent, err := qtx.GetMixedRLRunAgent(ctx, db.GetMixedRLRunAgentParams{RunID: input.RunID, RunAgentID: input.RunAgentID})
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if input.Batch.CaptureBoundary != agent.CaptureBoundary {
		return TrustedTurnCaptureResult{}, errors.New("capture batch boundary does not match trusted run-agent")
	}

	turn, alreadySettled, err := ensureTrustedResidentTurn(ctx, qtx, input.RunID, input.RunAgentID, input.TurnID, input.TurnOrdinal)
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if alreadySettled {
		if turn.Status != "settled" {
			return TrustedTurnCaptureResult{}, errors.New("turn is already settled as a capture gap")
		}
		if err := verifyTrustedCaptureReplay(ctx, qtx, input); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		return TrustedTurnCaptureResult{Run: mixedRLRunRecord(run), Turn: residentTurnRecord(turn)}, nil
	}
	ledger := &ProviderCallLedger{queries: qtx}
	if _, err := ledger.InsertCaptureBatch(ctx, input.Batch); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	for _, call := range input.Calls {
		if call.RunID != input.RunID || call.RunAgentID != input.RunAgentID || call.TurnID != input.TurnID {
			return TrustedTurnCaptureResult{}, errors.New("provider call falls outside the trusted turn scope")
		}
		if _, err := ledger.InsertProviderCall(ctx, call); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
	}
	for _, action := range input.Actions {
		if action.RunID != input.RunID || action.RunAgentID != input.RunAgentID || action.TurnID != input.TurnID {
			return TrustedTurnCaptureResult{}, errors.New("visible action falls outside the trusted turn scope")
		}
		if _, err := ledger.InsertVisibleAction(ctx, action); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
	}
	for _, consumption := range input.Consumptions {
		if consumption.RunID != input.RunID || consumption.RunAgentID != input.RunAgentID || consumption.TurnID != input.TurnID {
			return TrustedTurnCaptureResult{}, errors.New("message consumption falls outside the trusted turn scope")
		}
		if _, err := ledger.InsertMessageConsumption(ctx, consumption); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
	}
	var providerCaptureConflict error
	if err := assignMixedRLSegmentsForCapture(ctx, qtx, tx, ledger, input); err != nil {
		if !errors.Is(err, ErrDAGProviderCaptureConflict) {
			return TrustedTurnCaptureResult{}, err
		}
		providerCaptureConflict = err
	}
	completedAt := input.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	turn, err = qtx.CompleteMixedRLResidentTurn(ctx, db.CompleteMixedRLResidentTurnParams{
		TurnID: input.TurnID, Status: "settled", CompletedAt: timestamptz(completedAt),
	})
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	// Activity counters remain owned by the WebSocket/daemon transport. Capture
	// persistence must not decrement unfinished-capture or active-turn counts.
	if err := tx.Commit(ctx); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	result := TrustedTurnCaptureResult{Run: mixedRLRunRecord(run), Turn: residentTurnRecord(turn)}
	if providerCaptureConflict != nil {
		return result, providerCaptureConflict
	}
	return result, nil
}

// AcceptTrustedTurnCaptureGap writes the gap audit event, settles its active
// resident turn, and releases capture activity in one transaction. A report
// arriving after a terminal snapshot is retained as a late event and must not
// mutate the frozen run's activity counters.
func (s *ProviderCaptureService) AcceptTrustedTurnCaptureGap(ctx context.Context, input TrustedTurnCaptureGap) (TrustedTurnCaptureResult, error) {
	if err := requireMixedRLQueries(s.queries); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if s.txStarter == nil {
		return TrustedTurnCaptureResult{}, errors.New("transaction starter is required")
	}
	if !input.RunID.Valid || !input.RunAgentID.Valid || !input.TurnID.Valid {
		return TrustedTurnCaptureResult{}, errors.New("run, run-agent, and turn identities are required")
	}
	if input.TurnOrdinal <= 0 {
		return TrustedTurnCaptureResult{}, errors.New("positive turn ordinal is required")
	}
	if input.Reason == "" {
		return TrustedTurnCaptureResult{}, errors.New("capture gap reason is required")
	}

	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.queries.WithTx(tx)
	run, err := qtx.LockMixedRLRun(ctx, input.RunID)
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if _, err := qtx.GetMixedRLRunAgent(ctx, db.GetMixedRLRunAgentParams{RunID: input.RunID, RunAgentID: input.RunAgentID}); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if run.Status == "completed" || run.Status == "failed_timeout" {
		events, err := qtx.ListMixedRLRunAuditEvents(ctx, input.RunID)
		if err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		for _, event := range events {
			if event.Kind == "late_event" && event.RunAgentID == input.RunAgentID && event.TurnID == input.TurnID && event.Reason == "turn_capture_gap_after_freeze" {
				if err := tx.Commit(ctx); err != nil {
					return TrustedTurnCaptureResult{}, err
				}
				return TrustedTurnCaptureResult{Run: mixedRLRunRecord(run), Late: true, SnapshotID: run.FrozenSnapshotID.String}, nil
			}
		}
		eventID := input.LateEventID
		if !eventID.Valid {
			eventID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
		}
		if err := (&ProviderCallLedger{queries: qtx}).RecordLateEvent(ctx, LateEventInput{
			EventID: eventID, RunID: input.RunID, RunAgentID: input.RunAgentID, TurnID: input.TurnID,
			Reason: "turn_capture_gap_after_freeze", Summary: input.Summary, SnapshotID: run.FrozenSnapshotID.String,
		}); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		return TrustedTurnCaptureResult{Run: mixedRLRunRecord(run), Late: true, SnapshotID: run.FrozenSnapshotID.String}, nil
	}
	turn, alreadySettled, err := ensureTrustedResidentTurn(ctx, qtx, input.RunID, input.RunAgentID, input.TurnID, input.TurnOrdinal)
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if alreadySettled {
		if turn.Status != "capture_gap" {
			return TrustedTurnCaptureResult{}, errors.New("turn is already settled with a provider capture")
		}
		if err := tx.Commit(ctx); err != nil {
			return TrustedTurnCaptureResult{}, err
		}
		return TrustedTurnCaptureResult{Run: mixedRLRunRecord(run), Turn: residentTurnRecord(turn)}, nil
	}
	eventID := input.LateEventID
	if !eventID.Valid {
		eventID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
	updated, err := qtx.RecordMixedRLCaptureGap(ctx, db.RecordMixedRLCaptureGapParams{
		EventID: eventID, RunID: input.RunID, RunAgentID: input.RunAgentID, TurnID: input.TurnID,
		Reason: input.Reason, Summary: input.Summary,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TrustedTurnCaptureResult{}, ErrCaptureGapWindowClosed
	}
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	completedAt := input.OccurredAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	turn, err = qtx.CompleteMixedRLResidentTurn(ctx, db.CompleteMixedRLResidentTurnParams{
		TurnID: input.TurnID, Status: "capture_gap", CompletedAt: timestamptz(completedAt),
	})
	if err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TrustedTurnCaptureResult{}, err
	}
	return TrustedTurnCaptureResult{Run: mixedRLRunRecord(updated), Turn: residentTurnRecord(turn)}, nil
}

func prepareTrustedTurnCapture(input *TrustedTurnCapture) error {
	if input.Batch.PayloadHash == "" {
		return errors.New("payload_hash is required")
	}
	seenOrdinal := make(map[int64]struct{}, len(input.Calls))
	var previousOrdinal int64
	for i := range input.Calls {
		call := &input.Calls[i]
		if call.CallOrdinal <= 0 {
			return errors.New("provider call ordinal must be positive")
		}
		if _, dup := seenOrdinal[call.CallOrdinal]; dup {
			return fmt.Errorf("overlapping provider call ordinal %d", call.CallOrdinal)
		}
		if previousOrdinal > 0 && call.CallOrdinal < previousOrdinal {
			return errors.New("provider call ordinals must be non-decreasing")
		}
		seenOrdinal[call.CallOrdinal] = struct{}{}
		previousOrdinal = call.CallOrdinal
		if err := validateRawProviderRequest(call.RawProviderRequest); err != nil {
			return err
		}
		derived := deriveProviderCallTrainingEligibility(*call)
		call.TrainingEligible = derived
		if call.RequestHash == "" {
			call.RequestHash = sha256Prefixed(call.RawProviderRequest)
		}
		if call.ResponseHash == "" {
			call.ResponseHash = sha256Prefixed(call.FinalAssistantMessage)
		}
		call.RunID = input.RunID
		call.RunAgentID = input.RunAgentID
		call.TurnID = input.TurnID
	}
	for i := range input.Actions {
		action := &input.Actions[i]
		action.RunID = input.RunID
		action.RunAgentID = input.RunAgentID
		action.TurnID = input.TurnID
		if action.Status == "" {
			action.Status = "succeeded"
		}
		if !action.ActionID.Valid {
			action.ActionID = pgtype.UUID{Bytes: uuid.NewSHA1(uuid.NameSpaceURL, []byte(
				input.TurnID.String()+":action:"+action.Kind+":"+action.CanonicalID.String()+":"+fmt.Sprint(action.ActionOrdinal),
			)), Valid: true}
		}
	}
	for i := range input.Consumptions {
		consumption := &input.Consumptions[i]
		consumption.RunID = input.RunID
		consumption.RunAgentID = input.RunAgentID
		consumption.TurnID = input.TurnID
		if !consumption.ConsumptionID.Valid {
			consumption.ConsumptionID = pgtype.UUID{Bytes: uuid.NewSHA1(uuid.NameSpaceURL, []byte(
				input.TurnID.String()+":consumption:"+consumption.ChannelMessageID.String()+":"+consumption.EffectiveFromCallID,
			)), Valid: true}
		}
	}
	input.Batch.CallCount = int32(len(input.Calls))
	input.Batch.ActionCount = int32(len(input.Actions))
	input.Batch.ConsumptionCount = int32(len(input.Consumptions))
	return nil
}

func deriveProviderCallTrainingEligibility(call ProviderCallInput) bool {
	return call.Status == "completed" && call.ResponseComplete && (call.StopReason == "stop" || call.StopReason == "toolUse")
}

func sha256Prefixed(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}

// assignMixedRLSegmentsForCapture creates message/reaction segments for
// successful actions and assigns unowned calls in canonical order with
// first-success owned + sibling shared_producer associations.
func assignMixedRLSegmentsForCapture(ctx context.Context, qtx *db.Queries, tx pgx.Tx, ledger *ProviderCallLedger, input TrustedTurnCapture) error {
	actions := append([]VisibleActionInput(nil), input.Actions...)
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].CreatedAt.Equal(actions[j].CreatedAt) {
			return actions[i].ActionOrdinal < actions[j].ActionOrdinal
		}
		return actions[i].CreatedAt.Before(actions[j].CreatedAt)
	})
	allCalls, err := qtx.ListMixedRLProviderCallsCanonical(ctx, input.RunID)
	if err != nil {
		return err
	}
	agentCalls := make([]db.PiProviderCall, 0, len(allCalls))
	callByID := make(map[string]db.PiProviderCall, len(allCalls))
	for _, call := range allCalls {
		if call.RunAgentID != input.RunAgentID {
			continue
		}
		agentCalls = append(agentCalls, call)
		callByID[call.CallID] = call
	}
	associations, err := qtx.ListMixedRLSegmentCallsCanonical(ctx, input.RunID)
	if err != nil {
		return err
	}
	owned := make(map[string]string, len(associations))
	for _, association := range associations {
		if association.AssociationKind == "owned" {
			owned[association.ProviderCallID] = association.SegmentID
		}
	}
	nextOrdinal, err := qtx.CountMixedRLSegments(ctx, input.RunID)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if action.Status != "succeeded" {
			continue
		}
		if action.Kind != "message" && action.Kind != "reaction" {
			return fmt.Errorf("unsupported visible action kind %q", action.Kind)
		}
		if !action.CanonicalID.Valid {
			return errors.New("successful visible action requires a canonical id")
		}
		nextOrdinal++
		segmentID := action.Kind + ":" + action.CanonicalID.String()
		if _, err := ledger.InsertSegment(ctx, SegmentInput{
			SegmentID: segmentID, RunID: input.RunID, RunAgentID: input.RunAgentID,
			Kind: action.Kind, CanonicalActionID: action.CanonicalID.String(),
			SegmentOrdinal: nextOrdinal, ProvisionalAt: action.CreatedAt,
		}); err != nil {
			return err
		}
		producer := action.ProducerCallID
		if producer == "" {
			continue
		}
		if ownerSegment, alreadyOwned := owned[producer]; alreadyOwned {
			_ = ownerSegment
			call := callByID[producer]
			if err := ledger.AssociateProviderCall(ctx, SegmentCallAssociationInput{
				SegmentID: segmentID, ProviderCallID: producer,
				CallOrdinal: call.CallOrdinal, AssociationKind: "shared_producer",
			}); err != nil {
				return err
			}
			continue
		}
		producerCall, ok := callByID[producer]
		if !ok {
			return fmt.Errorf("producer call %q is not in the trusted run-agent scope", producer)
		}
		for _, call := range agentCalls {
			if _, alreadyOwned := owned[call.CallID]; alreadyOwned {
				continue
			}
			if call.CallOrdinal > producerCall.CallOrdinal {
				continue
			}
			if err := ledger.AssociateProviderCall(ctx, SegmentCallAssociationInput{
				SegmentID: segmentID, ProviderCallID: call.CallID,
				CallOrdinal: call.CallOrdinal, AssociationKind: "owned",
			}); err != nil {
				return err
			}
			owned[call.CallID] = segmentID
		}
	}
	if err := attachUniversalDAGCapture(ctx, qtx, tx, input, actions); err != nil {
		return err
	}
	return nil
}

func attachUniversalDAGCapture(
	ctx context.Context,
	qtx *db.Queries,
	tx pgx.Tx,
	input TrustedTurnCapture,
	actions []VisibleActionInput,
) error {
	if len(actions) == 0 {
		return nil
	}
	var universalTablePresent bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('interaction_dag_segment') IS NOT NULL`).Scan(&universalTablePresent); err != nil {
		return err
	}
	if !universalTablePresent {
		return nil
	}
	successful := make([]VisibleActionInput, 0, len(actions))
	for _, action := range actions {
		if action.Status == "succeeded" {
			successful = append(successful, action)
		}
	}
	if len(successful) == 0 {
		return nil
	}
	calls := append([]ProviderCallInput(nil), input.Calls...)
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].CallOrdinal < calls[j].CallOrdinal })
	assigned := make(map[string]struct{}, len(calls))
	dag := NewUniversalInteractionDAG()
	for actionIndex, action := range successful {
		producerOrdinal := int64(0)
		for _, call := range calls {
			if call.CallID == action.ProducerCallID {
				producerOrdinal = call.CallOrdinal
				break
			}
		}
		if producerOrdinal == 0 {
			return fmt.Errorf("universal DAG producer call %q is missing", action.ProducerCallID)
		}
		links := make([]ProviderCallAssociation, 0, len(calls))
		if _, shared := assigned[action.ProducerCallID]; shared {
			links = append(links, ProviderCallAssociation{
				ProviderCallID: action.ProducerCallID,
				Role:           "shared_producer",
				Ordinal:        producerOrdinal,
				RunID:          input.RunID,
				RunAgentID:     input.RunAgentID,
				CaptureVersion: 1,
				CorrelationKey: input.Batch.CaptureBoundary,
			})
		} else {
			for _, call := range calls {
				if call.CallOrdinal > producerOrdinal {
					break
				}
				if _, alreadyAssigned := assigned[call.CallID]; alreadyAssigned {
					continue
				}
				links = append(links, ProviderCallAssociation{
					ProviderCallID: call.CallID,
					Role:           "owned",
					Ordinal:        call.CallOrdinal,
					RunID:          input.RunID,
					RunAgentID:     input.RunAgentID,
					CaptureVersion: 1,
					CorrelationKey: input.Batch.CaptureBoundary,
				})
				assigned[call.CallID] = struct{}{}
			}
		}
		if actionIndex == len(successful)-1 {
			for _, call := range calls {
				if _, alreadyAssigned := assigned[call.CallID]; alreadyAssigned {
					continue
				}
				links = append(links, ProviderCallAssociation{
					ProviderCallID: call.CallID,
					Role:           "audit",
					Ordinal:        call.CallOrdinal,
					RunID:          input.RunID,
					RunAgentID:     input.RunAgentID,
					CaptureVersion: 1,
					CorrelationKey: input.Batch.CaptureBoundary,
				})
				assigned[call.CallID] = struct{}{}
			}
		}
		segment, err := qtx.GetUniversalDAGSegmentByVisibleAction(ctx, db.GetUniversalDAGSegmentByVisibleActionParams{
			WorkspaceID:      input.WorkspaceID,
			VisibleActionKey: pgtype.Text{String: action.Kind + ":" + action.CanonicalID.String(), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("resolve universal DAG visible action: %w", err)
		}
		if err := dag.AttachProviderCaptureTx(ctx, qtx, tx, segment.SegmentID, input.Batch.CaptureBatchID.String(), links); err != nil {
			return err
		}
	}
	return nil
}

// ensureTrustedResidentTurn creates the missing durable turn before capture
// insertion, so a capture upload may arrive before its WebSocket activity
// transition. Activity counters deliberately remain owned by that transport.
func ensureTrustedResidentTurn(ctx context.Context, qtx *db.Queries, runID, runAgentID, turnID pgtype.UUID, expectedOrdinal int64) (db.EnvDispatchResidentTurn, bool, error) {
	turn, err := qtx.GetMixedRLResidentTurn(ctx, turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		turn, err = qtx.CreateMixedRLResidentTurn(ctx, db.CreateMixedRLResidentTurnParams{
			TurnID: turnID, RunID: runID, RunAgentID: runAgentID, Status: "active", AcceptedMessageIds: []pgtype.UUID{},
		})
	}
	if err != nil {
		return db.EnvDispatchResidentTurn{}, false, err
	}
	if turn.RunID != runID || turn.RunAgentID != runAgentID {
		return db.EnvDispatchResidentTurn{}, false, errors.New("resident turn falls outside trusted run-agent scope")
	}
	if expectedOrdinal >= 0 && turn.TurnOrdinal != expectedOrdinal {
		return db.EnvDispatchResidentTurn{}, false, fmt.Errorf("resident turn ordinal %d does not match trusted ordinal %d", turn.TurnOrdinal, expectedOrdinal)
	}
	return turn, turn.Status != "active", nil
}

func verifyTrustedCaptureReplay(ctx context.Context, qtx *db.Queries, input TrustedTurnCapture) error {
	batch, err := qtx.GetMixedRLTurnCaptureBatch(ctx, input.TurnID)
	if err != nil {
		return fmt.Errorf("load previously accepted capture: %w", err)
	}
	if batch.CaptureBatchID != input.Batch.CaptureBatchID ||
		batch.CaptureBoundary != input.Batch.CaptureBoundary ||
		batch.CallCount != input.Batch.CallCount ||
		batch.ActionCount != input.Batch.ActionCount ||
		batch.ConsumptionCount != input.Batch.ConsumptionCount ||
		batch.PayloadHash != input.Batch.PayloadHash {
		return errors.New("capture replay does not match the previously accepted batch")
	}
	return nil
}

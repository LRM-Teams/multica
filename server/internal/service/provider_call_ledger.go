package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ProviderCallLedger persists captured provider activity and the frozen run DAG.
type ProviderCallLedger struct {
	queries   *db.Queries
	txStarter TxStarter
}

func NewProviderCallLedger(queries *db.Queries, txStarter TxStarter) *ProviderCallLedger {
	return &ProviderCallLedger{queries: queries, txStarter: txStarter}
}

type TurnCaptureBatchInput struct {
	CaptureBatchID   pgtype.UUID
	TurnID           pgtype.UUID
	CaptureBoundary  string
	CallCount        int32
	ActionCount      int32
	ConsumptionCount int32
	PayloadHash      string
}

type TurnCaptureBatchRecord struct {
	CaptureBatchID   pgtype.UUID
	TurnID           pgtype.UUID
	CaptureBoundary  string
	CallCount        int32
	ActionCount      int32
	ConsumptionCount int32
	PayloadHash      string
	AcceptedAt       time.Time
}

func (l *ProviderCallLedger) InsertCaptureBatch(ctx context.Context, input TurnCaptureBatchInput) (TurnCaptureBatchRecord, error) {
	if err := requireMixedRLQueries(l.queries); err != nil {
		return TurnCaptureBatchRecord{}, err
	}
	if !input.CaptureBatchID.Valid || !input.TurnID.Valid || input.CaptureBoundary == "" || input.PayloadHash == "" {
		return TurnCaptureBatchRecord{}, errors.New("capture batch identity, turn, boundary, and payload hash are required")
	}
	if input.CallCount < 0 || input.ActionCount < 0 || input.ConsumptionCount < 0 {
		return TurnCaptureBatchRecord{}, errors.New("capture batch counts cannot be negative")
	}
	row, err := l.queries.InsertMixedRLTurnCaptureBatch(ctx, db.InsertMixedRLTurnCaptureBatchParams{
		CaptureBatchID: input.CaptureBatchID, TurnID: input.TurnID,
		CaptureBoundary: input.CaptureBoundary, CallCount: input.CallCount,
		ActionCount: input.ActionCount, ConsumptionCount: input.ConsumptionCount,
		PayloadHash: input.PayloadHash,
	})
	if err != nil {
		return TurnCaptureBatchRecord{}, err
	}
	return TurnCaptureBatchRecord{
		CaptureBatchID: row.CaptureBatchID, TurnID: row.TurnID,
		CaptureBoundary: row.CaptureBoundary, CallCount: row.CallCount,
		ActionCount: row.ActionCount, ConsumptionCount: row.ConsumptionCount,
		PayloadHash: row.PayloadHash, AcceptedAt: timeValue(row.AcceptedAt),
	}, nil
}

type ProviderCallInput struct {
	CallID                string
	RunID                 pgtype.UUID
	RunAgentID            pgtype.UUID
	TurnID                pgtype.UUID
	PiSessionID           string
	CallOrdinal           int64
	Provider              string
	Model                 string
	APIKind               string
	RawProviderRequest    []byte
	FinalAssistantMessage []byte
	NormalizedTrajectory  []byte
	NormalizationVersion  string
	Status                string
	StopReason            string
	ResponseComplete      bool
	TrainingEligible      bool
	AReALSessionID        string
	AReALCallID           string
	RequestHash           string
	ResponseHash          string
	StartedAt             time.Time
	CompletedAt           time.Time
}

type ProviderCallRecord struct {
	CallID                string
	RunID                 pgtype.UUID
	RunAgentID            pgtype.UUID
	TurnID                pgtype.UUID
	PiSessionID           string
	CallOrdinal           int64
	Provider              string
	Model                 string
	APIKind               string
	RawProviderRequest    []byte
	FinalAssistantMessage []byte
	NormalizedTrajectory  []byte
	NormalizationVersion  string
	Status                string
	StopReason            string
	ResponseComplete      bool
	TrainingEligible      bool
	AReALSessionID        string
	AReALCallID           string
	RequestHash           string
	ResponseHash          string
	StartedAt             time.Time
	CompletedAt           time.Time
	FrozenAt              time.Time
}

type VisibleActionInput struct {
	ActionID       pgtype.UUID
	RunID          pgtype.UUID
	RunAgentID     pgtype.UUID
	TurnID         pgtype.UUID
	Kind           string
	CanonicalID    pgtype.UUID
	ProducerCallID string
	ActionOrdinal  int64
	Status         string
	CreatedAt      time.Time
}

type VisibleActionRecord struct {
	ActionID       pgtype.UUID
	RunID          pgtype.UUID
	RunAgentID     pgtype.UUID
	TurnID         pgtype.UUID
	Kind           string
	CanonicalID    pgtype.UUID
	ProducerCallID string
	ActionOrdinal  int64
	Status         string
	CreatedAt      time.Time
}

type MessageConsumptionInput struct {
	ConsumptionID       pgtype.UUID
	RunID               pgtype.UUID
	RunAgentID          pgtype.UUID
	TurnID              pgtype.UUID
	ChannelMessageID    pgtype.UUID
	Source              string
	EffectiveFromCallID string
	ConsumedAt          time.Time
}

type MessageConsumptionRecord struct {
	ConsumptionID       pgtype.UUID
	RunID               pgtype.UUID
	RunAgentID          pgtype.UUID
	TurnID              pgtype.UUID
	ChannelMessageID    pgtype.UUID
	Source              string
	EffectiveFromCallID string
	ConsumedAt          time.Time
}

type SegmentInput struct {
	SegmentID         string
	SnapshotID        string
	RunID             pgtype.UUID
	RunAgentID        pgtype.UUID
	Kind              string
	CanonicalActionID string
	SegmentOrdinal    int64
	Reward            *float64
	RewardSource      string
	ProvisionalAt     time.Time
	FinalizedAt       time.Time
}

type SegmentRecord struct {
	SegmentID         string
	SnapshotID        string
	RunID             pgtype.UUID
	RunAgentID        pgtype.UUID
	Kind              string
	CanonicalActionID pgtype.UUID
	SegmentOrdinal    int64
	Reward            pgtype.Float8
	RewardSource      string
	ProvisionalAt     time.Time
	FinalizedAt       time.Time
}

type SegmentCallAssociationInput struct {
	SegmentID       string
	ProviderCallID  string
	CallOrdinal     int64
	AssociationKind string
}

type CausalEdgeInput struct {
	EdgeID               pgtype.UUID
	SnapshotID           string
	RunID                pgtype.UUID
	SourceSegmentID      string
	DestinationSegmentID string
	Type                 string
	TriggerMessageID     pgtype.UUID
	DestinationCallID    string
	EdgeOrdinal          int64
}

type CausalEdgeRecord struct {
	EdgeID               pgtype.UUID
	SnapshotID           string
	RunID                pgtype.UUID
	SourceSegmentID      string
	DestinationSegmentID string
	Type                 string
	TriggerMessageID     pgtype.UUID
	DestinationCallID    string
	EdgeOrdinal          int64
}

type FrozenSnapshotInput struct {
	SnapshotID           string
	RunID                pgtype.UUID
	RunStatus            string
	SchemaVersion        string
	NormalizationVersion string
	CanonicalManifest    []byte
	SnapshotHash         string
	// Build executes after the run row is locked and transitioned to freezing,
	// but before validation/counting/snapshot publication. It is the only safe
	// extension point for deterministic terminal closure.
	Build func(context.Context, *db.Queries, FrozenSnapshotInput) (FrozenSnapshotInput, error)
}

type FrozenDAGRunRecord struct {
	RunID                     pgtype.UUID `json:"run_id"`
	ProjectID                 pgtype.UUID `json:"project_id"`
	WorkspaceID               pgtype.UUID `json:"workspace_id"`
	Status                    string      `json:"status"`
	InitialMessageSubmittedAt time.Time   `json:"initial_message_submitted_at"`
	TimeoutDeadlineAt         time.Time   `json:"timeout_deadline_at"`
	FrozenSnapshotID          string      `json:"snapshot_id"`
	SnapshotHash              string      `json:"snapshot_hash"`
	FrozenAt                  time.Time   `json:"frozen_at"`
	CaptureGapCount           int64       `json:"capture_gap_count"`
}

type FrozenDAGSnapshotRecord struct {
	SnapshotID           string      `json:"snapshot_id"`
	RunID                pgtype.UUID `json:"run_id"`
	RunStatus            string      `json:"run_status"`
	SchemaVersion        string      `json:"schema_version"`
	NormalizationVersion string      `json:"normalization_version"`
	SegmentCount         int64       `json:"segment_count"`
	CallCount            int64       `json:"call_count"`
	EdgeCount            int64       `json:"edge_count"`
	SnapshotHash         string      `json:"snapshot_hash"`
	CreatedAt            time.Time   `json:"created_at"`
}

type FrozenDAGRunAgentRecord struct {
	RunAgentID     pgtype.UUID `json:"run_agent_id"`
	SourceAgentID  pgtype.UUID `json:"source_agent_id"`
	TrainingMode   string      `json:"training_mode"`
	PiSessionID    string      `json:"pi_session_id"`
	AReALSessionID string      `json:"areal_session_id,omitempty"`
}

type FrozenDAGProviderCallRecord struct {
	CallID               string      `json:"call_id"`
	RunAgentID           pgtype.UUID `json:"run_agent_id"`
	TurnID               pgtype.UUID `json:"turn_id"`
	PiSessionID          string      `json:"pi_session_id"`
	CallOrdinal          int64       `json:"call_ordinal"`
	Provider             string      `json:"provider"`
	Model                string      `json:"model"`
	APIKind              string      `json:"api_kind"`
	Status               string      `json:"status"`
	StopReason           string      `json:"stop_reason,omitempty"`
	ResponseComplete     bool        `json:"response_complete"`
	TrainingEligible     bool        `json:"training_eligible"`
	AReALSessionID       string      `json:"areal_session_id,omitempty"`
	AReALCallID          string      `json:"areal_call_id,omitempty"`
	RequestHash          string      `json:"request_hash"`
	ResponseHash         string      `json:"response_hash,omitempty"`
	NormalizationVersion string      `json:"normalization_version,omitempty"`
	StartedAt            time.Time   `json:"started_at"`
	CompletedAt          time.Time   `json:"completed_at"`
}

type FrozenDAGSegmentRecord struct {
	SegmentID         string        `json:"segment_id"`
	RunAgentID        pgtype.UUID   `json:"run_agent_id"`
	Kind              string        `json:"kind"`
	CanonicalActionID pgtype.UUID   `json:"canonical_action_id"`
	SegmentOrdinal    int64         `json:"segment_ordinal"`
	Reward            pgtype.Float8 `json:"reward"`
	RewardSource      string        `json:"reward_source,omitempty"`
	FinalizedAt       time.Time     `json:"finalized_at"`
}

type FrozenDAGAssociationRecord struct {
	SegmentID       string      `json:"segment_id"`
	ProviderCallID  string      `json:"call_id"`
	RunAgentID      pgtype.UUID `json:"run_agent_id"`
	CallOrdinal     int64       `json:"call_ordinal"`
	AssociationKind string      `json:"association_kind"`
}

type FrozenDAGCaptureGapRecord struct {
	EventID    pgtype.UUID `json:"event_id"`
	RunAgentID pgtype.UUID `json:"run_agent_id"`
	TurnID     pgtype.UUID `json:"turn_id"`
	Reason     string      `json:"reason"`
	ReceivedAt time.Time   `json:"received_at"`
}

type FrozenDAGRecord struct {
	Run           FrozenDAGRunRecord            `json:"run"`
	Snapshot      FrozenDAGSnapshotRecord       `json:"snapshot"`
	RunAgents     []FrozenDAGRunAgentRecord     `json:"run_agents"`
	ProviderCalls []FrozenDAGProviderCallRecord `json:"provider_calls"`
	Segments      []FrozenDAGSegmentRecord      `json:"segments"`
	Associations  []FrozenDAGAssociationRecord  `json:"associations"`
	Edges         []CausalEdgeRecord            `json:"edges"`
	CaptureGaps   []FrozenDAGCaptureGapRecord   `json:"capture_gaps"`
}

type FrozenSnapshotRecord struct {
	SnapshotID           string
	RunID                pgtype.UUID
	RunStatus            string
	SchemaVersion        string
	NormalizationVersion string
	SegmentCount         int64
	CallCount            int64
	EdgeCount            int64
	CanonicalManifest    []byte
	SnapshotHash         string
	CreatedAt            time.Time
}

func (l *ProviderCallLedger) InsertProviderCall(ctx context.Context, input ProviderCallInput) (ProviderCallRecord, error) {
	if err := requireMixedRLQueries(l.queries); err != nil {
		return ProviderCallRecord{}, err
	}
	if input.CallID == "" || input.PiSessionID == "" || input.Provider == "" || input.Model == "" || input.APIKind == "" {
		return ProviderCallRecord{}, errors.New("call identity, session, provider, model, and API kind are required")
	}
	if input.CallOrdinal <= 0 {
		return ProviderCallRecord{}, errors.New("call ordinal must be positive")
	}
	derivedEligibility := input.Status == "completed" && input.ResponseComplete && (input.StopReason == "stop" || input.StopReason == "toolUse")
	if input.TrainingEligible != derivedEligibility {
		return ProviderCallRecord{}, fmt.Errorf("training eligibility does not match call completion semantics")
	}
	if err := validateRawProviderRequest(input.RawProviderRequest); err != nil {
		return ProviderCallRecord{}, err
	}
	if !json.Valid(input.FinalAssistantMessage) {
		return ProviderCallRecord{}, errors.New("final assistant message must be valid JSON")
	}
	if len(input.NormalizedTrajectory) > 0 && !json.Valid(input.NormalizedTrajectory) {
		return ProviderCallRecord{}, errors.New("normalized trajectory must be valid JSON")
	}
	agent, err := l.queries.GetMixedRLRunAgent(ctx, db.GetMixedRLRunAgentParams{
		RunID: input.RunID, RunAgentID: input.RunAgentID,
	})
	if err != nil {
		return ProviderCallRecord{}, err
	}
	if input.PiSessionID != agent.PiSessionID {
		return ProviderCallRecord{}, errors.New("provider call Pi session must match its run-agent")
	}
	if agent.TrainingMode == "online_rl" {
		if !agent.ArealSessionID.Valid || input.AReALSessionID != agent.ArealSessionID.String || input.AReALCallID == "" {
			return ProviderCallRecord{}, errors.New("online_rl provider call requires its run-agent AReaL session and a call identity")
		}
	} else if input.AReALSessionID != "" || input.AReALCallID != "" {
		return ProviderCallRecord{}, errors.New("non-online provider call cannot carry AReaL identity")
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = time.Now().UTC()
	}
	if input.Status == "completed" && input.CompletedAt.IsZero() {
		input.CompletedAt = input.StartedAt
	}
	row, err := l.queries.InsertMixedRLProviderCall(ctx, db.InsertMixedRLProviderCallParams{
		CallID: input.CallID, RunID: input.RunID, RunAgentID: input.RunAgentID,
		TurnID: input.TurnID, PiSessionID: input.PiSessionID, CallOrdinal: input.CallOrdinal,
		Provider: input.Provider, Model: input.Model, ApiKind: input.APIKind,
		RawProviderRequest:    cloneBytes(input.RawProviderRequest),
		FinalAssistantMessage: cloneBytes(input.FinalAssistantMessage),
		NormalizedTrajectory:  cloneBytes(input.NormalizedTrajectory),
		NormalizationVersion:  input.NormalizationVersion, Status: input.Status,
		StopReason: input.StopReason, ResponseComplete: input.ResponseComplete,
		ArealSessionID: input.AReALSessionID, ArealCallID: input.AReALCallID,
		RequestHash:  input.RequestHash,
		ResponseHash: input.ResponseHash, StartedAt: timestamptz(input.StartedAt),
		CompletedAt: timestamptz(input.CompletedAt),
	})
	return providerCallRecord(row), err
}

func (l *ProviderCallLedger) InsertVisibleAction(ctx context.Context, input VisibleActionInput) (VisibleActionRecord, error) {
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	}
	row, err := l.queries.InsertMixedRLVisibleAction(ctx, db.InsertMixedRLVisibleActionParams{
		ActionID: input.ActionID, RunID: input.RunID, RunAgentID: input.RunAgentID,
		TurnID: input.TurnID, Kind: input.Kind, CanonicalID: input.CanonicalID,
		ProducerCallID: input.ProducerCallID, ActionOrdinal: input.ActionOrdinal,
		Status: input.Status, CreatedAt: timestamptz(input.CreatedAt),
	})
	return visibleActionRecord(row), err
}

func (l *ProviderCallLedger) InsertMessageConsumption(ctx context.Context, input MessageConsumptionInput) (MessageConsumptionRecord, error) {
	if input.ConsumedAt.IsZero() {
		input.ConsumedAt = time.Now().UTC()
	}
	row, err := l.queries.InsertMixedRLMessageConsumption(ctx, db.InsertMixedRLMessageConsumptionParams{
		ConsumptionID: input.ConsumptionID, RunID: input.RunID, RunAgentID: input.RunAgentID,
		TurnID: input.TurnID, ChannelMessageID: input.ChannelMessageID, Source: input.Source,
		EffectiveFromCallID: input.EffectiveFromCallID, ConsumedAt: timestamptz(input.ConsumedAt),
	})
	return messageConsumptionRecord(row), err
}

func (l *ProviderCallLedger) InsertSegment(ctx context.Context, input SegmentInput) (SegmentRecord, error) {
	if input.SegmentID == "" || input.SegmentOrdinal <= 0 {
		return SegmentRecord{}, errors.New("segment ID and a positive ordinal are required")
	}
	var canonicalActionID pgtype.UUID
	if input.CanonicalActionID != "" {
		parsed, err := util.ParseUUID(input.CanonicalActionID)
		if err != nil {
			return SegmentRecord{}, fmt.Errorf("canonical action ID: %w", err)
		}
		canonicalActionID = parsed
	}
	if input.Kind != "terminal" && !canonicalActionID.Valid {
		return SegmentRecord{}, errors.New("message and reaction segments require a canonical action ID")
	}
	if input.Kind == "terminal" && canonicalActionID.Valid {
		return SegmentRecord{}, errors.New("terminal segments cannot have a canonical action ID")
	}
	if input.ProvisionalAt.IsZero() {
		input.ProvisionalAt = time.Now().UTC()
	}
	var reward pgtype.Float8
	if input.Reward != nil {
		reward = pgtype.Float8{Float64: *input.Reward, Valid: true}
	}
	row, err := l.queries.InsertMixedRLRunSegment(ctx, db.InsertMixedRLRunSegmentParams{
		SegmentID: input.SegmentID, SnapshotID: input.SnapshotID, RunID: input.RunID,
		RunAgentID: input.RunAgentID, Kind: input.Kind, CanonicalActionID: canonicalActionID,
		SegmentOrdinal: input.SegmentOrdinal, Reward: reward, RewardSource: input.RewardSource,
		ProvisionalAt: timestamptz(input.ProvisionalAt), FinalizedAt: timestamptz(input.FinalizedAt),
	})
	return segmentRecord(row), err
}

func (l *ProviderCallLedger) AssociateProviderCall(ctx context.Context, input SegmentCallAssociationInput) error {
	if input.AssociationKind != "owned" && input.AssociationKind != "shared_producer" && input.AssociationKind != "audit" {
		return fmt.Errorf("invalid association kind %q", input.AssociationKind)
	}
	affected, err := l.queries.AssociateMixedRLProviderCall(ctx, db.AssociateMixedRLProviderCallParams{
		SegmentID: input.SegmentID, ProviderCallID: input.ProviderCallID,
		CallOrdinal: input.CallOrdinal, AssociationKind: input.AssociationKind,
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("provider call association rejected: segment and call must exist in the same run-agent")
	}
	return nil
}

func (l *ProviderCallLedger) InsertCausalEdge(ctx context.Context, input CausalEdgeInput) (CausalEdgeRecord, error) {
	row, err := l.queries.InsertMixedRLCausalEdge(ctx, db.InsertMixedRLCausalEdgeParams{
		EdgeID: input.EdgeID, SnapshotID: input.SnapshotID, RunID: input.RunID,
		SrcSegmentID: input.SourceSegmentID, DstSegmentID: input.DestinationSegmentID,
		Type: input.Type, TriggerMessageID: input.TriggerMessageID,
		DstCallID: input.DestinationCallID, EdgeOrdinal: input.EdgeOrdinal,
	})
	return causalEdgeRecord(row), err
}

func (l *ProviderCallLedger) FreezeAndComplete(ctx context.Context, input FrozenSnapshotInput) (FrozenSnapshotRecord, EnvDispatchRunRecord, error) {
	if err := requireMixedRLQueries(l.queries); err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if l.txStarter == nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, errors.New("transaction starter is required")
	}
	if input.SnapshotID == "" || input.SnapshotHash == "" || input.SchemaVersion == "" || input.NormalizationVersion == "" {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, errors.New("snapshot identity, versions, and hash are required")
	}
	if input.RunStatus != "completed" && input.RunStatus != "failed_timeout" {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, fmt.Errorf("run status %q is not terminal", input.RunStatus)
	}
	if !json.Valid(input.CanonicalManifest) {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, errors.New("canonical manifest must be valid JSON")
	}

	tx, err := l.txStarter.Begin(ctx)
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := l.queries.WithTx(tx)

	locked, err := qtx.LockMixedRLRun(ctx, input.RunID)
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if input.RunStatus == "completed" && locked.Status != "quiet_candidate" {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, fmt.Errorf("completed freeze requires quiet_candidate status, got %q", locked.Status)
	}
	if input.RunStatus == "failed_timeout" && locked.Status != "running" && locked.Status != "quiet_candidate" {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, fmt.Errorf("timeout freeze requires running or quiet_candidate status, got %q", locked.Status)
	}
	expectedStatus := locked.Status
	if input.RunStatus == "failed_timeout" {
		activeTurns, listErr := qtx.ListMixedRLActiveResidentTurns(ctx, input.RunID)
		if listErr != nil {
			return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, listErr
		}
		if int64(len(activeTurns)) != locked.ActiveTurnCount {
			return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, fmt.Errorf(
				"timeout freeze activity mismatch: active_turn_count=%d active_resident_turns=%d",
				locked.ActiveTurnCount, len(activeTurns),
			)
		}
		for _, turn := range activeTurns {
			if _, gapErr := qtx.RecordMixedRLCaptureGap(ctx, db.RecordMixedRLCaptureGapParams{
				EventID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, RunID: input.RunID,
				RunAgentID: turn.RunAgentID, TurnID: turn.TurnID,
				Reason: "run_timeout", Summary: []byte(`{"source":"timeout_freeze"}`),
			}); gapErr != nil {
				return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, gapErr
			}
			if _, settleErr := qtx.CompleteMixedRLResidentTurn(ctx, db.CompleteMixedRLResidentTurnParams{
				TurnID: turn.TurnID, Status: "capture_gap", CompletedAt: timestamptz(time.Now().UTC()),
			}); settleErr != nil {
				return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, settleErr
			}
		}
		settled, settleErr := qtx.AdjustMixedRLRunActivity(ctx, db.AdjustMixedRLRunActivityParams{
			RunID: input.RunID, ActiveTurnDelta: -locked.ActiveTurnCount,
			InflightToolDelta: -locked.InflightToolCount, UnfinishedCaptureDelta: -locked.UnfinishedCaptureBatchCount,
		})
		if settleErr != nil {
			return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, settleErr
		}
		expectedStatus = settled.Status
	}

	if _, err = tx.Exec(ctx, "SELECT set_config('multica.mixed_rl_freeze_writer', 'on', true)"); err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if _, err = qtx.TransitionMixedRLRunStatus(ctx, db.TransitionMixedRLRunStatusParams{
		RunID: input.RunID, ExpectedStatus: expectedStatus, NextStatus: "freezing",
	}); err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if input.Build != nil {
		input, err = input.Build(ctx, qtx, input)
		if err != nil {
			return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
		}
		if input.SnapshotID == "" || input.SnapshotHash == "" || input.SchemaVersion == "" || input.NormalizationVersion == "" || !json.Valid(input.CanonicalManifest) {
			return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, errors.New("freeze builder returned an invalid snapshot input")
		}
	}

	invariants, err := qtx.ValidateMixedRLRunForFreeze(ctx, input.RunID)
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if invariants.MissingOnlineSessionCount != 0 ||
		invariants.InvalidRunAgentIdentityCount != 0 ||
		invariants.InvalidProviderCallIdentityCount != 0 ||
		invariants.CaptureBoundaryMismatchCount != 0 ||
		invariants.SharedWithoutOwnerCount != 0 ||
		invariants.InvalidConsumptionCount != 0 ||
		invariants.DuplicateTerminalAgentCount != 0 ||
		invariants.UncoveredSettledTurnCount != 0 {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, fmt.Errorf(
			"mixed-RL freeze invariant violation: missing_online_sessions=%d invalid_run_agent_identities=%d invalid_provider_call_identities=%d capture_boundary_mismatches=%d shared_without_owner=%d invalid_consumptions=%d duplicate_terminal_agents=%d uncovered_settled_turns=%d",
			invariants.MissingOnlineSessionCount,
			invariants.InvalidRunAgentIdentityCount,
			invariants.InvalidProviderCallIdentityCount,
			invariants.CaptureBoundaryMismatchCount,
			invariants.SharedWithoutOwnerCount,
			invariants.InvalidConsumptionCount,
			invariants.DuplicateTerminalAgentCount,
			invariants.UncoveredSettledTurnCount,
		)
	}

	segmentCount, err := qtx.CountMixedRLSegments(ctx, input.RunID)
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	callCount, err := qtx.CountMixedRLProviderCalls(ctx, input.RunID)
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	edgeCount, err := qtx.CountMixedRLEdges(ctx, input.RunID)
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}

	snapshotRow, err := qtx.CreateMixedRLFrozenSnapshot(ctx, db.CreateMixedRLFrozenSnapshotParams{
		SnapshotID: input.SnapshotID, RunID: input.RunID, RunStatus: input.RunStatus,
		SchemaVersion: input.SchemaVersion, NormalizationVersion: input.NormalizationVersion,
		SegmentCount: segmentCount, CallCount: callCount, EdgeCount: edgeCount,
		CanonicalManifest: cloneBytes(input.CanonicalManifest), SnapshotHash: input.SnapshotHash,
	})
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	frozenAt := timeValue(snapshotRow.CreatedAt)
	frozenCalls, err := qtx.FreezeMixedRLProviderCalls(ctx, db.FreezeMixedRLProviderCallsParams{
		FrozenAt: timestamptz(frozenAt), RunID: input.RunID,
	})
	if err != nil || frozenCalls != callCount {
		if err == nil {
			err = fmt.Errorf("provider-call freeze count mismatch: expected %d, froze %d", callCount, frozenCalls)
		}
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	frozenSegments, err := qtx.FreezeMixedRLSegments(ctx, db.FreezeMixedRLSegmentsParams{
		SnapshotID: text(input.SnapshotID), FrozenAt: timestamptz(frozenAt), RunID: input.RunID,
	})
	if err != nil || frozenSegments != segmentCount {
		if err == nil {
			err = fmt.Errorf("segment freeze count mismatch: expected %d, froze %d", segmentCount, frozenSegments)
		}
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	frozenEdges, err := qtx.FreezeMixedRLEdges(ctx, db.FreezeMixedRLEdgesParams{
		SnapshotID: text(input.SnapshotID), RunID: input.RunID,
	})
	if err != nil || frozenEdges != edgeCount {
		if err == nil {
			err = fmt.Errorf("edge freeze count mismatch: expected %d, froze %d", edgeCount, frozenEdges)
		}
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}

	runRow, err := qtx.CompleteMixedRLRunWithSnapshot(ctx, db.CompleteMixedRLRunWithSnapshotParams{
		TerminalStatus: input.RunStatus, RunID: input.RunID,
		SnapshotID: input.SnapshotID, SnapshotHash: input.SnapshotHash,
	})
	if err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('multica.mixed_rl_freeze_writer', 'off', true)"); err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return FrozenSnapshotRecord{}, EnvDispatchRunRecord{}, err
	}
	return frozenSnapshotRecord(snapshotRow), mixedRLRunRecord(runRow), nil
}
func (l *ProviderCallLedger) GetFrozenSnapshot(ctx context.Context, runID pgtype.UUID) (FrozenSnapshotRecord, error) {
	row, err := l.queries.GetMixedRLFrozenSnapshot(ctx, runID)
	return frozenSnapshotRecord(row), err
}

func providerCallRecord(row db.PiProviderCall) ProviderCallRecord {
	return ProviderCallRecord{
		CallID: row.CallID, RunID: row.RunID, RunAgentID: row.RunAgentID, TurnID: row.TurnID,
		PiSessionID: row.PiSessionID, CallOrdinal: row.CallOrdinal, Provider: row.Provider,
		Model: row.Model, APIKind: row.ApiKind, RawProviderRequest: cloneBytes(row.RawProviderRequest),
		FinalAssistantMessage: cloneBytes(row.FinalAssistantMessage),
		NormalizedTrajectory:  cloneBytes(row.NormalizedTrajectory),
		NormalizationVersion:  mixedRLTextValue(row.NormalizationVersion), Status: row.Status,
		StopReason: mixedRLTextValue(row.StopReason), ResponseComplete: row.ResponseComplete,
		TrainingEligible: row.TrainingEligible, AReALSessionID: mixedRLTextValue(row.ArealSessionID),
		AReALCallID: mixedRLTextValue(row.ArealCallID), RequestHash: row.RequestHash,
		ResponseHash: mixedRLTextValue(row.ResponseHash), StartedAt: timeValue(row.StartedAt),
		CompletedAt: timeValue(row.CompletedAt), FrozenAt: timeValue(row.FrozenAt),
	}
}

func visibleActionRecord(row db.PiVisibleAction) VisibleActionRecord {
	return VisibleActionRecord{
		ActionID: row.ActionID, RunID: row.RunID, RunAgentID: row.RunAgentID,
		TurnID: row.TurnID, Kind: row.Kind, CanonicalID: row.CanonicalID,
		ProducerCallID: mixedRLTextValue(row.ProducerCallID), ActionOrdinal: row.ActionOrdinal,
		Status: row.Status, CreatedAt: timeValue(row.CreatedAt),
	}
}

func messageConsumptionRecord(row db.PiMessageConsumption) MessageConsumptionRecord {
	return MessageConsumptionRecord{
		ConsumptionID: row.ConsumptionID, RunID: row.RunID, RunAgentID: row.RunAgentID,
		TurnID: row.TurnID, ChannelMessageID: row.ChannelMessageID, Source: row.Source,
		EffectiveFromCallID: row.EffectiveFromCallID, ConsumedAt: timeValue(row.ConsumedAt),
	}
}

func segmentRecord(row db.InteractionDagRunSegment) SegmentRecord {
	return SegmentRecord{
		SegmentID: row.SegmentID, SnapshotID: mixedRLTextValue(row.SnapshotID), RunID: row.RunID,
		RunAgentID: row.RunAgentID, Kind: row.Kind, CanonicalActionID: row.CanonicalActionID,
		SegmentOrdinal: row.SegmentOrdinal, Reward: row.Reward,
		RewardSource: mixedRLTextValue(row.RewardSource), ProvisionalAt: timeValue(row.ProvisionalAt),
		FinalizedAt: timeValue(row.FinalizedAt),
	}
}

func causalEdgeRecord(row db.InteractionDagCausalEdge) CausalEdgeRecord {
	return CausalEdgeRecord{
		EdgeID: row.EdgeID, SnapshotID: mixedRLTextValue(row.SnapshotID), RunID: row.RunID,
		SourceSegmentID: row.SrcSegmentID, DestinationSegmentID: row.DstSegmentID,
		Type: row.Type, TriggerMessageID: row.TriggerMessageID,
		DestinationCallID: mixedRLTextValue(row.DstCallID), EdgeOrdinal: row.EdgeOrdinal,
	}
}

func (l *ProviderCallLedger) GetFrozenDAG(ctx context.Context, runID pgtype.UUID, snapshotID string) (FrozenDAGRecord, error) {
	if err := requireMixedRLQueries(l.queries); err != nil {
		return FrozenDAGRecord{}, err
	}
	if !runID.Valid || snapshotID == "" {
		return FrozenDAGRecord{}, errors.New("run and snapshot identities are required")
	}

	runRow, err := l.queries.GetMixedRLRun(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	if runRow.Status != "completed" && runRow.Status != "failed_timeout" {
		return FrozenDAGRecord{}, fmt.Errorf("run %v is not terminal", runID.Bytes)
	}
	if mixedRLTextValue(runRow.FrozenSnapshotID) != snapshotID {
		return FrozenDAGRecord{}, fmt.Errorf("snapshot %q does not belong to the terminal run", snapshotID)
	}

	snapshotRow, err := l.queries.GetMixedRLFrozenSnapshot(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	if snapshotRow.SnapshotID != snapshotID || snapshotRow.RunID != runID || snapshotRow.RunStatus != runRow.Status {
		return FrozenDAGRecord{}, errors.New("terminal run and snapshot ownership mismatch")
	}
	if mixedRLTextValue(runRow.SnapshotHash) != snapshotRow.SnapshotHash ||
		!timeValue(runRow.FrozenAt).Equal(timeValue(snapshotRow.CreatedAt)) {
		return FrozenDAGRecord{}, errors.New("terminal run and snapshot metadata mismatch")
	}
	snapshot := frozenSnapshotRecord(snapshotRow)

	agentRows, err := l.queries.ListMixedRLRunAgents(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	callRows, err := l.queries.ListMixedRLProviderCallsCanonical(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	segmentRows, err := l.queries.ListMixedRLSnapshotSegmentsCanonical(ctx, text(snapshotID))
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	associationRows, err := l.queries.ListMixedRLSegmentCallsCanonical(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	edgeRows, err := l.queries.ListMixedRLCausalEdgesCanonical(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	auditRows, err := l.queries.ListMixedRLRunAuditEvents(ctx, runID)
	if err != nil {
		return FrozenDAGRecord{}, err
	}
	if int64(len(callRows)) != snapshot.CallCount || int64(len(segmentRows)) != snapshot.SegmentCount || int64(len(edgeRows)) != snapshot.EdgeCount {
		return FrozenDAGRecord{}, fmt.Errorf(
			"frozen DAG count mismatch: calls=%d/%d segments=%d/%d edges=%d/%d",
			len(callRows), snapshot.CallCount, len(segmentRows), snapshot.SegmentCount,
			len(edgeRows), snapshot.EdgeCount,
		)
	}

	result := FrozenDAGRecord{
		Run: FrozenDAGRunRecord{
			RunID: runRow.RunID, ProjectID: runRow.ProjectID, WorkspaceID: runRow.WorkspaceID,
			Status: runRow.Status, InitialMessageSubmittedAt: timeValue(runRow.InitialMessageSubmittedAt),
			TimeoutDeadlineAt: timeValue(runRow.TimeoutDeadlineAt),
			FrozenSnapshotID:  mixedRLTextValue(runRow.FrozenSnapshotID),
			SnapshotHash:      mixedRLTextValue(runRow.SnapshotHash), FrozenAt: timeValue(runRow.FrozenAt),
			CaptureGapCount: runRow.CaptureGapCount,
		},
		Snapshot:      frozenDAGSnapshotRecord(snapshotRow),
		RunAgents:     make([]FrozenDAGRunAgentRecord, 0, len(agentRows)),
		ProviderCalls: make([]FrozenDAGProviderCallRecord, 0, len(callRows)),
		Segments:      make([]FrozenDAGSegmentRecord, 0, len(segmentRows)),
		Associations:  make([]FrozenDAGAssociationRecord, 0, len(associationRows)),
		Edges:         make([]CausalEdgeRecord, 0, len(edgeRows)),
		CaptureGaps:   make([]FrozenDAGCaptureGapRecord, 0),
	}
	for _, row := range agentRows {
		result.RunAgents = append(result.RunAgents, FrozenDAGRunAgentRecord{
			RunAgentID: row.RunAgentID, SourceAgentID: row.SourceAgentID,
			TrainingMode: row.TrainingMode, PiSessionID: row.PiSessionID,
			AReALSessionID: mixedRLTextValue(row.ArealSessionID),
		})
	}
	callIDs := make(map[string]struct{}, len(callRows))
	for _, row := range callRows {
		frozenAt := timeValue(row.FrozenAt)
		if frozenAt.IsZero() || frozenAt.After(snapshot.CreatedAt) {
			return FrozenDAGRecord{}, fmt.Errorf("provider call %q is not part of snapshot %q", row.CallID, snapshotID)
		}
		callIDs[row.CallID] = struct{}{}
		result.ProviderCalls = append(result.ProviderCalls, FrozenDAGProviderCallRecord{
			CallID: row.CallID, RunAgentID: row.RunAgentID, TurnID: row.TurnID,
			PiSessionID: row.PiSessionID, CallOrdinal: row.CallOrdinal,
			Provider: row.Provider, Model: row.Model, APIKind: row.ApiKind,
			Status: row.Status, StopReason: mixedRLTextValue(row.StopReason),
			ResponseComplete: row.ResponseComplete, TrainingEligible: row.TrainingEligible,
			AReALSessionID: mixedRLTextValue(row.ArealSessionID), AReALCallID: mixedRLTextValue(row.ArealCallID),
			RequestHash: row.RequestHash, ResponseHash: mixedRLTextValue(row.ResponseHash),
			NormalizationVersion: mixedRLTextValue(row.NormalizationVersion),
			StartedAt:            timeValue(row.StartedAt), CompletedAt: timeValue(row.CompletedAt),
		})
	}
	segmentIDs := make(map[string]struct{}, len(segmentRows))
	for _, row := range segmentRows {
		segmentIDs[row.SegmentID] = struct{}{}
		result.Segments = append(result.Segments, FrozenDAGSegmentRecord{
			SegmentID: row.SegmentID, RunAgentID: row.RunAgentID, Kind: row.Kind,
			CanonicalActionID: row.CanonicalActionID, SegmentOrdinal: row.SegmentOrdinal,
			Reward: row.Reward, RewardSource: mixedRLTextValue(row.RewardSource),
			FinalizedAt: timeValue(row.FinalizedAt),
		})
	}
	for _, row := range associationRows {
		if _, ok := segmentIDs[row.SegmentID]; !ok {
			return FrozenDAGRecord{}, fmt.Errorf("association references segment %q outside snapshot %q", row.SegmentID, snapshotID)
		}
		if _, ok := callIDs[row.ProviderCallID]; !ok {
			return FrozenDAGRecord{}, fmt.Errorf("association references call %q outside snapshot %q", row.ProviderCallID, snapshotID)
		}
		result.Associations = append(result.Associations, FrozenDAGAssociationRecord{
			SegmentID: row.SegmentID, ProviderCallID: row.ProviderCallID,
			RunAgentID: row.RunAgentID, CallOrdinal: row.CallOrdinal,
			AssociationKind: row.AssociationKind,
		})
	}
	for _, row := range edgeRows {
		if mixedRLTextValue(row.SnapshotID) != snapshotID {
			return FrozenDAGRecord{}, fmt.Errorf("edge does not belong to snapshot %q", snapshotID)
		}
		result.Edges = append(result.Edges, causalEdgeRecord(row))
	}
	for _, row := range auditRows {
		receivedAt := timeValue(row.ReceivedAt)
		if row.Kind != "capture_gap" || receivedAt.After(snapshot.CreatedAt) {
			continue
		}
		result.CaptureGaps = append(result.CaptureGaps, FrozenDAGCaptureGapRecord{
			EventID: row.EventID, RunAgentID: row.RunAgentID, TurnID: row.TurnID,
			Reason: row.Reason, ReceivedAt: receivedAt,
		})
	}
	if int64(len(result.CaptureGaps)) != result.Run.CaptureGapCount {
		return FrozenDAGRecord{}, fmt.Errorf("frozen capture-gap count mismatch: events=%d count=%d", len(result.CaptureGaps), result.Run.CaptureGapCount)
	}
	return result, nil
}

func frozenDAGSnapshotRecord(row db.InteractionDagFrozenSnapshot) FrozenDAGSnapshotRecord {
	return FrozenDAGSnapshotRecord{
		SnapshotID: row.SnapshotID, RunID: row.RunID, RunStatus: row.RunStatus,
		SchemaVersion: row.SchemaVersion, NormalizationVersion: row.NormalizationVersion,
		SegmentCount: row.SegmentCount, CallCount: row.CallCount, EdgeCount: row.EdgeCount,
		SnapshotHash: row.SnapshotHash, CreatedAt: timeValue(row.CreatedAt),
	}
}

func frozenSnapshotRecord(row db.InteractionDagFrozenSnapshot) FrozenSnapshotRecord {
	return FrozenSnapshotRecord{
		SnapshotID: row.SnapshotID, RunID: row.RunID, RunStatus: row.RunStatus,
		SchemaVersion: row.SchemaVersion, NormalizationVersion: row.NormalizationVersion,
		SegmentCount: row.SegmentCount, CallCount: row.CallCount, EdgeCount: row.EdgeCount,
		CanonicalManifest: cloneBytes(row.CanonicalManifest), SnapshotHash: row.SnapshotHash,
		CreatedAt: timeValue(row.CreatedAt),
	}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

var forbiddenProviderRequestKeys = map[string]struct{}{
	"authorization":      {},
	"proxyauthorization": {},
	"apikey":             {},
	"xapikey":            {},
	"accesstoken":        {},
	"authtoken":          {},
	"bearertoken":        {},
	"clientsecret":       {},
	"credential":         {},
	"credentials":        {},
	"password":           {},
}

func validateRawProviderRequest(raw []byte) error {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return errors.New("provider request must be valid JSON")
	}
	if _, ok := payload.(map[string]any); !ok {
		return errors.New("provider request must be a JSON object")
	}
	if key, found := findForbiddenProviderRequestKey(payload); found {
		return fmt.Errorf("provider request contains forbidden transport authentication field %q", key)
	}
	return nil
}

func findForbiddenProviderRequestKey(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsNumber(r) {
					return unicode.ToLower(r)
				}
				return -1
			}, key)
			if _, forbidden := forbiddenProviderRequestKeys[normalized]; forbidden {
				return key, true
			}
			if key, found := findForbiddenProviderRequestKey(child); found {
				return key, true
			}
		}
	case []any:
		for _, child := range typed {
			if key, found := findForbiddenProviderRequestKey(child); found {
				return key, true
			}
		}
	}
	return "", false
}

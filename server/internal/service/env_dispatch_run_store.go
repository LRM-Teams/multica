package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrCaptureGapWindowClosed = errors.New("mixed-RL capture gap window is closed")
	ErrMixedRLRunNotFound     = errors.New("mixed-RL run not found")
)

var validMixedRLRunTransitions = map[string]map[string]bool{
	"provisioning":    {"preflight": true},
	"preflight":       {"failed_preflight": true},
	"running":         {"quiet_candidate": true, "freezing": true},
	"quiet_candidate": {"running": true, "freezing": true},
}

// EnvDispatchRunStore is the persistence boundary for a mixed-RL run.
type EnvDispatchRunStore struct {
	queries *db.Queries
}

func NewEnvDispatchRunStore(queries *db.Queries) *EnvDispatchRunStore {
	return &EnvDispatchRunStore{queries: queries}
}

type EnvDispatchRunRecord struct {
	ProjectID                   pgtype.UUID
	WorkspaceID                 pgtype.UUID
	RunID                       pgtype.UUID
	SourceTaskID                pgtype.UUID
	SampleIndex                 int32
	Status                      string
	QuietWindowMS               int32
	TotalTimeoutSeconds         int32
	InitialMessageSubmittedAt   time.Time
	TimeoutDeadlineAt           time.Time
	QuietCandidateSince         time.Time
	ActiveTurnCount             int64
	PendingDeliveryCount        int64
	QueuedMessageCount          int64
	InflightToolCount           int64
	UnfinishedCaptureBatchCount int64
	CaptureGapCount             int64
	FrozenSnapshotID            string
	SnapshotHash                string
	FrozenAt                    time.Time
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

type CreateEnvDispatchRunInput struct {
	RunID               pgtype.UUID
	ProjectID           pgtype.UUID
	WorkspaceID         pgtype.UUID
	SourceTaskID        pgtype.UUID
	SampleIndex         int32
	QuietWindowMS       int32
	TotalTimeoutSeconds int32
}

type EnvDispatchRunAgentRecord struct {
	RunAgentID       pgtype.UUID
	RunID            pgtype.UUID
	SourceAgentID    pgtype.UUID
	ExecutionAgentID pgtype.UUID
	RuntimeID        pgtype.UUID
	PiSessionID      string
	TrainingMode     string
	AReALSessionID   string
	CaptureBoundary  string
	NextTurnOrdinal  int64
	NextCallOrdinal  int64
	SettledAt        time.Time
	CreatedAt        time.Time
}

type BindEnvDispatchRunAgentInput struct {
	RunAgentID       pgtype.UUID
	RunID            pgtype.UUID
	SourceAgentID    pgtype.UUID
	ExecutionAgentID pgtype.UUID
	RuntimeID        pgtype.UUID
	PiSessionID      string
	TrainingMode     string
	AReALSessionID   string
	CaptureBoundary  string
}

type ResidentTurnRecord struct {
	TurnID             pgtype.UUID
	RunID              pgtype.UUID
	RunAgentID         pgtype.UUID
	TurnOrdinal        int64
	Status             string
	CaptureStartedAt   time.Time
	CaptureCompletedAt time.Time
	AcceptedMessageIDs []pgtype.UUID
	StartedAt          time.Time
	CompletedAt        time.Time
}

type CreateResidentTurnInput struct {
	TurnID             pgtype.UUID
	RunID              pgtype.UUID
	RunAgentID         pgtype.UUID
	Status             string
	AcceptedMessageIDs []pgtype.UUID
}

type DeliveryObligationRecord struct {
	DeliveryID             pgtype.UUID
	RunID                  pgtype.UUID
	ChannelMessageID       pgtype.UUID
	SourceRecipientAgentID pgtype.UUID
	RunAgentID             pgtype.UUID
	State                  string
	QueuedAt               time.Time
	SettledAt              time.Time
	CreatedAt              time.Time
}

type CreateDeliveryObligationInput struct {
	DeliveryID             pgtype.UUID
	RunID                  pgtype.UUID
	ChannelMessageID       pgtype.UUID
	SourceRecipientAgentID pgtype.UUID
	RunAgentID             pgtype.UUID
	State                  string
	QueuedAt               time.Time
}

type ActivityCounterDelta struct {
	ActiveTurns       int64
	PendingDeliveries int64
	QueuedMessages    int64
	InflightTools     int64
	UnfinishedCapture int64
}

// MixedRLQuiescenceResult contains the durable run state after one evaluation
// and, when due, the terminal freeze kind a scheduler must execute.
type MixedRLQuiescenceResult struct {
	Run       EnvDispatchRunRecord
	Decision  MixedRLQuiescenceDecision
	FreezeDue bool
	TimedOut  bool
}

type CaptureGapInput struct {
	EventID    pgtype.UUID
	RunID      pgtype.UUID
	RunAgentID pgtype.UUID
	TurnID     pgtype.UUID
	Reason     string
	Summary    []byte
}

type LateEventInput struct {
	EventID    pgtype.UUID
	RunID      pgtype.UUID
	RunAgentID pgtype.UUID
	TurnID     pgtype.UUID
	Reason     string
	Summary    []byte
	SnapshotID string
}

type RunAuditEventRecord struct {
	EventID    pgtype.UUID
	RunID      pgtype.UUID
	RunAgentID pgtype.UUID
	TurnID     pgtype.UUID
	Kind       string
	Reason     string
	Summary    []byte
	SnapshotID string
	ReceivedAt time.Time
}

func (s *EnvDispatchRunStore) CreateRun(ctx context.Context, input CreateEnvDispatchRunInput) (EnvDispatchRunRecord, error) {
	if err := requireMixedRLQueries(s.queries); err != nil {
		return EnvDispatchRunRecord{}, err
	}
	if !input.RunID.Valid || !input.ProjectID.Valid || !input.WorkspaceID.Valid {
		return EnvDispatchRunRecord{}, errors.New("run, project, and workspace IDs are required")
	}
	if input.QuietWindowMS < 100 || input.QuietWindowMS > 60_000 {
		return EnvDispatchRunRecord{}, fmt.Errorf("quiet window must be between 100 and 60000 milliseconds")
	}
	if input.TotalTimeoutSeconds < 30 || input.TotalTimeoutSeconds > 86_400 || int64(input.TotalTimeoutSeconds)*1000 <= int64(input.QuietWindowMS) {
		return EnvDispatchRunRecord{}, fmt.Errorf("timeout must be between 30 and 86400 seconds and exceed the quiet window")
	}
	run, err := s.queries.CreateMixedRLRun(ctx, db.CreateMixedRLRunParams{
		ProjectID: input.ProjectID, WorkspaceID: input.WorkspaceID, RunID: input.RunID,
		SourceTaskID: input.SourceTaskID, SampleIndex: input.SampleIndex,
		QuietWindowMs: input.QuietWindowMS, TotalTimeoutSeconds: input.TotalTimeoutSeconds,
	})
	return mixedRLRunRecord(run), err
}

func (s *EnvDispatchRunStore) TransitionStatus(ctx context.Context, runID pgtype.UUID, expected, next string) (EnvDispatchRunRecord, error) {
	if !validMixedRLRunTransitions[expected][next] {
		return EnvDispatchRunRecord{}, fmt.Errorf("invalid run status transition %q to %q", expected, next)
	}
	run, err := s.queries.TransitionMixedRLRunStatus(ctx, db.TransitionMixedRLRunStatusParams{
		RunID: runID, ExpectedStatus: expected, NextStatus: next,
	})
	return mixedRLRunRecord(run), err
}

func (s *EnvDispatchRunStore) StartTimeout(ctx context.Context, runID pgtype.UUID, submittedAt time.Time) (EnvDispatchRunRecord, error) {
	if submittedAt.IsZero() {
		return EnvDispatchRunRecord{}, errors.New("initial message submission time is required")
	}
	run, err := s.queries.StartMixedRLRunTimeout(ctx, db.StartMixedRLRunTimeoutParams{
		RunID: runID, SubmittedAt: timestamptz(submittedAt),
	})
	return mixedRLRunRecord(run), err
}

func (s *EnvDispatchRunStore) GetRun(ctx context.Context, runID pgtype.UUID) (EnvDispatchRunRecord, error) {
	run, err := s.queries.GetMixedRLRun(ctx, runID)
	return mixedRLRunRecord(run), err
}

// EvaluateQuiescence applies only the reversible lifecycle transitions. A
// caller that receives FreezeDue must immediately invoke the atomic ledger
// freeze; this split prevents a timer from publishing a partial snapshot while
// activity transitions are still being persisted.
func (s *EnvDispatchRunStore) EvaluateQuiescence(ctx context.Context, runID pgtype.UUID, now time.Time) (MixedRLQuiescenceResult, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return MixedRLQuiescenceResult{}, err
	}
	decision := EvaluateMixedRLQuiescence(run, now)
	result := MixedRLQuiescenceResult{Run: run, Decision: decision}
	switch decision {
	case MixedRLQuiescenceStartCandidate:
		updated, transitionErr := s.TransitionStatus(ctx, runID, "running", "quiet_candidate")
		if transitionErr != nil {
			return MixedRLQuiescenceResult{}, transitionErr
		}
		result.Run = updated
	case MixedRLQuiescenceResumeRunning:
		updated, transitionErr := s.TransitionStatus(ctx, runID, "quiet_candidate", "running")
		if transitionErr != nil {
			return MixedRLQuiescenceResult{}, transitionErr
		}
		result.Run = updated
	case MixedRLQuiescenceFreezeCompleted:
		result.FreezeDue = true
	case MixedRLQuiescenceFreezeTimeout:
		result.FreezeDue = true
		result.TimedOut = true
	}
	return result, nil
}

func (s *EnvDispatchRunStore) BindRunAgent(ctx context.Context, input BindEnvDispatchRunAgentInput) (EnvDispatchRunAgentRecord, error) {
	if !input.RunAgentID.Valid {
		generated := uuid.New()
		input.RunAgentID = pgtype.UUID{Bytes: generated, Valid: true}
	}
	if input.PiSessionID == "" || input.CaptureBoundary == "" {
		return EnvDispatchRunAgentRecord{}, errors.New("Pi session and capture boundary are required")
	}
	if input.TrainingMode != "online_rl" && input.TrainingMode != "offline_rl" && input.TrainingMode != "none" {
		return EnvDispatchRunAgentRecord{}, fmt.Errorf("invalid training mode %q", input.TrainingMode)
	}
	if input.TrainingMode == "none" && input.AReALSessionID != "" {
		return EnvDispatchRunAgentRecord{}, errors.New("none run-agent cannot carry an AReaL session")
	}
	agent, err := s.queries.CreateMixedRLRunAgent(ctx, db.CreateMixedRLRunAgentParams{
		RunAgentID: input.RunAgentID, RunID: input.RunID, SourceAgentID: input.SourceAgentID,
		ExecutionAgentID: input.ExecutionAgentID, RuntimeID: input.RuntimeID,
		PiSessionID: input.PiSessionID, TrainingMode: input.TrainingMode,
		ArealSessionID: input.AReALSessionID, CaptureBoundary: input.CaptureBoundary,
	})
	return mixedRLRunAgentRecord(agent), err
}

func (s *EnvDispatchRunStore) CreateResidentTurn(ctx context.Context, input CreateResidentTurnInput) (ResidentTurnRecord, error) {
	if input.Status == "" {
		input.Status = "active"
	}
	acceptedMessageIDs := input.AcceptedMessageIDs
	if acceptedMessageIDs == nil {
		acceptedMessageIDs = []pgtype.UUID{}
	}
	turn, err := s.queries.CreateMixedRLResidentTurn(ctx, db.CreateMixedRLResidentTurnParams{
		TurnID: input.TurnID, RunID: input.RunID, RunAgentID: input.RunAgentID,
		Status: input.Status, AcceptedMessageIds: acceptedMessageIDs,
	})
	return residentTurnRecord(turn), err
}

func (s *EnvDispatchRunStore) CompleteResidentTurn(ctx context.Context, turnID pgtype.UUID, status string, completedAt time.Time) (ResidentTurnRecord, error) {
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	turn, err := s.queries.CompleteMixedRLResidentTurn(ctx, db.CompleteMixedRLResidentTurnParams{
		TurnID: turnID, Status: status, CompletedAt: timestamptz(completedAt),
	})
	return residentTurnRecord(turn), err
}

func (s *EnvDispatchRunStore) CreateDeliveryObligation(ctx context.Context, input CreateDeliveryObligationInput) (DeliveryObligationRecord, error) {
	queuedAt := input.QueuedAt
	if queuedAt.IsZero() {
		queuedAt = time.Now().UTC()
	}
	row, err := s.queries.CreateMixedRLDeliveryObligation(ctx, db.CreateMixedRLDeliveryObligationParams{
		DeliveryID: input.DeliveryID, RunID: input.RunID,
		ChannelMessageID:       input.ChannelMessageID,
		SourceRecipientAgentID: input.SourceRecipientAgentID,
		RunAgentID:             input.RunAgentID, State: input.State, QueuedAt: timestamptz(queuedAt),
	})
	return deliveryObligationRecord(row), err
}

func (s *EnvDispatchRunStore) SettleDeliveryObligation(ctx context.Context, deliveryID pgtype.UUID, state string, settledAt time.Time) (DeliveryObligationRecord, error) {
	if settledAt.IsZero() {
		settledAt = time.Now().UTC()
	}
	row, err := s.queries.SettleMixedRLDeliveryObligation(ctx, db.SettleMixedRLDeliveryObligationParams{
		DeliveryID: deliveryID, State: state, SettledAt: timestamptz(settledAt),
	})
	return deliveryObligationRecord(row), err
}

func (s *EnvDispatchRunStore) AdjustActivity(ctx context.Context, runID pgtype.UUID, delta ActivityCounterDelta) (EnvDispatchRunRecord, error) {
	run, err := s.queries.AdjustMixedRLRunActivity(ctx, db.AdjustMixedRLRunActivityParams{
		RunID: runID, ActiveTurnDelta: delta.ActiveTurns,
		PendingDeliveryDelta: delta.PendingDeliveries, QueuedMessageDelta: delta.QueuedMessages,
		InflightToolDelta: delta.InflightTools, UnfinishedCaptureDelta: delta.UnfinishedCapture,
	})
	return mixedRLRunRecord(run), err
}

func (s *EnvDispatchRunStore) RecordCaptureGap(ctx context.Context, input CaptureGapInput) error {
	_, err := s.queries.RecordMixedRLCaptureGap(ctx, db.RecordMixedRLCaptureGapParams{
		EventID: input.EventID, RunID: input.RunID, RunAgentID: input.RunAgentID,
		TurnID: input.TurnID, Reason: input.Reason, Summary: input.Summary,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, lookupErr := s.queries.GetMixedRLRun(ctx, input.RunID); errors.Is(lookupErr, pgx.ErrNoRows) {
		return ErrMixedRLRunNotFound
	} else if lookupErr != nil {
		return lookupErr
	}
	return ErrCaptureGapWindowClosed
}

func (s *EnvDispatchRunStore) RecordLateEvent(ctx context.Context, input LateEventInput) error {
	return s.queries.RecordMixedRLLateEvent(ctx, db.RecordMixedRLLateEventParams{
		EventID: input.EventID, RunID: input.RunID, RunAgentID: input.RunAgentID,
		TurnID: input.TurnID, Reason: input.Reason, Summary: input.Summary,
		SnapshotID: text(input.SnapshotID),
	})
}

func (s *EnvDispatchRunStore) ListAuditEvents(ctx context.Context, runID pgtype.UUID) ([]RunAuditEventRecord, error) {
	rows, err := s.queries.ListMixedRLRunAuditEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	result := make([]RunAuditEventRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, RunAuditEventRecord{
			EventID: row.EventID, RunID: row.RunID, RunAgentID: row.RunAgentID,
			TurnID: row.TurnID, Kind: row.Kind, Reason: row.Reason,
			Summary: row.Summary, SnapshotID: mixedRLTextValue(row.SnapshotID),
			ReceivedAt: timeValue(row.ReceivedAt),
		})
	}
	return result, nil
}

func (s *EnvDispatchRunStore) DeleteRun(ctx context.Context, runID, workspaceID pgtype.UUID) (bool, error) {
	count, err := s.queries.DeleteMixedRLRun(ctx, db.DeleteMixedRLRunParams{RunID: runID, WorkspaceID: workspaceID})
	return count == 1, err
}

func requireMixedRLQueries(queries *db.Queries) error {
	if queries == nil {
		return errors.New("database queries are required")
	}
	return nil
}

func mixedRLRunRecord(row db.EnvDispatchRun) EnvDispatchRunRecord {
	return EnvDispatchRunRecord{
		ProjectID: row.ProjectID, WorkspaceID: row.WorkspaceID, RunID: row.RunID,
		SourceTaskID: row.SourceTaskID, SampleIndex: row.SampleIndex, Status: row.Status,
		QuietWindowMS: row.QuietWindowMs, TotalTimeoutSeconds: row.TotalTimeoutSeconds,
		InitialMessageSubmittedAt: timeValue(row.InitialMessageSubmittedAt),
		TimeoutDeadlineAt:         timeValue(row.TimeoutDeadlineAt),
		QuietCandidateSince:       timeValue(row.QuietCandidateSince),
		ActiveTurnCount:           row.ActiveTurnCount, PendingDeliveryCount: row.PendingDeliveryCount,
		QueuedMessageCount: row.QueuedMessageCount, InflightToolCount: row.InflightToolCount,
		UnfinishedCaptureBatchCount: row.UnfinishedCaptureBatchCount,
		CaptureGapCount:             row.CaptureGapCount, FrozenSnapshotID: mixedRLTextValue(row.FrozenSnapshotID),
		SnapshotHash: mixedRLTextValue(row.SnapshotHash), FrozenAt: timeValue(row.FrozenAt),
		CreatedAt: timeValue(row.CreatedAt), UpdatedAt: timeValue(row.UpdatedAt),
	}
}

func mixedRLRunAgentRecord(row db.EnvDispatchRunAgent) EnvDispatchRunAgentRecord {
	return EnvDispatchRunAgentRecord{
		RunAgentID: row.RunAgentID, RunID: row.RunID, SourceAgentID: row.SourceAgentID,
		ExecutionAgentID: row.ExecutionAgentID, RuntimeID: row.RuntimeID,
		PiSessionID: row.PiSessionID, TrainingMode: row.TrainingMode,
		AReALSessionID: mixedRLTextValue(row.ArealSessionID), CaptureBoundary: row.CaptureBoundary,
		NextTurnOrdinal: row.NextTurnOrdinal, NextCallOrdinal: row.NextCallOrdinal,
		SettledAt: timeValue(row.SettledAt), CreatedAt: timeValue(row.CreatedAt),
	}
}

func residentTurnRecord(row db.EnvDispatchResidentTurn) ResidentTurnRecord {
	return ResidentTurnRecord{
		TurnID: row.TurnID, RunID: row.RunID, RunAgentID: row.RunAgentID,
		TurnOrdinal: row.TurnOrdinal, Status: row.Status,
		CaptureStartedAt:   timeValue(row.CaptureStartedAt),
		CaptureCompletedAt: timeValue(row.CaptureCompletedAt),
		AcceptedMessageIDs: row.AcceptedMessageIds, StartedAt: timeValue(row.StartedAt),
		CompletedAt: timeValue(row.CompletedAt),
	}
}

func deliveryObligationRecord(row db.EnvDispatchDeliveryObligation) DeliveryObligationRecord {
	return DeliveryObligationRecord{
		DeliveryID: row.DeliveryID, RunID: row.RunID, ChannelMessageID: row.ChannelMessageID,
		SourceRecipientAgentID: row.SourceRecipientAgentID, RunAgentID: row.RunAgentID,
		State: row.State, QueuedAt: timeValue(row.QueuedAt), SettledAt: timeValue(row.SettledAt),
		CreatedAt: timeValue(row.CreatedAt),
	}
}

func text(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func mixedRLTextValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func timeValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}

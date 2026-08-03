package voicecall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type VoiceCallQueries interface {
	CreateVoiceCallSession(
		ctx context.Context,
		params db.CreateVoiceCallSessionParams,
	) (db.VoiceCallSession, error)
	GetVoiceCallSessionForMember(
		ctx context.Context,
		params db.GetVoiceCallSessionForMemberParams,
	) (db.VoiceCallSession, error)
	UpsertVoiceCallProviderTurn(
		ctx context.Context,
		params db.UpsertVoiceCallProviderTurnParams,
	) (db.UpsertVoiceCallProviderTurnRow, error)
	BeginVoiceCallProviderStart(
		ctx context.Context,
		params db.BeginVoiceCallProviderStartParams,
	) (db.BeginVoiceCallProviderStartRow, error)
	MarkVoiceCallFailed(
		ctx context.Context,
		params db.MarkVoiceCallFailedParams,
	) (db.VoiceCallSession, error)
	ApplyVoiceCallProviderActive(
		ctx context.Context,
		params db.ApplyVoiceCallProviderActiveParams,
	) (db.VoiceCallSession, error)
	ApplyVoiceCallClientAnswered(
		ctx context.Context,
		params db.ApplyVoiceCallClientAnsweredParams,
	) (db.VoiceCallSession, error)
	ApplyVoiceCallProviderFailure(
		ctx context.Context,
		params db.ApplyVoiceCallProviderFailureParams,
	) (db.VoiceCallSession, error)
	BeginVoiceCallEnding(
		ctx context.Context,
		params db.BeginVoiceCallEndingParams,
	) (db.BeginVoiceCallEndingRow, error)
	MarkVoiceCallEnded(
		ctx context.Context,
		params db.MarkVoiceCallEndedParams,
	) (db.VoiceCallSession, error)
	FailVoiceCallSessionsForActivePair(
		ctx context.Context,
		params db.FailVoiceCallSessionsForActivePairParams,
	) ([]db.VoiceCallSession, error)
}

type PostgresStore struct {
	queries VoiceCallQueries
}

var (
	_ Store            = (*PostgresStore)(nil)
	_ VoiceCallQueries = (*db.Queries)(nil)
)

func NewPostgresStore(queries VoiceCallQueries) (*PostgresStore, error) {
	if queries == nil {
		return nil, errors.New("voice call queries are required")
	}
	return &PostgresStore{queries: queries}, nil
}

func (store *PostgresStore) CreateStarting(
	ctx context.Context,
	input NewSession,
) (Session, error) {
	workspaceID, err := parseVoiceCallUUID("workspace_id", input.WorkspaceID)
	if err != nil {
		return Session{}, err
	}
	channelID, err := parseVoiceCallUUID("channel_id", input.ChannelID)
	if err != nil {
		return Session{}, err
	}
	agentID, err := parseVoiceCallUUID("agent_id", input.AgentID)
	if err != nil {
		return Session{}, err
	}
	userID, err := parseVoiceCallUUID("user_id", input.UserID)
	if err != nil {
		return Session{}, err
	}
	provider := strings.TrimSpace(input.Provider)
	providerTaskID := strings.TrimSpace(input.ProviderTaskID)
	roomID := strings.TrimSpace(input.RoomID)
	if provider == "" || providerTaskID == "" || roomID == "" {
		return Session{}, errors.New("voice call provider, provider task, and room IDs are required")
	}

	row, err := store.queries.CreateVoiceCallSession(ctx, db.CreateVoiceCallSessionParams{
		WorkspaceID:    workspaceID,
		ChannelID:      channelID,
		AgentID:        agentID,
		UserID:         userID,
		Provider:       provider,
		ProviderTaskID: pgtype.Text{String: providerTaskID, Valid: true},
		RoomID:         pgtype.Text{String: roomID, Valid: true},
	})
	if err != nil {
		if voiceCallConstraintViolation(err, "voice_call_session_active_pair_idx") {
			return Session{}, errors.Join(
				ErrCallAlreadyActive,
				fmt.Errorf("insert voice call session: %w", err),
			)
		}
		return Session{}, fmt.Errorf("insert voice call session: %w", err)
	}
	return voiceCallSessionFromDB(row)
}

func (store *PostgresStore) Get(
	ctx context.Context,
	workspaceID string,
	userID string,
	callID string,
) (Session, error) {
	params, err := scopedVoiceCallParams(workspaceID, userID, callID)
	if err != nil {
		return Session{}, err
	}
	row, err := store.queries.GetVoiceCallSessionForMember(
		ctx,
		db.GetVoiceCallSessionForMemberParams(params),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, errors.Join(
				ErrCallNotFound,
				fmt.Errorf("select voice call session: %w", err),
			)
		}
		return Session{}, fmt.Errorf("select voice call session: %w", err)
	}
	return voiceCallSessionFromDB(row)
}

func (store *PostgresStore) UpsertProviderTurn(
	ctx context.Context,
	input ProviderTurnInput,
) (Turn, error) {
	provider, providerTaskID, err := validateProviderIdentity(
		input.Provider,
		input.ProviderTaskID,
	)
	if err != nil {
		return Turn{}, err
	}
	if input.Sequence <= 0 {
		return Turn{}, errors.New("voice call turn sequence must be positive")
	}
	if input.Speaker != SpeakerMember && input.Speaker != SpeakerAgent {
		return Turn{}, errors.New("voice call turn speaker must be member or agent")
	}
	if strings.TrimSpace(input.Transcript) == "" {
		return Turn{}, errors.New("voice call turn transcript is required")
	}
	providerTurnID := strings.TrimSpace(input.ProviderTurnID)
	if providerTurnID == "" {
		return Turn{}, errors.New("voice call provider turn ID is required")
	}

	row, err := store.queries.UpsertVoiceCallProviderTurn(
		ctx,
		db.UpsertVoiceCallProviderTurnParams{
			Sequence:       input.Sequence,
			Speaker:        string(input.Speaker),
			Transcript:     input.Transcript,
			IsInterrupted:  input.IsInterrupted,
			ProviderTurnID: pgtype.Text{String: providerTurnID, Valid: true},
			Provider:       provider,
			ProviderTaskID: pgtype.Text{String: providerTaskID, Valid: true},
		},
	)
	if err != nil {
		return Turn{}, classifyProviderCallbackStoreError(
			err,
			"upsert voice call provider turn",
		)
	}
	return voiceCallTurnFromDB(row)
}

func (store *PostgresStore) BeginProviderStart(
	ctx context.Context,
	workspaceID string,
	callID string,
) (BeginProviderStartResult, error) {
	parsedWorkspaceID, parsedCallID, err := workspaceCallUUIDs(workspaceID, callID)
	if err != nil {
		return BeginProviderStartResult{}, err
	}
	row, err := store.queries.BeginVoiceCallProviderStart(
		ctx,
		db.BeginVoiceCallProviderStartParams{
			ID:          parsedCallID,
			WorkspaceID: parsedWorkspaceID,
		},
	)
	if err != nil {
		return BeginProviderStartResult{}, fmt.Errorf("begin voice call provider start: %w", err)
	}
	session, err := voiceCallSessionFromDB(db.VoiceCallSession{
		ID:             row.ID,
		WorkspaceID:    row.WorkspaceID,
		ChannelID:      row.ChannelID,
		AgentID:        row.AgentID,
		UserID:         row.UserID,
		Provider:       row.Provider,
		ProviderTaskID: row.ProviderTaskID,
		RoomID:         row.RoomID,
		Status:         row.Status,
		StartedAt:      row.StartedAt,
		ConnectedAt:    row.ConnectedAt,
		EndedAt:        row.EndedAt,
		EndReason:      row.EndReason,
		ErrorCode:      row.ErrorCode,
		InputAudioMs:   row.InputAudioMs,
		OutputAudioMs:  row.OutputAudioMs,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	})
	if err != nil {
		return BeginProviderStartResult{}, err
	}
	return BeginProviderStartResult{
		Session:               session,
		ProviderStartRequired: row.ProviderStartRequired,
	}, nil
}

func (store *PostgresStore) MarkFailed(
	ctx context.Context,
	workspaceID string,
	callID string,
	errorCode string,
) (Session, error) {
	parsedWorkspaceID, parsedCallID, err := workspaceCallUUIDs(workspaceID, callID)
	if err != nil {
		return Session{}, err
	}
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" {
		return Session{}, errors.New("voice call error_code is required")
	}
	row, err := store.queries.MarkVoiceCallFailed(
		ctx,
		db.MarkVoiceCallFailedParams{
			ErrorCode:   errorCode,
			ID:          parsedCallID,
			WorkspaceID: parsedWorkspaceID,
		},
	)
	if err != nil {
		return Session{}, fmt.Errorf("update voice call to failed: %w", err)
	}
	return voiceCallSessionFromDB(row)
}

func (store *PostgresStore) ApplyProviderActive(
	ctx context.Context,
	provider string,
	providerTaskID string,
) (Session, error) {
	provider, providerTaskID, err := validateProviderIdentity(provider, providerTaskID)
	if err != nil {
		return Session{}, err
	}
	row, err := store.queries.ApplyVoiceCallProviderActive(
		ctx,
		db.ApplyVoiceCallProviderActiveParams{
			Provider:       provider,
			ProviderTaskID: pgtype.Text{String: providerTaskID, Valid: true},
		},
	)
	if err != nil {
		return Session{}, classifyProviderCallbackStoreError(
			err,
			"apply voice call provider activity",
		)
	}
	return voiceCallSessionFromDB(row)
}

func (store *PostgresStore) ApplyClientAnswered(
	ctx context.Context,
	workspaceID string,
	userID string,
	callID string,
) (Session, error) {
	params, err := scopedVoiceCallParams(workspaceID, userID, callID)
	if err != nil {
		return Session{}, err
	}
	row, err := store.queries.ApplyVoiceCallClientAnswered(
		ctx,
		db.ApplyVoiceCallClientAnsweredParams{
			ID:          params.ID,
			WorkspaceID: params.WorkspaceID,
			UserID:      params.UserID,
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrCallNotFound
		}
		return Session{}, fmt.Errorf("apply voice call client answer: %w", err)
	}
	return voiceCallSessionFromDB(row)
}

func (store *PostgresStore) ApplyProviderFailure(
	ctx context.Context,
	provider string,
	providerTaskID string,
	errorCode string,
) (Session, error) {
	provider, providerTaskID, err := validateProviderIdentity(provider, providerTaskID)
	if err != nil {
		return Session{}, err
	}
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" {
		return Session{}, errors.New("voice call provider error_code is required")
	}
	row, err := store.queries.ApplyVoiceCallProviderFailure(
		ctx,
		db.ApplyVoiceCallProviderFailureParams{
			ErrorCode:      errorCode,
			Provider:       provider,
			ProviderTaskID: pgtype.Text{String: providerTaskID, Valid: true},
		},
	)
	if err != nil {
		return Session{}, classifyProviderCallbackStoreError(
			err,
			"apply voice call provider failure",
		)
	}
	return voiceCallSessionFromDB(row)
}

func (store *PostgresStore) BeginEnding(
	ctx context.Context,
	workspaceID string,
	userID string,
	callID string,
	reason string,
) (BeginEndingResult, error) {
	params, err := scopedVoiceCallParams(workspaceID, userID, callID)
	if err != nil {
		return BeginEndingResult{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return BeginEndingResult{}, errors.New("voice call end_reason is required")
	}
	row, err := store.queries.BeginVoiceCallEnding(
		ctx,
		db.BeginVoiceCallEndingParams{
			ID:          params.ID,
			WorkspaceID: params.WorkspaceID,
			UserID:      params.UserID,
			EndReason:   reason,
		},
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BeginEndingResult{}, errors.Join(
				ErrCallNotFound,
				fmt.Errorf("update voice call to ending: %w", err),
			)
		}
		return BeginEndingResult{}, fmt.Errorf("update voice call to ending: %w", err)
	}
	session, err := voiceCallSessionFromDB(db.VoiceCallSession{
		ID:             row.ID,
		WorkspaceID:    row.WorkspaceID,
		ChannelID:      row.ChannelID,
		AgentID:        row.AgentID,
		UserID:         row.UserID,
		Provider:       row.Provider,
		ProviderTaskID: row.ProviderTaskID,
		RoomID:         row.RoomID,
		Status:         row.Status,
		StartedAt:      row.StartedAt,
		ConnectedAt:    row.ConnectedAt,
		EndedAt:        row.EndedAt,
		EndReason:      row.EndReason,
		ErrorCode:      row.ErrorCode,
		InputAudioMs:   row.InputAudioMs,
		OutputAudioMs:  row.OutputAudioMs,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	})
	if err != nil {
		return BeginEndingResult{}, err
	}
	return BeginEndingResult{
		Session:              session,
		ProviderStopRequired: row.ProviderStopRequired,
	}, nil
}

func (store *PostgresStore) FailActivePair(
	ctx context.Context,
	workspaceID string,
	userID string,
	agentID string,
	endReason string,
	errorCode string,
) (int, error) {
	parsedWorkspaceID, err := parseVoiceCallUUID("workspace_id", workspaceID)
	if err != nil {
		return 0, err
	}
	parsedUserID, err := parseVoiceCallUUID("user_id", userID)
	if err != nil {
		return 0, err
	}
	parsedAgentID, err := parseVoiceCallUUID("agent_id", agentID)
	if err != nil {
		return 0, err
	}
	endReason = strings.TrimSpace(endReason)
	errorCode = strings.TrimSpace(errorCode)
	if endReason == "" || errorCode == "" {
		return 0, errors.New("voice call end_reason and error_code are required")
	}
	rows, err := store.queries.FailVoiceCallSessionsForActivePair(
		ctx,
		db.FailVoiceCallSessionsForActivePairParams{
			EndReason:   endReason,
			ErrorCode:   errorCode,
			WorkspaceID: parsedWorkspaceID,
			UserID:      parsedUserID,
			AgentID:     parsedAgentID,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("fail active voice call pair: %w", err)
	}
	return len(rows), nil
}

func (store *PostgresStore) MarkEnded(
	ctx context.Context,
	workspaceID string,
	callID string,
	reason string,
) (Session, error) {
	parsedWorkspaceID, parsedCallID, err := workspaceCallUUIDs(workspaceID, callID)
	if err != nil {
		return Session{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Session{}, errors.New("voice call end_reason is required")
	}
	row, err := store.queries.MarkVoiceCallEnded(
		ctx,
		db.MarkVoiceCallEndedParams{
			EndReason:   reason,
			ID:          parsedCallID,
			WorkspaceID: parsedWorkspaceID,
		},
	)
	if err != nil {
		return Session{}, fmt.Errorf("update voice call to ended: %w", err)
	}
	return voiceCallSessionFromDB(row)
}

func validateProviderIdentity(provider string, providerTaskID string) (string, string, error) {
	provider = strings.TrimSpace(provider)
	providerTaskID = strings.TrimSpace(providerTaskID)
	if provider == "" || providerTaskID == "" {
		return "", "", errors.New("voice call provider and provider task ID are required")
	}
	return provider, providerTaskID, nil
}

func classifyProviderCallbackStoreError(err error, operation string) error {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.Join(ErrCallNotFound, wrapped)
	}
	return wrapped
}

func scopedVoiceCallParams(
	workspaceID string,
	userID string,
	callID string,
) (db.GetVoiceCallSessionForMemberParams, error) {
	parsedWorkspaceID, err := parseVoiceCallUUID("workspace_id", workspaceID)
	if err != nil {
		return db.GetVoiceCallSessionForMemberParams{}, err
	}
	parsedUserID, err := parseVoiceCallUUID("user_id", userID)
	if err != nil {
		return db.GetVoiceCallSessionForMemberParams{}, err
	}
	parsedCallID, err := parseVoiceCallUUID("call_id", callID)
	if err != nil {
		return db.GetVoiceCallSessionForMemberParams{}, err
	}
	return db.GetVoiceCallSessionForMemberParams{
		ID:          parsedCallID,
		WorkspaceID: parsedWorkspaceID,
		UserID:      parsedUserID,
	}, nil
}

func workspaceCallUUIDs(
	workspaceID string,
	callID string,
) (pgtype.UUID, pgtype.UUID, error) {
	parsedWorkspaceID, err := parseVoiceCallUUID("workspace_id", workspaceID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	parsedCallID, err := parseVoiceCallUUID("call_id", callID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return parsedWorkspaceID, parsedCallID, nil
}

func parseVoiceCallUUID(field string, value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("voice call %s must be a UUID: %w", field, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func voiceCallSessionFromDB(row db.VoiceCallSession) (Session, error) {
	id, err := voiceCallUUIDString("id", row.ID)
	if err != nil {
		return Session{}, err
	}
	workspaceID, err := voiceCallUUIDString("workspace_id", row.WorkspaceID)
	if err != nil {
		return Session{}, err
	}
	channelID, err := voiceCallUUIDString("channel_id", row.ChannelID)
	if err != nil {
		return Session{}, err
	}
	agentID, err := voiceCallUUIDString("agent_id", row.AgentID)
	if err != nil {
		return Session{}, err
	}
	userID, err := voiceCallUUIDString("user_id", row.UserID)
	if err != nil {
		return Session{}, err
	}
	if !row.StartedAt.Valid || !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return Session{}, errors.New("voice call database row has invalid required timestamps")
	}
	if !row.ProviderTaskID.Valid || !row.RoomID.Valid {
		return Session{}, errors.New("voice call database row has no provider identity")
	}
	return Session{
		ID:             id,
		WorkspaceID:    workspaceID,
		ChannelID:      channelID,
		AgentID:        agentID,
		UserID:         userID,
		Provider:       row.Provider,
		ProviderTaskID: row.ProviderTaskID.String,
		RoomID:         row.RoomID.String,
		Status:         Status(row.Status),
		StartedAt:      row.StartedAt.Time,
		ConnectedAt:    optionalVoiceCallTime(row.ConnectedAt),
		EndedAt:        optionalVoiceCallTime(row.EndedAt),
		EndReason:      row.EndReason,
		ErrorCode:      row.ErrorCode,
		InputAudioMS:   row.InputAudioMs,
		OutputAudioMS:  row.OutputAudioMs,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}, nil
}

func voiceCallUUIDString(field string, value pgtype.UUID) (string, error) {
	if !value.Valid {
		return "", fmt.Errorf("voice call database row has invalid %s", field)
	}
	return uuid.UUID(value.Bytes).String(), nil
}

func optionalVoiceCallTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func voiceCallTurnFromDB(row db.UpsertVoiceCallProviderTurnRow) (Turn, error) {
	id, err := voiceCallUUIDString("turn id", row.ID)
	if err != nil {
		return Turn{}, err
	}
	callSessionID, err := voiceCallUUIDString("turn call_session_id", row.CallSessionID)
	if err != nil {
		return Turn{}, err
	}
	if !row.ProviderTurnID.Valid {
		return Turn{}, errors.New("voice call database row has no provider turn identity")
	}
	return Turn{
		ID:             id,
		CallSessionID:  callSessionID,
		Sequence:       row.Sequence,
		Speaker:        Speaker(row.Speaker),
		Transcript:     row.Transcript,
		IsInterrupted:  row.IsInterrupted,
		ProviderTurnID: row.ProviderTurnID.String,
	}, nil
}

func voiceCallConstraintViolation(err error, constraint string) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) &&
		postgresError.Code == "23505" &&
		postgresError.ConstraintName == constraint
}

type voiceCallRestartRecoveryQueries interface {
	FailVoiceCallSessionsStartedBefore(
		context.Context,
		pgtype.Timestamptz,
	) ([]db.VoiceCallSession, error)
}

// RecoverInterruptedSessionsAtStartup makes sessions created before this server
// process terminal. Their provider lifecycle cannot be safely resumed after a
// process restart, and leaving them non-terminal blocks a caller from retrying.
func (store *PostgresStore) RecoverInterruptedSessionsAtStartup(
	timeout time.Duration,
	startedBefore time.Time,
) ([]Session, error) {
	if timeout <= 0 {
		return nil, errors.New("voice call recovery timeout must be positive")
	}
	if startedBefore.IsZero() {
		return nil, errors.New("voice call recovery cutoff is required")
	}
	recoveryQueries, ok := store.queries.(voiceCallRestartRecoveryQueries)
	if !ok {
		return nil, errors.New("voice call queries do not support restart recovery")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	rows, err := recoveryQueries.FailVoiceCallSessionsStartedBefore(ctx, pgtype.Timestamptz{
		Time:  startedBefore.UTC(),
		Valid: true,
	})
	if err != nil {
		return nil, fmt.Errorf("fail interrupted voice call sessions: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		session, err := voiceCallSessionFromDB(row)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

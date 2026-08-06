package voicecall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Status string

var (
	ErrScopeNotFound     = errors.New("voice call scope not found")
	ErrScopeForbidden    = errors.New("voice call scope forbidden")
	ErrScopeUnavailable  = errors.New("voice call scope unavailable")
	ErrCallNotFound      = errors.New("voice call not found")
	ErrCallAlreadyActive = errors.New("voice call already active")
	ErrProviderFailure   = errors.New("voice call provider failure")
)

const (
	StatusStarting     Status = "starting"
	StatusConnecting   Status = "connecting"
	StatusActive       Status = "active"
	StatusReconnecting Status = "reconnecting"
	StatusEnding       Status = "ending"
	StatusEnded        Status = "ended"
	StatusFailed       Status = "failed"
)

const (
	providerRoomIDPrefix   = "voice-call-"
	providerTaskIDPrefix   = "voice-task-"
	providerMemberIDPrefix = "voice-member-"
	providerAgentIDPrefix  = "voice-agent-"
)

type Scope struct {
	WorkspaceID string
	ChannelID   string
	AgentID     string
	UserID      string
}

type Session struct {
	ID             string
	WorkspaceID    string
	ChannelID      string
	AgentID        string
	UserID         string
	Provider       string
	ProviderTaskID string
	RoomID         string
	Status         Status
	StartedAt      time.Time
	ConnectedAt    *time.Time
	EndedAt        *time.Time
	EndReason      string
	ErrorCode      string
	InputAudioMS   int64
	OutputAudioMS  int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Speaker string

const (
	SpeakerMember Speaker = "member"
	SpeakerAgent  Speaker = "agent"
)

type Turn struct {
	ID             string
	CallSessionID  string
	Sequence       int64
	Speaker        Speaker
	Transcript     string
	IsInterrupted  bool
	ProviderTurnID string
}

type ProviderTurnInput struct {
	Provider       string
	ProviderTaskID string
	Sequence       int64
	Speaker        Speaker
	Transcript     string
	IsInterrupted  bool
	ProviderTurnID string
}

type NewSession struct {
	WorkspaceID    string
	ChannelID      string
	AgentID        string
	UserID         string
	Provider       string
	ProviderTaskID string
	RoomID         string
}

type StartInput struct {
	WorkspaceID string
	ChannelID   string
	AgentID     string
	UserID      string
}

type StopInput struct {
	WorkspaceID string
	UserID      string
	CallID      string
	Reason      string
}

type ConnectInput struct {
	WorkspaceID string
	UserID      string
	CallID      string
}

type AnswerInput struct {
	WorkspaceID string
	UserID      string
	CallID      string
}

type ConversationContext struct {
	WelcomeMessage string
	SystemMessages []string
}

type ProviderPrepareInput struct {
	RoomID       string
	TargetUserID string
}

type ProviderConnectInput struct {
	RoomID         string
	TaskID         string
	TargetUserID   string
	AgentUserID    string
	WelcomeMessage string
	SystemMessages []string
}

type ProviderPrepareResult struct {
	AppID     string
	Token     string
	ExpiresAt time.Time
}

// ProviderStartUncertainError means the provider start result is unknown and a
// compensating provider stop also failed. The session must stay non-terminal
// so recovery can find the possibly running task.
type ProviderStartUncertainError struct {
	Err error
}

func (failure *ProviderStartUncertainError) Error() string {
	if failure == nil || failure.Err == nil {
		return "voice call provider start result is uncertain"
	}
	return failure.Err.Error()
}

func (failure *ProviderStartUncertainError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

type ProviderCallIdentity struct {
	RoomID string
	TaskID string
}

type MediaCredentials struct {
	AppID     string
	RoomID    string
	UserID    string
	Token     string
	ExpiresAt time.Time
}

type StartResult struct {
	Session Session
	Media   MediaCredentials
}

type BeginEndingResult struct {
	Session              Session
	ProviderStopRequired bool
}

type BeginProviderStartResult struct {
	Session               Session
	ProviderStartRequired bool
}

type Store interface {
	CreateStarting(ctx context.Context, input NewSession) (Session, error)
	Get(ctx context.Context, workspaceID, userID, callID string) (Session, error)
	BeginProviderStart(ctx context.Context, workspaceID, callID string) (BeginProviderStartResult, error)
	ApplyClientAnswered(ctx context.Context, workspaceID, userID, callID string) (Session, error)
	MarkFailed(ctx context.Context, workspaceID, callID, errorCode string) (Session, error)
	BeginEnding(ctx context.Context, workspaceID, userID, callID, reason string) (BeginEndingResult, error)
	MarkEnded(ctx context.Context, workspaceID, callID, reason string) (Session, error)
}

type Authorizer interface {
	Authorize(ctx context.Context, scope Scope) error
}

type ContextBuilder interface {
	Build(ctx context.Context, scope Scope) (ConversationContext, error)
}

// Provider.Connect must either start the agent or return with no provider task
// left running. It owns compensation for ambiguous transport failures inside
// its protocol boundary.
type Provider interface {
	Prepare(ctx context.Context, input ProviderPrepareInput) (ProviderPrepareResult, error)
	Connect(ctx context.Context, input ProviderConnectInput) error
	Stop(ctx context.Context, identity ProviderCallIdentity) error
}

type ServiceConfig struct {
	ProviderName   string
	IDGenerator    func() string
	CleanupTimeout time.Duration
}

type Service struct {
	providerName   string
	idGenerator    func() string
	cleanupTimeout time.Duration
	store          Store
	authorizer     Authorizer
	contextBuilder ContextBuilder
	provider       Provider
}

func NewService(
	config ServiceConfig,
	store Store,
	authorizer Authorizer,
	contextBuilder ContextBuilder,
	provider Provider,
) (*Service, error) {
	providerName := strings.TrimSpace(config.ProviderName)
	if providerName == "" {
		return nil, errors.New("voice call provider name is required")
	}
	if config.IDGenerator == nil {
		return nil, errors.New("voice call ID generator is required")
	}
	if config.CleanupTimeout <= 0 {
		return nil, errors.New("voice call cleanup timeout must be positive")
	}
	if store == nil {
		return nil, errors.New("voice call store is required")
	}
	if authorizer == nil {
		return nil, errors.New("voice call authorizer is required")
	}
	if contextBuilder == nil {
		return nil, errors.New("voice call context builder is required")
	}
	if provider == nil {
		return nil, errors.New("voice call provider is required")
	}
	return &Service{
		providerName:   providerName,
		idGenerator:    config.IDGenerator,
		cleanupTimeout: config.CleanupTimeout,
		store:          store,
		authorizer:     authorizer,
		contextBuilder: contextBuilder,
		provider:       provider,
	}, nil
}

func (service *Service) Start(ctx context.Context, input StartInput) (StartResult, error) {
	scope, err := validateStartInput(input)
	if err != nil {
		return StartResult{}, err
	}
	if err := service.authorizer.Authorize(ctx, scope); err != nil {
		return StartResult{}, fmt.Errorf("authorize voice call: %w", err)
	}

	nonce := strings.TrimSpace(service.idGenerator())
	if err := validateNonce(nonce); err != nil {
		return StartResult{}, err
	}
	roomID := providerRoomIDPrefix + nonce
	taskID := providerTaskIDPrefix + nonce
	memberUserID := providerMemberIDPrefix + nonce

	session, err := service.store.CreateStarting(ctx, NewSession{
		WorkspaceID:    scope.WorkspaceID,
		ChannelID:      scope.ChannelID,
		AgentID:        scope.AgentID,
		UserID:         scope.UserID,
		Provider:       service.providerName,
		ProviderTaskID: taskID,
		RoomID:         roomID,
	})
	if err != nil {
		return StartResult{}, fmt.Errorf("create starting voice call: %w", err)
	}

	providerResult, err := service.provider.Prepare(ctx, ProviderPrepareInput{
		RoomID:       roomID,
		TargetUserID: memberUserID,
	})
	if err != nil {
		return StartResult{}, providerFailure(
			service.recordFailed(
				ctx,
				session,
				"media_prepare_failed",
				fmt.Errorf("prepare voice call media: %w", err),
			),
		)
	}
	if err := validateProviderPrepareResult(providerResult); err != nil {
		return StartResult{}, providerFailure(
			service.recordFailed(ctx, session, "provider_response_invalid", err),
		)
	}
	return StartResult{
		Session: session,
		Media: MediaCredentials{
			AppID:     strings.TrimSpace(providerResult.AppID),
			RoomID:    roomID,
			UserID:    memberUserID,
			Token:     strings.TrimSpace(providerResult.Token),
			ExpiresAt: providerResult.ExpiresAt,
		},
	}, nil
}

func (service *Service) Connect(ctx context.Context, input ConnectInput) (Session, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CallID = strings.TrimSpace(input.CallID)
	if input.WorkspaceID == "" || input.UserID == "" || input.CallID == "" {
		return Session{}, errors.New("voice call workspace, user, and call IDs are required")
	}

	session, err := service.store.Get(ctx, input.WorkspaceID, input.UserID, input.CallID)
	if err != nil {
		return Session{}, fmt.Errorf("get voice call for provider start: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, sessionScope(session)); err != nil {
		return Session{}, fmt.Errorf("authorize voice call provider start: %w", err)
	}
	switch session.Status {
	case StatusConnecting, StatusActive, StatusReconnecting:
		return session, nil
	case StatusStarting:
	default:
		return Session{}, ErrScopeUnavailable
	}

	conversationContext, err := service.contextBuilder.Build(ctx, sessionScope(session))
	if err != nil {
		return Session{}, service.recordFailed(
			ctx, session, "context_failed", fmt.Errorf("build voice call context: %w", err),
		)
	}
	if err := validateConversationContext(conversationContext); err != nil {
		return Session{}, service.recordFailed(ctx, session, "context_failed", err)
	}

	starting, err := service.store.BeginProviderStart(
		ctx, session.WorkspaceID, session.ID,
	)
	if err != nil {
		return Session{}, fmt.Errorf("claim voice call provider start: %w", err)
	}
	if !starting.ProviderStartRequired {
		return starting.Session, nil
	}
	session = starting.Session

	nonce := strings.TrimPrefix(session.RoomID, providerRoomIDPrefix)
	if nonce == session.RoomID || nonce == "" {
		return Session{}, service.recordFailed(
			ctx, session, "provider_identity_invalid",
			errors.New("voice call room ID does not contain the expected nonce"),
		)
	}
	if err := service.provider.Connect(ctx, ProviderConnectInput{
		RoomID:         session.RoomID,
		TaskID:         session.ProviderTaskID,
		TargetUserID:   providerMemberIDPrefix + nonce,
		AgentUserID:    providerAgentIDPrefix + nonce,
		WelcomeMessage: strings.TrimSpace(conversationContext.WelcomeMessage),
		SystemMessages: append([]string(nil), conversationContext.SystemMessages...),
	}); err != nil {
		var uncertain *ProviderStartUncertainError
		if errors.As(err, &uncertain) {
			return Session{}, providerFailure(
				fmt.Errorf("start voice call provider: %w", err),
			)
		}
		return Session{}, providerFailure(
			service.recordFailed(
				ctx,
				session,
				"provider_start_failed",
				fmt.Errorf("start voice call provider: %w", err),
			),
		)
	}
	return session, nil
}

// Answer records that the caller heard the expected agent remote audio. Used
// when Volcengine lifecycle callbacks cannot reach the public origin, so
// connected_at / active still match the audible evidence path.
func (service *Service) Answer(ctx context.Context, input AnswerInput) (Session, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CallID = strings.TrimSpace(input.CallID)
	if input.WorkspaceID == "" || input.UserID == "" || input.CallID == "" {
		return Session{}, errors.New("voice call workspace, user, and call IDs are required")
	}

	session, err := service.store.Get(ctx, input.WorkspaceID, input.UserID, input.CallID)
	if err != nil {
		return Session{}, fmt.Errorf("get voice call for client answer: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, sessionScope(session)); err != nil {
		return Session{}, fmt.Errorf("authorize voice call client answer: %w", err)
	}
	switch session.Status {
	case StatusActive, StatusReconnecting:
		if session.ConnectedAt != nil && session.Status == StatusActive {
			return session, nil
		}
	case StatusStarting, StatusConnecting:
	default:
		return Session{}, ErrScopeUnavailable
	}

	answered, err := service.store.ApplyClientAnswered(
		ctx,
		input.WorkspaceID,
		input.UserID,
		input.CallID,
	)
	if err != nil {
		if errors.Is(err, ErrCallNotFound) {
			return Session{}, ErrScopeUnavailable
		}
		return Session{}, fmt.Errorf("apply voice call client answer: %w", err)
	}
	return answered, nil
}

func (service *Service) Get(ctx context.Context, workspaceID, userID, callID string) (Session, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	userID = strings.TrimSpace(userID)
	callID = strings.TrimSpace(callID)
	if workspaceID == "" || userID == "" || callID == "" {
		return Session{}, errors.New("voice call workspace, user, and call IDs are required")
	}
	session, err := service.store.Get(ctx, workspaceID, userID, callID)
	if err != nil {
		return Session{}, fmt.Errorf("get voice call: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, sessionScope(session)); err != nil {
		return Session{}, fmt.Errorf("authorize voice call: %w", err)
	}
	return session, nil
}

func (service *Service) Stop(ctx context.Context, input StopInput) (Session, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CallID = strings.TrimSpace(input.CallID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.WorkspaceID == "" || input.UserID == "" || input.CallID == "" || input.Reason == "" {
		return Session{}, errors.New("voice call workspace, user, call IDs, and stop reason are required")
	}

	cleanupContext, cancel := service.newCleanupContext(ctx)
	defer cancel()
	session, err := service.store.Get(cleanupContext, input.WorkspaceID, input.UserID, input.CallID)
	if err != nil {
		return Session{}, fmt.Errorf("get voice call for stop: %w", err)
	}
	if err := service.authorizer.Authorize(cleanupContext, sessionScope(session)); err != nil {
		return Session{}, fmt.Errorf("authorize voice call stop: %w", err)
	}
	ending, err := service.store.BeginEnding(
		cleanupContext, input.WorkspaceID, input.UserID, input.CallID, input.Reason,
	)
	if err != nil {
		return Session{}, fmt.Errorf("begin ending voice call: %w", err)
	}
	if !ending.ProviderStopRequired {
		if ending.Session.Status == StatusEnding {
			session, err = service.store.MarkEnded(
				cleanupContext,
				ending.Session.WorkspaceID,
				ending.Session.ID,
				ending.Session.EndReason,
			)
			if err != nil {
				return Session{}, fmt.Errorf("mark unstarted voice call ended: %w", err)
			}
			return session, nil
		}
		return ending.Session, nil
	}

	if err := service.provider.Stop(cleanupContext, ProviderCallIdentity{
		RoomID: ending.Session.RoomID,
		TaskID: ending.Session.ProviderTaskID,
	}); err != nil {
		return Session{}, providerFailure(
			fmt.Errorf("stop voice call provider: %w", err),
		)
	}
	session, err = service.store.MarkEnded(
		cleanupContext, ending.Session.WorkspaceID, ending.Session.ID, ending.Session.EndReason,
	)
	if err != nil {
		return Session{}, fmt.Errorf("mark voice call ended: %w", err)
	}
	return session, nil
}

func (service *Service) compensateStarted(
	ctx context.Context,
	session Session,
	identity ProviderCallIdentity,
	errorCode string,
	cause error,
) error {
	cleanupContext, cancel := service.newCleanupContext(ctx)
	defer cancel()
	if err := service.provider.Stop(cleanupContext, identity); err != nil {
		return errors.Join(cause, fmt.Errorf("compensate voice call provider: %w", err))
	}
	_, err := service.store.MarkFailed(
		cleanupContext, session.WorkspaceID, session.ID, errorCode,
	)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("mark voice call failed after compensation: %w", err))
	}
	return cause
}

func (service *Service) recordFailed(
	ctx context.Context,
	session Session,
	errorCode string,
	cause error,
) error {
	cleanupContext, cancel := service.newCleanupContext(ctx)
	defer cancel()
	_, err := service.store.MarkFailed(
		cleanupContext, session.WorkspaceID, session.ID, errorCode,
	)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("mark voice call failed: %w", err))
	}
	return cause
}

func (service *Service) newCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), service.cleanupTimeout)
}

func providerFailure(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(ErrProviderFailure, err)
}

func validateStartInput(input StartInput) (Scope, error) {
	scope := Scope{
		WorkspaceID: strings.TrimSpace(input.WorkspaceID),
		ChannelID:   strings.TrimSpace(input.ChannelID),
		AgentID:     strings.TrimSpace(input.AgentID),
		UserID:      strings.TrimSpace(input.UserID),
	}
	if scope.WorkspaceID == "" ||
		scope.ChannelID == "" ||
		scope.AgentID == "" ||
		scope.UserID == "" {
		return Scope{}, errors.New("voice call workspace, channel, agent, and user IDs are required")
	}
	return scope, nil
}

func validateNonce(value string) error {
	if value == "" {
		return errors.New("voice call ID generator returned an empty value")
	}
	if len(value) > 96 {
		return errors.New("voice call ID generator returned a value longer than 96 characters")
	}
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' {
			continue
		}
		return errors.New("voice call ID generator returned an unsupported character")
	}
	return nil
}

func validateConversationContext(callContext ConversationContext) error {
	if strings.TrimSpace(callContext.WelcomeMessage) == "" {
		return errors.New("voice call context requires a welcome message")
	}
	if len(callContext.SystemMessages) == 0 {
		return errors.New("voice call context requires at least one system message")
	}
	for _, message := range callContext.SystemMessages {
		if strings.TrimSpace(message) == "" {
			return errors.New("voice call context contains a blank system message")
		}
	}
	return nil
}

func validateProviderPrepareResult(result ProviderPrepareResult) error {
	if strings.TrimSpace(result.AppID) == "" ||
		strings.TrimSpace(result.Token) == "" ||
		result.ExpiresAt.IsZero() {
		return errors.New("voice call provider returned incomplete media credentials")
	}
	return nil
}

func sessionScope(session Session) Scope {
	return Scope{
		WorkspaceID: session.WorkspaceID,
		ChannelID:   session.ChannelID,
		AgentID:     session.AgentID,
		UserID:      session.UserID,
	}
}

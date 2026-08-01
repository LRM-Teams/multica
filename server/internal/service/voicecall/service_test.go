package voicecall

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestServiceStartPreparesMediaWithoutStartingProvider(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	expiresAt := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	deps.provider.prepareResult = ProviderPrepareResult{
		AppID:     "rtc-app",
		Token:     "room-token",
		ExpiresAt: expiresAt,
	}

	result, err := service.Start(context.Background(), StartInput{
		WorkspaceID: "workspace-1",
		ChannelID:   "dm-1",
		AgentID:     "beckham-1",
		UserID:      "member-1",
	})
	if err != nil {
		t.Fatalf("start call: %v", err)
	}

	wantOrder := []string{"authorize", "create", "provider_prepare"}
	if !reflect.DeepEqual(deps.order, wantOrder) {
		t.Fatalf("order = %v, want %v", deps.order, wantOrder)
	}
	if result.Session.Status != StatusStarting {
		t.Fatalf("status = %q, want %q", result.Session.Status, StatusStarting)
	}
	if result.Media.AppID != "rtc-app" ||
		result.Media.RoomID != "voice-call-nonce-1" ||
		result.Media.UserID != "voice-member-nonce-1" ||
		result.Media.Token != "room-token" ||
		!result.Media.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("media = %+v", result.Media)
	}
	wantProviderInput := ProviderPrepareInput{
		RoomID:       "voice-call-nonce-1",
		TargetUserID: "voice-member-nonce-1",
	}
	if !reflect.DeepEqual(deps.provider.prepareInput, wantProviderInput) {
		t.Fatalf("provider input = %+v, want %+v", deps.provider.prepareInput, wantProviderInput)
	}
	if deps.provider.connectCalls != 0 {
		t.Fatalf("provider started before caller joined: %d", deps.provider.connectCalls)
	}
}

func TestServiceAnswerPromotesConnectingCallToActiveWithConnectedAt(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	if _, err := service.Start(context.Background(), validStartInput()); err != nil {
		t.Fatalf("start call: %v", err)
	}
	if _, err := service.Connect(context.Background(), ConnectInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	}); err != nil {
		t.Fatalf("connect call: %v", err)
	}
	deps.order = nil

	session, err := service.Answer(context.Background(), AnswerInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	})
	if err != nil {
		t.Fatalf("answer call: %v", err)
	}
	if session.Status != StatusActive || session.ConnectedAt == nil {
		t.Fatalf("session = %+v", session)
	}
	if !reflect.DeepEqual(deps.order, []string{"get", "authorize", "client_answer"}) {
		t.Fatalf("order = %v", deps.order)
	}

	deps.order = nil
	again, err := service.Answer(context.Background(), AnswerInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	})
	if err != nil {
		t.Fatalf("repeat answer: %v", err)
	}
	if again.Status != StatusActive || again.ConnectedAt == nil {
		t.Fatalf("repeat session = %+v", again)
	}
	if !reflect.DeepEqual(deps.order, []string{"get", "authorize"}) {
		t.Fatalf("repeat order = %v", deps.order)
	}
}

func TestServiceConnectStartsProviderWithWelcomeOnlyAfterCallerJoined(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	_, err := service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("start call: %v", err)
	}
	if deps.provider.connectCalls != 0 {
		t.Fatalf("provider calls before room join = %d, want 0", deps.provider.connectCalls)
	}
	deps.order = nil

	session, err := service.Connect(context.Background(), ConnectInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	})
	if err != nil {
		t.Fatalf("connect after room join: %v", err)
	}

	if session.ID != "call-1" || session.Status != StatusConnecting {
		t.Fatalf("session = %+v", session)
	}
	if deps.provider.connectCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", deps.provider.connectCalls)
	}
	wantProviderInput := ProviderConnectInput{
		RoomID:         "voice-call-nonce-1",
		TaskID:         "voice-task-nonce-1",
		TargetUserID:   "voice-member-nonce-1",
		AgentUserID:    "voice-agent-nonce-1",
		WelcomeMessage: "你好，我是贝克汉姆。",
		SystemMessages: []string{"You are Beckham.", "Use the current DM context."},
	}
	if !reflect.DeepEqual(deps.provider.connectInput, wantProviderInput) {
		t.Fatalf("provider input = %+v, want %+v", deps.provider.connectInput, wantProviderInput)
	}
	if !reflect.DeepEqual(
		deps.order,
		[]string{"get", "authorize", "context", "provider_start_claim", "provider_connect"},
	) {
		t.Fatalf("order = %v", deps.order)
	}

	deps.order = nil
	if _, err := service.Connect(context.Background(), ConnectInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	}); err != nil {
		t.Fatalf("repeat connect: %v", err)
	}
	if deps.provider.connectCalls != 1 {
		t.Fatalf("repeat connect started provider %d times", deps.provider.connectCalls)
	}
	if !reflect.DeepEqual(deps.order, []string{"get", "authorize"}) {
		t.Fatalf("repeat order = %v", deps.order)
	}
}

func TestServiceStartRejectsUnauthorizedCallBeforePersistence(t *testing.T) {
	deps := newTestDependencies()
	deps.authorizer.err = errors.New("not the canonical agent DM")
	service := newTestService(t, deps)

	_, err := service.Start(context.Background(), validStartInput())
	if err == nil || !strings.Contains(err.Error(), "authorize voice call") {
		t.Fatalf("error = %v", err)
	}
	if deps.store.createCalls != 0 || deps.provider.prepareCalls != 0 {
		t.Fatalf("unauthorized call reached store/provider: store=%d provider=%d", deps.store.createCalls, deps.provider.prepareCalls)
	}
}

func TestServiceStartRecordsMediaPreparationFailure(t *testing.T) {
	deps := newTestDependencies()
	deps.provider.prepareErr = errors.New("token unavailable")
	service := newTestService(t, deps)

	_, err := service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("error = %v, want ErrProviderFailure", err)
	}
	if deps.store.session.Status != StatusFailed ||
		deps.store.session.ErrorCode != "media_prepare_failed" {
		t.Fatalf("session = %+v", deps.store.session)
	}
}

func TestServiceConnectRecordsContextAndProviderFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testDependencies)
		wantCode  string
	}{
		{
			name: "context",
			configure: func(deps *testDependencies) {
				deps.context.err = errors.New("context unavailable")
			},
			wantCode: "context_failed",
		},
		{
			name: "provider",
			configure: func(deps *testDependencies) {
				deps.provider.connectErr = errors.New("provider unavailable")
			},
			wantCode: "provider_start_failed",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			deps := newTestDependencies()
			service := newTestService(t, deps)
			if _, err := service.Start(context.Background(), validStartInput()); err != nil {
				t.Fatalf("prepare call: %v", err)
			}
			testCase.configure(deps)

			_, err := service.Connect(context.Background(), ConnectInput{
				WorkspaceID: "workspace-1",
				UserID:      "member-1",
				CallID:      "call-1",
			})
			if err == nil {
				t.Fatal("connect call succeeded")
			}
			if testCase.name == "provider" && !errors.Is(err, ErrProviderFailure) {
				t.Fatalf("provider error = %v, want ErrProviderFailure", err)
			}
			if testCase.name == "context" && errors.Is(err, ErrProviderFailure) {
				t.Fatalf("context error = %v, must not be classified as provider failure", err)
			}
			if deps.store.session.Status != StatusFailed ||
				deps.store.session.ErrorCode != testCase.wantCode {
				t.Fatalf("session = %+v, want failed/%s", deps.store.session, testCase.wantCode)
			}
		})
	}
}

func TestServiceConnectLeavesSessionRecoverableWhenProviderStartIsUncertain(t *testing.T) {
	deps := newTestDependencies()
	deps.provider.connectErr = &ProviderStartUncertainError{
		Err: errors.New("start timed out and compensating stop failed"),
	}
	service := newTestService(t, deps)
	if _, err := service.Start(context.Background(), validStartInput()); err != nil {
		t.Fatalf("prepare call: %v", err)
	}

	_, err := service.Connect(context.Background(), ConnectInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	})
	var uncertain *ProviderStartUncertainError
	if !errors.As(err, &uncertain) {
		t.Fatalf("error = %v, want ProviderStartUncertainError", err)
	}
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("error = %v, want ErrProviderFailure", err)
	}
	if deps.store.session.Status != StatusConnecting {
		t.Fatalf("status = %q, want recoverable connecting", deps.store.session.Status)
	}
	if deps.store.markFailedCalls != 0 {
		t.Fatalf("failed transition calls = %d, want 0", deps.store.markFailedCalls)
	}
}

func TestServiceConnectDoesNotStartProviderWhenClaimFails(t *testing.T) {
	deps := newTestDependencies()
	deps.store.markConnectingErr = errors.New("database unavailable")
	service := newTestService(t, deps)
	if _, err := service.Start(context.Background(), validStartInput()); err != nil {
		t.Fatalf("prepare call: %v", err)
	}
	deps.order = nil

	_, err := service.Connect(context.Background(), ConnectInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	})
	if err == nil || !strings.Contains(err.Error(), "claim voice call provider start") {
		t.Fatalf("error = %v", err)
	}
	wantOrder := []string{"get", "authorize", "context", "provider_start_claim"}
	if !reflect.DeepEqual(deps.order, wantOrder) {
		t.Fatalf("order = %v, want %v", deps.order, wantOrder)
	}
	if deps.store.session.Status != StatusStarting || deps.provider.connectCalls != 0 {
		t.Fatalf("session = %+v", deps.store.session)
	}
}

func TestServiceStopEndsPreparedCallWithoutStoppingAbsentProvider(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	if _, err := service.Start(context.Background(), validStartInput()); err != nil {
		t.Fatalf("prepare call: %v", err)
	}
	deps.order = nil

	session, err := service.Stop(context.Background(), StopInput{
		WorkspaceID: deps.store.session.WorkspaceID,
		UserID:      deps.store.session.UserID,
		CallID:      deps.store.session.ID,
		Reason:      "browser_closed",
	})
	if err != nil {
		t.Fatalf("stop prepared call: %v", err)
	}
	if session.Status != StatusEnded || deps.provider.stopCalls != 0 {
		t.Fatalf("session=%+v provider stops=%d", session, deps.provider.stopCalls)
	}
	if !reflect.DeepEqual(
		deps.order,
		[]string{"get", "authorize", "ending", "ended"},
	) {
		t.Fatalf("order = %v", deps.order)
	}
}

func TestServiceStopEndsActiveCallAndIsTerminallyIdempotent(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	_, err := service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("start call: %v", err)
	}
	deps.store.session.Status = StatusActive
	deps.order = nil

	session, err := service.Stop(context.Background(), StopInput{
		WorkspaceID: deps.store.session.WorkspaceID,
		UserID:      deps.store.session.UserID,
		CallID:      deps.store.session.ID,
		Reason:      "user_hangup",
	})
	if err != nil {
		t.Fatalf("stop call: %v", err)
	}
	if session.Status != StatusEnded || session.EndReason != "user_hangup" {
		t.Fatalf("session = %+v", session)
	}
	wantOrder := []string{"get", "authorize", "ending", "provider_stop", "ended"}
	if !reflect.DeepEqual(deps.order, wantOrder) {
		t.Fatalf("order = %v, want %v", deps.order, wantOrder)
	}

	deps.order = nil
	session, err = service.Stop(context.Background(), StopInput{
		WorkspaceID: deps.store.session.WorkspaceID,
		UserID:      deps.store.session.UserID,
		CallID:      deps.store.session.ID,
		Reason:      "duplicate_hangup",
	})
	if err != nil {
		t.Fatalf("repeat stop: %v", err)
	}
	if session.Status != StatusEnded || deps.provider.stopCalls != 1 {
		t.Fatalf("repeat session=%+v stop calls=%d", session, deps.provider.stopCalls)
	}
	if !reflect.DeepEqual(deps.order, []string{"get", "authorize", "ending"}) {
		t.Fatalf("repeat order = %v", deps.order)
	}
}

func TestServiceStopRetriesProviderWhileSessionIsEnding(t *testing.T) {
	deps := newTestDependencies()
	deps.store.session = Session{
		ID:             "call-1",
		WorkspaceID:    "workspace-1",
		ChannelID:      "dm-1",
		AgentID:        "beckham-1",
		UserID:         "member-1",
		Provider:       "volcengine",
		ProviderTaskID: "voice-task-nonce-1",
		RoomID:         "voice-call-nonce-1",
		Status:         StatusEnding,
		EndReason:      "user_hangup",
	}
	service := newTestService(t, deps)

	session, err := service.Stop(context.Background(), StopInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
		Reason:      "user_hangup",
	})
	if err != nil {
		t.Fatalf("retry stop: %v", err)
	}
	if session.Status != StatusEnded || deps.provider.stopCalls != 1 {
		t.Fatalf("session=%+v stop calls=%d", session, deps.provider.stopCalls)
	}
}

func TestServiceStopLeavesEndingForRetryWhenProviderFails(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	_, err := service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("start call: %v", err)
	}
	deps.store.session.Status = StatusActive
	deps.provider.stopErr = errors.New("provider stop failed")

	_, err = service.Stop(context.Background(), StopInput{
		WorkspaceID: deps.store.session.WorkspaceID,
		UserID:      deps.store.session.UserID,
		CallID:      deps.store.session.ID,
		Reason:      "user_hangup",
	})
	if err == nil || !strings.Contains(err.Error(), "provider stop failed") {
		t.Fatalf("error = %v", err)
	}
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("error = %v, want ErrProviderFailure", err)
	}
	if deps.store.session.Status != StatusEnding || deps.store.markEndedCalls != 0 {
		t.Fatalf("session=%+v ended calls=%d", deps.store.session, deps.store.markEndedCalls)
	}
}

func TestServiceStopUsesCleanupContextAfterRequestCancellation(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	_, err := service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("start call: %v", err)
	}
	deps.store.session.Status = StatusActive
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = service.Stop(requestContext, StopInput{
		WorkspaceID: deps.store.session.WorkspaceID,
		UserID:      deps.store.session.UserID,
		CallID:      deps.store.session.ID,
		Reason:      "browser_closed",
	})
	if err != nil {
		t.Fatalf("stop canceled request: %v", err)
	}
	if deps.provider.stopContextErr != nil {
		t.Fatalf("provider received canceled context: %v", deps.provider.stopContextErr)
	}
}

type testDependencies struct {
	order      []string
	store      *fakeStore
	authorizer *fakeAuthorizer
	context    *fakeContextBuilder
	provider   *fakeProvider
}

func newTestDependencies() *testDependencies {
	deps := &testDependencies{}
	deps.store = &fakeStore{order: &deps.order}
	deps.authorizer = &fakeAuthorizer{order: &deps.order}
	deps.context = &fakeContextBuilder{
		order: &deps.order,
		result: ConversationContext{
			WelcomeMessage: "你好，我是贝克汉姆。",
			SystemMessages: []string{"You are Beckham.", "Use the current DM context."},
		},
	}
	deps.provider = &fakeProvider{
		order: &deps.order,
		prepareResult: ProviderPrepareResult{
			AppID:     "rtc-app",
			Token:     "room-token",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
	return deps
}

func newTestService(t *testing.T, deps *testDependencies) *Service {
	t.Helper()
	service, err := NewService(ServiceConfig{
		ProviderName:   "volcengine",
		IDGenerator:    func() string { return "nonce-1" },
		CleanupTimeout: time.Second,
	}, deps.store, deps.authorizer, deps.context, deps.provider)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service
}

func validStartInput() StartInput {
	return StartInput{
		WorkspaceID: "workspace-1",
		ChannelID:   "dm-1",
		AgentID:     "beckham-1",
		UserID:      "member-1",
	}
}

type fakeStore struct {
	order             *[]string
	session           Session
	createCalls       int
	markFailedCalls   int
	markEndedCalls    int
	markConnectingErr error
}

func (store *fakeStore) CreateStarting(_ context.Context, input NewSession) (Session, error) {
	*store.order = append(*store.order, "create")
	store.createCalls++
	store.session = Session{
		ID:             "call-1",
		WorkspaceID:    input.WorkspaceID,
		ChannelID:      input.ChannelID,
		AgentID:        input.AgentID,
		UserID:         input.UserID,
		Provider:       input.Provider,
		ProviderTaskID: input.ProviderTaskID,
		RoomID:         input.RoomID,
		Status:         StatusStarting,
	}
	return store.session, nil
}

func (store *fakeStore) Get(_ context.Context, workspaceID, userID, callID string) (Session, error) {
	*store.order = append(*store.order, "get")
	if store.session.ID != callID ||
		store.session.WorkspaceID != workspaceID ||
		store.session.UserID != userID {
		return Session{}, errors.New("not found")
	}
	return store.session, nil
}

func (store *fakeStore) BeginProviderStart(
	_ context.Context,
	workspaceID,
	callID string,
) (BeginProviderStartResult, error) {
	*store.order = append(*store.order, "provider_start_claim")
	if store.markConnectingErr != nil {
		return BeginProviderStartResult{}, store.markConnectingErr
	}
	if store.session.WorkspaceID != workspaceID || store.session.ID != callID {
		return BeginProviderStartResult{}, errors.New("not found")
	}
	required := store.session.Status == StatusStarting
	store.session.Status = StatusConnecting
	return BeginProviderStartResult{
		Session:               store.session,
		ProviderStartRequired: required,
	}, nil
}

func (store *fakeStore) ApplyClientAnswered(
	_ context.Context,
	workspaceID,
	userID,
	callID string,
) (Session, error) {
	*store.order = append(*store.order, "client_answer")
	if store.session.WorkspaceID != workspaceID ||
		store.session.UserID != userID ||
		store.session.ID != callID {
		return Session{}, ErrCallNotFound
	}
	switch store.session.Status {
	case StatusStarting, StatusConnecting, StatusReconnecting, StatusActive:
		store.session.Status = StatusActive
		if store.session.ConnectedAt == nil {
			now := time.Date(2026, time.August, 1, 0, 12, 0, 0, time.UTC)
			store.session.ConnectedAt = &now
		}
		return store.session, nil
	default:
		return Session{}, ErrCallNotFound
	}
}

func (store *fakeStore) MarkFailed(_ context.Context, workspaceID, callID, errorCode string) (Session, error) {
	*store.order = append(*store.order, "failed")
	store.markFailedCalls++
	if store.session.WorkspaceID != workspaceID || store.session.ID != callID {
		return Session{}, errors.New("not found")
	}
	store.session.Status = StatusFailed
	store.session.ErrorCode = errorCode
	return store.session, nil
}

func (store *fakeStore) BeginEnding(_ context.Context, workspaceID, userID, callID, reason string) (BeginEndingResult, error) {
	*store.order = append(*store.order, "ending")
	if store.session.WorkspaceID != workspaceID ||
		store.session.UserID != userID ||
		store.session.ID != callID {
		return BeginEndingResult{}, errors.New("not found")
	}
	switch store.session.Status {
	case StatusEnded, StatusFailed:
		return BeginEndingResult{Session: store.session}, nil
	case StatusEnding:
		return BeginEndingResult{Session: store.session, ProviderStopRequired: true}, nil
	case StatusStarting:
		store.session.Status = StatusEnding
		store.session.EndReason = reason
		return BeginEndingResult{Session: store.session}, nil
	default:
		store.session.Status = StatusEnding
		store.session.EndReason = reason
		return BeginEndingResult{Session: store.session, ProviderStopRequired: true}, nil
	}
}

func (store *fakeStore) MarkEnded(_ context.Context, workspaceID, callID, reason string) (Session, error) {
	*store.order = append(*store.order, "ended")
	store.markEndedCalls++
	if store.session.WorkspaceID != workspaceID || store.session.ID != callID {
		return Session{}, errors.New("not found")
	}
	store.session.Status = StatusEnded
	store.session.EndReason = reason
	return store.session, nil
}

type fakeAuthorizer struct {
	order *[]string
	err   error
}

func (authorizer *fakeAuthorizer) Authorize(_ context.Context, _ Scope) error {
	*authorizer.order = append(*authorizer.order, "authorize")
	return authorizer.err
}

type fakeContextBuilder struct {
	order  *[]string
	result ConversationContext
	err    error
}

func (builder *fakeContextBuilder) Build(_ context.Context, _ Scope) (ConversationContext, error) {
	*builder.order = append(*builder.order, "context")
	return builder.result, builder.err
}

type fakeProvider struct {
	order          *[]string
	prepareInput   ProviderPrepareInput
	prepareResult  ProviderPrepareResult
	prepareErr     error
	connectInput   ProviderConnectInput
	connectErr     error
	stopErr        error
	prepareCalls   int
	connectCalls   int
	stopCalls      int
	stopContextErr error
}

func (provider *fakeProvider) Prepare(
	_ context.Context,
	input ProviderPrepareInput,
) (ProviderPrepareResult, error) {
	*provider.order = append(*provider.order, "provider_prepare")
	provider.prepareCalls++
	provider.prepareInput = input
	return provider.prepareResult, provider.prepareErr
}

func TestServiceActivateDuplexSkipsProviderConnectAndStop(t *testing.T) {
	deps := newTestDependencies()
	service := newTestService(t, deps)
	if _, err := service.Start(context.Background(), validStartInput()); err != nil {
		t.Fatalf("prepare call: %v", err)
	}

	session, err := service.ActivateDuplex(context.Background(), AnswerInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
	})
	if err != nil {
		t.Fatalf("activate duplex: %v", err)
	}
	if session.Status != StatusActive || session.ConnectedAt == nil {
		t.Fatalf("session = %+v, want active with connected_at", session)
	}
	if deps.provider.connectCalls != 0 {
		t.Fatalf("provider connect calls = %d, want 0", deps.provider.connectCalls)
	}

	ended, err := service.EndWithoutProviderStop(context.Background(), StopInput{
		WorkspaceID: "workspace-1",
		UserID:      "member-1",
		CallID:      "call-1",
		Reason:      "duplex_client_stop",
	})
	if err != nil {
		t.Fatalf("end duplex: %v", err)
	}
	if ended.Status != StatusEnded || ended.EndReason != "duplex_client_stop" {
		t.Fatalf("ended = %+v", ended)
	}
	if deps.provider.stopCalls != 0 {
		t.Fatalf("provider stop calls = %d, want 0", deps.provider.stopCalls)
	}
}

func (provider *fakeProvider) Connect(
	_ context.Context,
	input ProviderConnectInput,
) error {
	*provider.order = append(*provider.order, "provider_connect")
	provider.connectCalls++
	provider.connectInput = input
	return provider.connectErr
}

func (provider *fakeProvider) Stop(ctx context.Context, _ ProviderCallIdentity) error {
	*provider.order = append(*provider.order, "provider_stop")
	provider.stopCalls++
	provider.stopContextErr = ctx.Err()
	return provider.stopErr
}

func (provider *fakeProvider) String() string {
	return fmt.Sprintf("connect=%d stop=%d", provider.connectCalls, provider.stopCalls)
}

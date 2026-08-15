package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service/duplexcall"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	testVoiceAPIWorkspaceID = "20000000-0000-4000-8000-000000000001"
	testVoiceAPIChannelID   = "20000000-0000-4000-8000-000000000002"
	testVoiceAPIAgentID     = "20000000-0000-4000-8000-000000000003"
	testVoiceAPIUserID      = "20000000-0000-4000-8000-000000000004"
	testVoiceAPICallID      = "20000000-0000-4000-8000-000000000005"
)

func TestCreateVoiceCallReturnsScopedMediaWithoutProviderIdentity(t *testing.T) {
	startedAt := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	expiresAt := startedAt.Add(time.Hour)
	service := &fakeVoiceCallService{
		startResult: voicecall.StartResult{
			Session: voicecall.Session{
				ID:             testVoiceAPICallID,
				WorkspaceID:    testVoiceAPIWorkspaceID,
				ChannelID:      testVoiceAPIChannelID,
				AgentID:        testVoiceAPIAgentID,
				UserID:         testVoiceAPIUserID,
				Provider:       "volcengine",
				ProviderTaskID: "secret-provider-task",
				RoomID:         "voice-room",
				Status:         voicecall.StatusStarting,
				StartedAt:      startedAt,
				CreatedAt:      startedAt,
				UpdatedAt:      startedAt,
			},
			Media: voicecall.MediaCredentials{
				AppID:     "rtc-app",
				RoomID:    "voice-room",
				UserID:    testVoiceAPIUserID,
				Token:     "short-lived-room-token",
				ExpiresAt: expiresAt,
			},
		},
	}
	bus := events.New()
	var published events.Event
	bus.Subscribe(protocol.EventVoiceCallUpdated, func(event events.Event) {
		published = event
	})
	handler := &Handler{VoiceCallService: service, Bus: bus}
	request := voiceCallAPIRequest(
		http.MethodPost,
		"/api/workspaces/"+testVoiceAPIWorkspaceID+"/voice-calls",
		`{"channel_id":"`+testVoiceAPIChannelID+`","agent_id":"`+testVoiceAPIAgentID+`"}`,
	)
	request = withRouteParams(request, "id", testVoiceAPIWorkspaceID)
	response := httptest.NewRecorder()

	handler.CreateVoiceCall(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.startInput != (voicecall.StartInput{
		WorkspaceID: testVoiceAPIWorkspaceID,
		ChannelID:   testVoiceAPIChannelID,
		AgentID:     testVoiceAPIAgentID,
		UserID:      testVoiceAPIUserID,
	}) {
		t.Fatalf("start input = %+v", service.startInput)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	encoded := response.Body.String()
	for _, forbidden := range []string{"secret-provider-task", `"provider"`} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("response exposes %q: %s", forbidden, encoded)
		}
	}
	media, ok := body["media"].(map[string]any)
	if !ok ||
		media["token"] != "short-lived-room-token" ||
		media["room_id"] != "voice-room" ||
		media["user_id"] != testVoiceAPIUserID {
		t.Fatalf("media = %#v", body["media"])
	}
	assertVoiceCallUpdatedEvent(
		t,
		published,
		testVoiceAPIWorkspaceID,
		testVoiceAPICallID,
		testVoiceAPIUserID,
	)
}

func TestVoiceCallHandlersUseAuthenticatedScopeAndServerStopReason(t *testing.T) {
	session := voicecall.Session{
		ID:          testVoiceAPICallID,
		WorkspaceID: testVoiceAPIWorkspaceID,
		ChannelID:   testVoiceAPIChannelID,
		AgentID:     testVoiceAPIAgentID,
		UserID:      testVoiceAPIUserID,
		Status:      voicecall.StatusActive,
		StartedAt:   time.Now(),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	service := &fakeVoiceCallService{session: session}
	bus := events.New()
	var published events.Event
	bus.Subscribe(protocol.EventVoiceCallUpdated, func(event events.Event) {
		published = event
	})
	handler := &Handler{VoiceCallService: service, Bus: bus}

	connectRequest := voiceCallAPIRequest(
		http.MethodPost,
		"/api/workspaces/"+testVoiceAPIWorkspaceID+"/voice-calls/"+testVoiceAPICallID+"/connect",
		"",
	)
	connectRequest = withRouteParams(
		connectRequest,
		"id",
		testVoiceAPIWorkspaceID,
		"callId",
		testVoiceAPICallID,
	)
	connectResponse := httptest.NewRecorder()
	handler.ConnectVoiceCall(connectResponse, connectRequest)
	if connectResponse.Code != http.StatusOK {
		t.Fatalf("connect status = %d, body = %s", connectResponse.Code, connectResponse.Body.String())
	}
	if service.connectInput != (voicecall.ConnectInput{
		WorkspaceID: testVoiceAPIWorkspaceID,
		UserID:      testVoiceAPIUserID,
		CallID:      testVoiceAPICallID,
	}) {
		t.Fatalf("connect input = %+v", service.connectInput)
	}
	assertVoiceCallUpdatedEvent(
		t,
		published,
		testVoiceAPIWorkspaceID,
		testVoiceAPICallID,
		testVoiceAPIUserID,
	)

	connectedAt := time.Date(2026, time.August, 1, 0, 12, 0, 0, time.UTC)
	service.session.Status = voicecall.StatusActive
	service.session.ConnectedAt = &connectedAt
	published = events.Event{}
	answerRequest := voiceCallAPIRequest(
		http.MethodPost,
		"/api/workspaces/"+testVoiceAPIWorkspaceID+"/voice-calls/"+testVoiceAPICallID+"/answer",
		"",
	)
	answerRequest = withRouteParams(
		answerRequest,
		"id",
		testVoiceAPIWorkspaceID,
		"callId",
		testVoiceAPICallID,
	)
	answerResponse := httptest.NewRecorder()
	handler.AnswerVoiceCall(answerResponse, answerRequest)
	if answerResponse.Code != http.StatusOK {
		t.Fatalf("answer status = %d, body = %s", answerResponse.Code, answerResponse.Body.String())
	}
	if service.answerInput != (voicecall.AnswerInput{
		WorkspaceID: testVoiceAPIWorkspaceID,
		UserID:      testVoiceAPIUserID,
		CallID:      testVoiceAPICallID,
	}) {
		t.Fatalf("answer input = %+v", service.answerInput)
	}
	assertVoiceCallUpdatedEvent(
		t,
		published,
		testVoiceAPIWorkspaceID,
		testVoiceAPICallID,
		testVoiceAPIUserID,
	)

	getRequest := voiceCallAPIRequest(
		http.MethodGet,
		"/api/workspaces/"+testVoiceAPIWorkspaceID+"/voice-calls/"+testVoiceAPICallID,
		"",
	)
	getRequest = withRouteParams(
		getRequest,
		"id",
		testVoiceAPIWorkspaceID,
		"callId",
		testVoiceAPICallID,
	)
	getResponse := httptest.NewRecorder()
	handler.GetVoiceCall(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	if service.getWorkspaceID != testVoiceAPIWorkspaceID ||
		service.getUserID != testVoiceAPIUserID ||
		service.getCallID != testVoiceAPICallID {
		t.Fatalf(
			"get scope = %q/%q/%q",
			service.getWorkspaceID,
			service.getUserID,
			service.getCallID,
		)
	}

	stopRequest := voiceCallAPIRequest(
		http.MethodPost,
		"/api/workspaces/"+testVoiceAPIWorkspaceID+"/voice-calls/"+testVoiceAPICallID+"/stop",
		"",
	)
	stopRequest = withRouteParams(
		stopRequest,
		"id",
		testVoiceAPIWorkspaceID,
		"callId",
		testVoiceAPICallID,
	)
	stopResponse := httptest.NewRecorder()
	handler.StopVoiceCall(stopResponse, stopRequest)
	if stopResponse.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body = %s", stopResponse.Code, stopResponse.Body.String())
	}
	if service.stopInput != (voicecall.StopInput{
		WorkspaceID: testVoiceAPIWorkspaceID,
		UserID:      testVoiceAPIUserID,
		CallID:      testVoiceAPICallID,
		Reason:      "user_hangup",
	}) {
		t.Fatalf("stop input = %+v", service.stopInput)
	}
	assertVoiceCallUpdatedEvent(
		t,
		published,
		testVoiceAPIWorkspaceID,
		testVoiceAPICallID,
		testVoiceAPIUserID,
	)
}

func TestCreateVoiceCallRejectsAmbiguousOrUntrustedInput(t *testing.T) {
	service := &fakeVoiceCallService{}
	handler := &Handler{VoiceCallService: service}
	for _, testCase := range []struct {
		name string
		body string
	}{
		{
			name: "unknown field",
			body: `{"channel_id":"` + testVoiceAPIChannelID + `","agent_id":"` + testVoiceAPIAgentID + `","user_id":"` + testVoiceAPIUserID + `"}`,
		},
		{
			name: "multiple objects",
			body: `{"channel_id":"` + testVoiceAPIChannelID + `","agent_id":"` + testVoiceAPIAgentID + `"}{}`,
		},
		{
			name: "invalid channel",
			body: `{"channel_id":"not-a-uuid","agent_id":"` + testVoiceAPIAgentID + `"}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := voiceCallAPIRequest(http.MethodPost, "/voice-calls", testCase.body)
			request = withRouteParams(request, "id", testVoiceAPIWorkspaceID)
			response := httptest.NewRecorder()
			handler.CreateVoiceCall(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
	if service.startCalls != 0 {
		t.Fatalf("invalid input reached service %d times", service.startCalls)
	}
}

func TestVoiceCallHandlersMapStableServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", voicecall.ErrCallNotFound, http.StatusNotFound, "voice_call_not_found"},
		{"scope not found", voicecall.ErrScopeNotFound, http.StatusNotFound, "voice_call_not_found"},
		{"forbidden", voicecall.ErrScopeForbidden, http.StatusForbidden, "voice_call_forbidden"},
		{"unavailable", voicecall.ErrScopeUnavailable, http.StatusConflict, "voice_call_unavailable"},
		{"active", voicecall.ErrCallAlreadyActive, http.StatusConflict, "voice_call_already_active"},
		{"provider", voicecall.ErrProviderFailure, http.StatusBadGateway, "voice_call_provider_failed"},
		{"internal", errors.New("database detail"), http.StatusInternalServerError, "voice_call_failed"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler := &Handler{VoiceCallService: &fakeVoiceCallService{startErr: testCase.err}}
			request := voiceCallAPIRequest(
				http.MethodPost,
				"/voice-calls",
				`{"channel_id":"`+testVoiceAPIChannelID+`","agent_id":"`+testVoiceAPIAgentID+`"}`,
			)
			request = withRouteParams(request, "id", testVoiceAPIWorkspaceID)
			response := httptest.NewRecorder()
			handler.CreateVoiceCall(response, request)
			if response.Code != testCase.wantStatus ||
				!strings.Contains(response.Body.String(), testCase.wantCode) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "database detail") {
				t.Fatalf("internal detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestVoiceCallHandlersExposeConfigurationAndAuthenticationFailures(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		handler := &Handler{}
		request := voiceCallAPIRequest(
			http.MethodPost,
			"/voice-calls",
			`{"channel_id":"`+testVoiceAPIChannelID+`","agent_id":"`+testVoiceAPIAgentID+`"}`,
		)
		request = withRouteParams(request, "id", testVoiceAPIWorkspaceID)
		response := httptest.NewRecorder()
		handler.CreateVoiceCall(response, request)
		if response.Code != http.StatusServiceUnavailable ||
			!strings.Contains(response.Body.String(), "voice_call_not_configured") {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("missing authenticated member", func(t *testing.T) {
		handler := &Handler{VoiceCallService: &fakeVoiceCallService{}}
		request := httptest.NewRequest(
			http.MethodGet,
			"/voice-calls/"+testVoiceAPICallID,
			nil,
		)
		request = withRouteParams(
			request,
			"id",
			testVoiceAPIWorkspaceID,
			"callId",
			testVoiceAPICallID,
		)
		response := httptest.NewRecorder()
		handler.GetVoiceCall(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})
}

func voiceCallAPIRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", testVoiceAPIUserID)
	return request
}

func assertVoiceCallUpdatedEvent(
	t *testing.T,
	event events.Event,
	workspaceID string,
	callID string,
	userID string,
) {
	t.Helper()
	if event.Type != protocol.EventVoiceCallUpdated ||
		event.WorkspaceID != workspaceID ||
		event.ActorType != "system" {
		t.Fatalf("event routing = %+v", event)
	}
	if len(event.RecipientUserIDs) != 1 || event.RecipientUserIDs[0] != userID {
		t.Fatalf("event recipients = %#v, want [%s]", event.RecipientUserIDs, userID)
	}
	payload, ok := event.Payload.(protocol.VoiceCallUpdatedPayload)
	if !ok {
		t.Fatalf("event payload type = %T", event.Payload)
	}
	if payload.WorkspaceID != workspaceID || payload.CallID != callID {
		t.Fatalf("event payload = %+v", payload)
	}
}

type fakeVoiceCallService struct {
	startResult    voicecall.StartResult
	session        voicecall.Session
	startErr       error
	getErr         error
	stopErr        error
	answerErr      error
	startInput     voicecall.StartInput
	connectInput   voicecall.ConnectInput
	answerInput    voicecall.AnswerInput
	stopInput      voicecall.StopInput
	startCalls     int
	answerCalls    int
	getWorkspaceID string
	getUserID      string
	getCallID      string
}

func (service *fakeVoiceCallService) Connect(
	_ context.Context,
	input voicecall.ConnectInput,
) (voicecall.Session, error) {
	service.connectInput = input
	return service.session, nil
}

func (service *fakeVoiceCallService) Answer(
	_ context.Context,
	input voicecall.AnswerInput,
) (voicecall.Session, error) {
	service.answerCalls++
	service.answerInput = input
	if service.answerErr != nil {
		return voicecall.Session{}, service.answerErr
	}
	return service.session, nil
}

func (service *fakeVoiceCallService) Start(
	_ context.Context,
	input voicecall.StartInput,
) (voicecall.StartResult, error) {
	service.startCalls++
	service.startInput = input
	return service.startResult, service.startErr
}

func (service *fakeVoiceCallService) Get(
	_ context.Context,
	workspaceID string,
	userID string,
	callID string,
) (voicecall.Session, error) {
	service.getWorkspaceID = workspaceID
	service.getUserID = userID
	service.getCallID = callID
	return service.session, service.getErr
}

func (service *fakeVoiceCallService) Stop(
	_ context.Context,
	input voicecall.StopInput,
) (voicecall.Session, error) {
	service.stopInput = input
	return service.session, service.stopErr
}

// fakeDuplexVoiceCallService adds EndWithoutProviderStop for StopVoiceCall fallback.
type fakeDuplexVoiceCallService struct {
	fakeVoiceCallService
	endWithoutProviderStopCalls int
	endWithoutProviderStopErr   error
	endedSession                voicecall.Session
}

func (service *fakeDuplexVoiceCallService) ActivateDuplex(
	_ context.Context,
	_ voicecall.AnswerInput,
) (voicecall.DuplexActivation, error) {
	return voicecall.DuplexActivation{}, errors.New("not used in stop fallback test")
}

func (service *fakeDuplexVoiceCallService) EndWithoutProviderStop(
	_ context.Context,
	input voicecall.StopInput,
) (voicecall.Session, error) {
	service.endWithoutProviderStopCalls++
	service.stopInput = input
	if service.endWithoutProviderStopErr != nil {
		return voicecall.Session{}, service.endWithoutProviderStopErr
	}
	if service.endedSession.ID != "" {
		return service.endedSession, nil
	}
	session := service.session
	session.Status = voicecall.StatusEnded
	session.EndReason = input.Reason
	return session, nil
}

type fakeDuplexGateway struct {
	configured bool
	hasCallID  string
}

func (g *fakeDuplexGateway) Configured() bool { return g != nil && g.configured }
func (g *fakeDuplexGateway) Has(callID string) bool {
	return g != nil && g.hasCallID != "" && g.hasCallID == callID
}
func (g *fakeDuplexGateway) MarkPending(string, string, string) {}
func (g *fakeDuplexGateway) Close(string)                       {}
func (g *fakeDuplexGateway) Start(
	context.Context,
	string,
	duplexcall.MulticaExecutor,
	duplexcall.Emitter,
) (*duplexcall.Session, error) {
	return nil, errors.New("not used")
}

func TestStopVoiceCallFallsBackToDuplexEndWhenProviderStopFailsAfterRestart(t *testing.T) {
	endedAt := time.Date(2026, time.August, 3, 13, 40, 0, 0, time.UTC)
	service := &fakeDuplexVoiceCallService{
		fakeVoiceCallService: fakeVoiceCallService{
			stopErr: errors.New("stop Volcengine voice task: task not found"),
			session: voicecall.Session{
				ID:          testVoiceAPICallID,
				WorkspaceID: testVoiceAPIWorkspaceID,
				UserID:      testVoiceAPIUserID,
				Status:      voicecall.StatusEnding,
			},
		},
		endedSession: voicecall.Session{
			ID:          testVoiceAPICallID,
			WorkspaceID: testVoiceAPIWorkspaceID,
			UserID:      testVoiceAPIUserID,
			Status:      voicecall.StatusEnded,
			EndedAt:     &endedAt,
			EndReason:   "user_hangup",
		},
	}
	handler := &Handler{
		VoiceCallService: service,
		DuplexGateway:    &fakeDuplexGateway{configured: true}, // Has empty after restart
		Bus:              events.New(),
	}
	request := voiceCallAPIRequest(
		http.MethodPost,
		"/api/workspaces/"+testVoiceAPIWorkspaceID+"/voice-calls/"+testVoiceAPICallID+"/stop",
		"",
	)
	request = withRouteParams(
		request,
		"id",
		testVoiceAPIWorkspaceID,
		"callId",
		testVoiceAPICallID,
	)
	response := httptest.NewRecorder()

	handler.StopVoiceCall(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.endWithoutProviderStopCalls != 1 {
		t.Fatalf("EndWithoutProviderStop calls = %d, want 1", service.endWithoutProviderStopCalls)
	}
}

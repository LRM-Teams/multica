package voicecall

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
)

func TestVolcengineProviderStartsWithTokenBeforeProviderTask(t *testing.T) {
	var order []string
	client := &fakeVolcengineClient{order: &order}
	signer := &fakeVolcengineTokenSigner{
		order: &order,
		appID: "123456781234567812345678",
		token: volcenginertc.SignedRoomToken{
			Value:     "room-token",
			ExpiresAt: time.Date(2026, time.July, 23, 14, 0, 0, 0, time.UTC),
		},
	}
	provider := newTestVolcengineProvider(t, client, signer)

	result, err := provider.Start(context.Background(), validProviderStartInput())
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"token", "start"}) {
		t.Fatalf("order = %v, want token before start", order)
	}
	if result.AppID != signer.appID ||
		result.Token != signer.token.Value ||
		!result.ExpiresAt.Equal(signer.token.ExpiresAt) {
		t.Fatalf("result = %+v", result)
	}
	if signer.roomID != "voice-call-1" || signer.userID != "member-1" {
		t.Fatalf("token identity = room %q user %q", signer.roomID, signer.userID)
	}
	request := client.startRequest
	if request.AppID != signer.appID ||
		request.RoomID != "voice-call-1" ||
		request.TaskID != "voice-task-1" {
		t.Fatalf("start request identity = %+v", request)
	}
	if !strings.Contains(string(request.Config), `"ApiResourceId":"volc.seedasr.sauc.duration"`) ||
		!strings.Contains(string(request.Config), `"ResourceId":"seed-tts-2.0"`) ||
		!strings.Contains(string(request.Config), `"SystemMessages":["You are Beckham."]`) ||
		!strings.Contains(string(request.Config), `"Mode":"CustomLLM"`) ||
		!strings.Contains(string(request.Config), `"Custom":"{\"voice_call_id\":\"call-1\"}"`) {
		t.Fatalf("Config = %s", request.Config)
	}
	if string(request.AgentConfig) != `{"TargetUserId":["member-1"],"UserId":"voice-agent-1","WelcomeMessage":"你好，我是贝克汉姆。","EnableConversationStateCallback":true,"ServerMessageURLForRTS":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignatureForRTS":"callback-secret"}` {
		t.Fatalf("AgentConfig = %s", request.AgentConfig)
	}
}

func TestVolcengineProviderDoesNotStartWhenConfigurationOrTokenFails(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ProviderStartInput, *fakeVolcengineTokenSigner)
		want      string
		tokenCall int
	}{
		{
			name: "configuration",
			configure: func(input *ProviderStartInput, _ *fakeVolcengineTokenSigner) {
				input.SystemMessages = nil
			},
			want: "SystemMessages",
		},
		{
			name: "token",
			configure: func(_ *ProviderStartInput, signer *fakeVolcengineTokenSigner) {
				signer.err = errors.New("entropy unavailable")
			},
			want:      "sign room token",
			tokenCall: 1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var order []string
			client := &fakeVolcengineClient{order: &order}
			signer := &fakeVolcengineTokenSigner{
				order: &order,
				appID: "123456781234567812345678",
			}
			provider := newTestVolcengineProvider(t, client, signer)
			input := validProviderStartInput()
			testCase.configure(&input, signer)

			_, err := provider.Start(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			if signer.calls != testCase.tokenCall || client.startCalls != 0 {
				t.Fatalf("token calls=%d start calls=%d", signer.calls, client.startCalls)
			}
		})
	}
}

func TestVolcengineProviderTreatsStructuredStartRejectionAsDefinitive(t *testing.T) {
	var order []string
	client := &fakeVolcengineClient{
		order: &order,
		startErr: &volcenginertc.ProviderError{
			Action: "StartVoiceChat",
			Code:   "InvalidParameter",
		},
	}
	signer := validFakeVolcengineTokenSigner(&order)
	provider := newTestVolcengineProvider(t, client, signer)

	_, err := provider.Start(context.Background(), validProviderStartInput())
	var providerError *volcenginertc.ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("error = %v, want ProviderError", err)
	}
	if client.stopCalls != 0 {
		t.Fatalf("structured rejection triggered %d compensating stops", client.stopCalls)
	}
}

func TestVolcengineProviderCompensatesAmbiguousStartFailure(t *testing.T) {
	var order []string
	startErr := errors.New("connection reset after request write")
	client := &fakeVolcengineClient{order: &order, startErr: startErr}
	signer := validFakeVolcengineTokenSigner(&order)
	provider := newTestVolcengineProvider(t, client, signer)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Start(requestContext, validProviderStartInput())
	if !errors.Is(err, startErr) {
		t.Fatalf("error = %v, want original start error", err)
	}
	var uncertain *ProviderStartUncertainError
	if errors.As(err, &uncertain) {
		t.Fatalf("successful compensation returned uncertain error: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"token", "start", "stop"}) {
		t.Fatalf("order = %v", order)
	}
	if client.stopContextErr != nil {
		t.Fatalf("compensating stop received canceled context: %v", client.stopContextErr)
	}
}

func TestVolcengineProviderReportsUncertainStartWhenCompensationFails(t *testing.T) {
	var order []string
	startErr := errors.New("connection reset after request write")
	stopErr := errors.New("stop endpoint unavailable")
	client := &fakeVolcengineClient{
		order:    &order,
		startErr: startErr,
		stopErr:  stopErr,
	}
	signer := validFakeVolcengineTokenSigner(&order)
	provider := newTestVolcengineProvider(t, client, signer)

	_, err := provider.Start(context.Background(), validProviderStartInput())
	var uncertain *ProviderStartUncertainError
	if !errors.As(err, &uncertain) ||
		!errors.Is(err, startErr) ||
		!errors.Is(err, stopErr) {
		t.Fatalf("error = %v, want uncertain joined start/stop error", err)
	}
}

func TestVolcengineProviderStopUsesExactIdentity(t *testing.T) {
	var order []string
	client := &fakeVolcengineClient{order: &order}
	provider := newTestVolcengineProvider(
		t,
		client,
		validFakeVolcengineTokenSigner(&order),
	)

	err := provider.Stop(context.Background(), ProviderCallIdentity{
		RoomID: "voice-call-1",
		TaskID: "voice-task-1",
	})
	if err != nil {
		t.Fatalf("stop provider: %v", err)
	}
	if client.stopRequest.AppID != "123456781234567812345678" ||
		client.stopRequest.RoomID != "voice-call-1" ||
		client.stopRequest.TaskID != "voice-task-1" {
		t.Fatalf("stop request = %+v", client.stopRequest)
	}
}

func newTestVolcengineProvider(
	t *testing.T,
	client VolcengineVoiceClient,
	signer VolcengineTokenSigner,
) *VolcengineProvider {
	t.Helper()
	provider, err := NewVolcengineProvider(VolcengineProviderConfig{
		CustomLLMURL:        "https://multica.example.com/api/voice-calls/llm",
		CustomLLMAPIKey:     "llm-secret",
		TTSVoiceID:          "zh_male_m191_uranus_bigtts",
		CallbackURL:         "https://multica.example.com/api/voice-calls/callback",
		CallbackSignature:   "callback-secret",
		CompensationTimeout: time.Second,
	}, client, signer)
	if err != nil {
		t.Fatalf("new Volcengine provider: %v", err)
	}
	return provider
}

func validProviderStartInput() ProviderStartInput {
	return ProviderStartInput{
		CallID:         "call-1",
		RoomID:         "voice-call-1",
		TaskID:         "voice-task-1",
		TargetUserID:   "member-1",
		AgentUserID:    "voice-agent-1",
		WelcomeMessage: "你好，我是贝克汉姆。",
		SystemMessages: []string{"You are Beckham."},
	}
}

func validFakeVolcengineTokenSigner(order *[]string) *fakeVolcengineTokenSigner {
	return &fakeVolcengineTokenSigner{
		order: order,
		appID: "123456781234567812345678",
		token: volcenginertc.SignedRoomToken{
			Value:     "room-token",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
}

type fakeVolcengineClient struct {
	order          *[]string
	startRequest   volcenginertc.StartVoiceChatRequest
	stopRequest    volcenginertc.StopVoiceChatRequest
	startErr       error
	stopErr        error
	stopContextErr error
	startCalls     int
	stopCalls      int
}

func (client *fakeVolcengineClient) StartVoiceChat(
	_ context.Context,
	request volcenginertc.StartVoiceChatRequest,
) (volcenginertc.Response, error) {
	*client.order = append(*client.order, "start")
	client.startCalls++
	client.startRequest = request
	return volcenginertc.Response{RequestID: "request-start"}, client.startErr
}

func (client *fakeVolcengineClient) StopVoiceChat(
	ctx context.Context,
	request volcenginertc.StopVoiceChatRequest,
) (volcenginertc.Response, error) {
	*client.order = append(*client.order, "stop")
	client.stopCalls++
	client.stopContextErr = ctx.Err()
	client.stopRequest = request
	return volcenginertc.Response{RequestID: "request-stop"}, client.stopErr
}

type fakeVolcengineTokenSigner struct {
	order  *[]string
	appID  string
	token  volcenginertc.SignedRoomToken
	err    error
	roomID string
	userID string
	calls  int
}

func (signer *fakeVolcengineTokenSigner) AppID() string {
	return signer.appID
}

func (signer *fakeVolcengineTokenSigner) Sign(
	roomID string,
	userID string,
) (volcenginertc.SignedRoomToken, error) {
	*signer.order = append(*signer.order, "token")
	signer.calls++
	signer.roomID = roomID
	signer.userID = userID
	return signer.token, signer.err
}

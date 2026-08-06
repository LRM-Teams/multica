package volcenginertc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestStartVoiceChatUsesDefaultProtocolAndExactSignature(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		captured = request.Clone(request.Context())
		var err error
		capturedBody, err = io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		return jsonResponse(http.StatusOK, `{
			"ResponseMetadata": {
				"RequestId": "request-start",
				"Action": "StartVoiceChat",
				"Version": "2025-06-01",
				"Service": "rtc",
				"Region": "cn-north-1"
			}
		}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.now = func() time.Time {
		return time.Date(2026, time.July, 23, 8, 9, 10, 0, time.UTC)
	}

	response, err := client.StartVoiceChat(context.Background(), StartVoiceChatRequest{
		AppID:      "app-1",
		RoomID:     "room-1",
		TaskID:     "task-1",
		BusinessID: "biz-1",
		Config:     json.RawMessage(`{"InterruptMode":0}`),
		AgentConfig: json.RawMessage(
			`{"TargetUserId":["member-1"],"UserId":"beckham"}`,
		),
	})
	if err != nil {
		t.Fatalf("start voice chat: %v", err)
	}
	if response.RequestID != "request-start" {
		t.Fatalf("request id = %q, want request-start", response.RequestID)
	}
	if captured == nil {
		t.Fatal("request was not sent")
	}
	if captured.Method != http.MethodPost || captured.URL.Scheme != "https" ||
		captured.URL.Host != "rtc.volcengineapi.com" || captured.URL.Path != "/" {
		t.Fatalf("request target = %s %s", captured.Method, captured.URL.String())
	}
	if got := captured.URL.Query().Get("Action"); got != "StartVoiceChat" {
		t.Fatalf("Action = %q", got)
	}
	if got := captured.URL.Query().Get("Version"); got != DefaultAPIVersion {
		t.Fatalf("Version = %q, want %s", got, DefaultAPIVersion)
	}
	if got := captured.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := captured.Header.Get("X-Date"); got != "20260723T080910Z" {
		t.Fatalf("X-Date = %q", got)
	}
	if got := captured.Header.Get("X-Content-Sha256"); got != "6a252d8a1be5d036a16a5ecda678a13583715b0b0e42fd489566b699931ce620" {
		t.Fatalf("X-Content-Sha256 = %q", got)
	}
	const expectedAuthorization = "HMAC-SHA256 Credential=AKIDEXAMPLE/20260723/cn-north-1/rtc/request, SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=2b0706be45e5d59b4b70ae32e5cb4c8eea83d958dda701cc10b471619f91863d"
	if got := captured.Header.Get("Authorization"); got != expectedAuthorization {
		t.Fatalf("Authorization = %q, want %q", got, expectedAuthorization)
	}
	const expectedBody = `{"AppId":"app-1","RoomId":"room-1","TaskId":"task-1","BusinessId":"biz-1","Config":{"InterruptMode":0},"AgentConfig":{"TargetUserId":["member-1"],"UserId":"beckham"}}`
	if string(capturedBody) != expectedBody {
		t.Fatalf("body = %s, want %s", capturedBody, expectedBody)
	}
}

func TestClientUsesConfiguredCompatibleAPIVersion(t *testing.T) {
	var capturedVersion string
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		capturedVersion = request.URL.Query().Get("Version")
		return jsonResponse(http.StatusOK, `{"ResponseMetadata":{"RequestId":"request-old-app"}}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		APIVersion:      LegacyAPIVersion,
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.StopVoiceChat(context.Background(), StopVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "task-1",
	})
	if err != nil {
		t.Fatalf("stop voice chat: %v", err)
	}
	if capturedVersion != LegacyAPIVersion {
		t.Fatalf("Version = %q, want %s", capturedVersion, LegacyAPIVersion)
	}
}

func TestClientRejectsUnsupportedAPIVersion(t *testing.T) {
	_, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		APIVersion:      "2024-06-01",
	})
	if err == nil || !strings.Contains(err.Error(), "API version") {
		t.Fatalf("unsupported API version error = %v", err)
	}
}

func TestUpdateAndStopVoiceChatUseExactActions(t *testing.T) {
	var actions []string
	var bodies []string
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		actions = append(actions, request.URL.Query().Get("Action"))
		bodies = append(bodies, string(body))
		return jsonResponse(http.StatusOK, `{"ResponseMetadata":{"RequestId":"request-ok"}}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := client.UpdateVoiceChat(context.Background(), UpdateVoiceChatRequest{
		AppID:   "app-1",
		RoomID:  "room-1",
		TaskID:  "task-1",
		Command: UpdateCommandInterrupt,
	}); err != nil {
		t.Fatalf("update voice chat: %v", err)
	}
	if _, err := client.StopVoiceChat(context.Background(), StopVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "task-1",
	}); err != nil {
		t.Fatalf("stop voice chat: %v", err)
	}

	if got := strings.Join(actions, ","); got != "UpdateVoiceChat,StopVoiceChat" {
		t.Fatalf("actions = %q", got)
	}
	if got := strings.Join(bodies, "\n"); got !=
		"{\"AppId\":\"app-1\",\"RoomId\":\"room-1\",\"TaskId\":\"task-1\",\"Command\":\"interrupt\"}\n"+
			"{\"AppId\":\"app-1\",\"RoomId\":\"room-1\",\"TaskId\":\"task-1\"}" {
		t.Fatalf("bodies = %s", got)
	}
}

func TestUpdateVoiceChatReturnsFunctionResultToExactToolCall(t *testing.T) {
	var body string
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		body = string(payload)
		return jsonResponse(http.StatusOK, `{"ResponseMetadata":{"RequestId":"request-ok"}}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.UpdateVoiceChat(context.Background(), UpdateVoiceChatRequest{
		AppID:   "app-1",
		RoomID:  "room-1",
		TaskID:  "task-1",
		Command: UpdateCommandFunction,
		Message: `{"ToolCallID":"call-1","Content":"已创建 issue MUL-42。"}`,
	})
	if err != nil {
		t.Fatalf("return function result: %v", err)
	}
	const want = `{"AppId":"app-1","RoomId":"room-1","TaskId":"task-1","Command":"function","Message":"{\"ToolCallID\":\"call-1\",\"Content\":\"已创建 issue MUL-42。\"}"}`
	if body != want {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func TestUpdateVoiceChatSendsImmediateChineseProgressSpeech(t *testing.T) {
	var body string
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		body = string(payload)
		return jsonResponse(http.StatusOK, `{"ResponseMetadata":{"RequestId":"request-ok"}}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.UpdateVoiceChat(context.Background(), UpdateVoiceChatRequest{
		AppID:         "app-1",
		RoomID:        "room-1",
		TaskID:        "task-1",
		Command:       UpdateCommandExternalTextToSpeech,
		Message:       "我已经开始处理，请稍等。",
		InterruptMode: 2,
	})
	if err != nil {
		t.Fatalf("send progress speech: %v", err)
	}
	const want = `{"AppId":"app-1","RoomId":"room-1","TaskId":"task-1","Command":"ExternalTextToSpeech","Message":"我已经开始处理，请稍等。","InterruptMode":2}`
	if body != want {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func TestVoiceChatProviderErrorPreservesRequestMetadata(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{
			"ResponseMetadata": {
				"RequestId": "request-failed",
				"Error": {
					"Code": "InvalidParameter",
					"Message": "TaskId is invalid"
				}
			}
		}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.StopVoiceChat(context.Background(), StopVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "bad-task",
	})
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("error = %v, want ProviderError", err)
	}
	if providerError.Action != "StopVoiceChat" ||
		providerError.RequestID != "request-failed" ||
		providerError.Code != "InvalidParameter" ||
		providerError.Message != "TaskId is invalid" {
		t.Fatalf("provider error = %+v", providerError)
	}
}

func TestVoiceChatProviderErrorParsesCurrentTopLevelErrorShape(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{
			"code": "AccessDenied.NoPermission",
			"message": "User is not authorized to perform on resource."
		}`), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.StopVoiceChat(context.Background(), StopVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "task-1",
	})
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("error = %v, want ProviderError", err)
	}
	if providerError.StatusCode != http.StatusForbidden ||
		providerError.Code != "AccessDenied.NoPermission" ||
		providerError.Message != "User is not authorized to perform on resource." {
		t.Fatalf("provider error = %+v", providerError)
	}
}

func TestVoiceChatResponseBodyIsBounded(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, strings.Repeat("x", maxResponseBytes+1)), nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.StopVoiceChat(context.Background(), StopVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "task-1",
	})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v, want bounded response failure", err)
	}
}

func TestVoiceChatRequestBodyIsBounded(t *testing.T) {
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("oversized request reached the network")
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.call(context.Background(), "StartVoiceChat", map[string]string{
		"oversized": strings.Repeat("x", maxRequestBytes),
	})
	if err == nil || !strings.Contains(err.Error(), "request exceeds") {
		t.Fatalf("error = %v, want bounded request failure", err)
	}
}

func TestVoiceChatDoesNotForwardSignedRequestsAcrossRedirects(t *testing.T) {
	calls := 0
	httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		response := jsonResponse(http.StatusTemporaryRedirect, `{"ResponseMetadata":{"RequestId":"redirect"}}`)
		response.Header.Set("Location", "https://redirected.example.com/")
		return response, nil
	})}
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.StopVoiceChat(context.Background(), StopVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "task-1",
	})
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("error = %v, want 307 ProviderError", err)
	}
	if calls != 1 {
		t.Fatalf("transport calls = %d, want 1", calls)
	}
}

func TestStartVoiceChatRejectsInvalidOneToOneAgentConfig(t *testing.T) {
	client, err := New(Config{
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid request reached the network")
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.StartVoiceChat(context.Background(), StartVoiceChatRequest{
		AppID:  "app-1",
		RoomID: "room-1",
		TaskID: "task-1",
		Config: json.RawMessage(`{}`),
		AgentConfig: json.RawMessage(
			`{"TargetUserId":["member-1","member-2"],"UserId":"beckham"}`,
		),
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one TargetUserId") {
		t.Fatalf("error = %v, want one-to-one validation", err)
	}

	_, err = client.UpdateVoiceChat(context.Background(), UpdateVoiceChatRequest{
		AppID:   "app-1",
		RoomID:  "room-1",
		TaskID:  "task-1",
		Command: UpdateCommand("invented"),
	})
	if err == nil || !strings.Contains(err.Error(), "is not supported") {
		t.Fatalf("error = %v, want unsupported command validation", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

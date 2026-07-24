package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testVoiceCallLLMCallID = "a5c8726b-280b-48db-890e-e0c6c07c1bdc"
	testVoiceCallLLMAPIKey = "rtc-provider-secret"
)

func TestVoiceCallLLMRejectsInvalidBearerBeforeDecoding(t *testing.T) {
	processor := &fakeVoiceCallLLMProcessor{}
	handler := &Handler{
		VoiceCallLLMProcessor: processor,
		VoiceCallLLMAPIKey:    testVoiceCallLLMAPIKey,
	}

	for _, authorization := range []string{"", "Bearer wrong-secret"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/voice-calls/llm",
			strings.NewReader("{malformed"),
		)
		request.Header.Set("Authorization", authorization)

		handler.HandleVoiceCallLLM(response, request)

		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status = %d, want %d", authorization, response.Code, http.StatusUnauthorized)
		}
	}
	if processor.calls != 0 {
		t.Fatalf("processor calls = %d, want 0", processor.calls)
	}
}

func TestVoiceCallLLMValidatesProviderRequest(t *testing.T) {
	testCases := []struct {
		name string
		body string
		code int
	}{
		{
			name: "malformed JSON",
			body: "{malformed",
			code: http.StatusBadRequest,
		},
		{
			name: "trailing JSON",
			body: `{"stream":true} {}`,
			code: http.StatusBadRequest,
		},
		{
			name: "stream disabled",
			body: `{"voice_call_id":"` + testVoiceCallLLMCallID + `","round_id":1,"messages":[{"role":"user","content":"hello"}]}`,
			code: http.StatusBadRequest,
		},
		{
			name: "invalid call ID",
			body: `{"stream":true,"voice_call_id":"not-a-uuid","round_id":1,"messages":[{"role":"user","content":"hello"}]}`,
			code: http.StatusBadRequest,
		},
		{
			name: "missing round ID",
			body: `{"stream":true,"voice_call_id":"` + testVoiceCallLLMCallID + `","messages":[{"role":"user","content":"hello"}]}`,
			code: http.StatusBadRequest,
		},
		{
			name: "invalid round ID",
			body: `{"stream":true,"voice_call_id":"` + testVoiceCallLLMCallID + `","round_id":-1,"messages":[{"role":"user","content":"hello"}]}`,
			code: http.StatusBadRequest,
		},
		{
			name: "missing transcript",
			body: `{"stream":true,"voice_call_id":"` + testVoiceCallLLMCallID + `","round_id":1,"messages":[{"role":"assistant","content":"hello"}]}`,
			code: http.StatusBadRequest,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			processor := &fakeVoiceCallLLMProcessor{}
			handler := &Handler{
				VoiceCallLLMProcessor: processor,
				VoiceCallLLMAPIKey:    testVoiceCallLLMAPIKey,
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/voice-calls/llm",
				strings.NewReader(testCase.body),
			)
			request.Header.Set("Authorization", "Bearer "+testVoiceCallLLMAPIKey)

			handler.HandleVoiceCallLLM(response, request)

			if response.Code != testCase.code {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, testCase.code, response.Body.String())
			}
			if processor.calls != 0 {
				t.Fatalf("processor calls = %d, want 0", processor.calls)
			}
		})
	}
}

func TestVoiceCallLLMRejectsOversizedRequest(t *testing.T) {
	processor := &fakeVoiceCallLLMProcessor{}
	handler := &Handler{
		VoiceCallLLMProcessor: processor,
		VoiceCallLLMAPIKey:    testVoiceCallLLMAPIKey,
	}
	response := httptest.NewRecorder()
	body := bytes.NewBufferString(
		`{"stream":true,"voice_call_id":"` + testVoiceCallLLMCallID +
			`","round_id":1,"messages":[{"role":"user","content":"`,
	)
	body.Write(bytes.Repeat([]byte("x"), maxVoiceCallLLMRequestBytes))
	body.WriteString(`"}]}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/voice-calls/llm",
		io.NopCloser(body),
	)
	request.ContentLength = -1
	request.Header.Set("Authorization", "Bearer "+testVoiceCallLLMAPIKey)

	handler.HandleVoiceCallLLM(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	if processor.calls != 0 {
		t.Fatalf("processor calls = %d, want 0", processor.calls)
	}
}

func TestVoiceCallLLMNormalizesRoundAndStreamsOpenAIChunk(t *testing.T) {
	for _, roundID := range []string{"7", `"round-7"`} {
		t.Run(roundID, func(t *testing.T) {
			processor := &fakeVoiceCallLLMProcessor{
				reply: VoiceCallLLMReply{Content: "  这是贝克汉姆的回答。  "},
			}
			handler := &Handler{
				VoiceCallLLMProcessor: processor,
				VoiceCallLLMAPIKey:    testVoiceCallLLMAPIKey,
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/voice-calls/llm",
				strings.NewReader(
					`{"stream":true,"voice_call_id":"`+testVoiceCallLLMCallID+
						`","round_id":`+roundID+
						`,"messages":[{"role":"system","content":"provider context"},`+
						`{"role":"user","content":"旧问题"},{"role":"assistant","content":"旧回答"},`+
						`{"role":"user","content":"  当前问题  "}],"temperature":0.2}`,
				),
			)
			request.Header.Set("Authorization", "Bearer "+testVoiceCallLLMAPIKey)

			handler.HandleVoiceCallLLM(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
			}
			if processor.calls != 1 {
				t.Fatalf("processor calls = %d, want 1", processor.calls)
			}
			wantRoundID := strings.Trim(roundID, `"`)
			if processor.input != (VoiceCallLLMInput{
				VoiceCallID: testVoiceCallLLMCallID,
				RoundID:     wantRoundID,
				Transcript:  "当前问题",
			}) {
				t.Fatalf("processor input = %#v", processor.input)
			}
			if got := response.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			body := response.Body.String()
			for _, want := range []string{
				`data: {"id":"voice-call-` + testVoiceCallLLMCallID + `-` + wantRoundID + `"`,
				`"object":"chat.completion.chunk"`,
				`"model":"` + voiceCallLLMModel + `"`,
				`"role":"assistant","content":"这是贝克汉姆的回答。"`,
				`"finish_reason":null`,
				`"delta":{},"finish_reason":"stop"`,
				"data: [DONE]",
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("response body missing %q: %s", want, body)
				}
			}
		})
	}
}

func TestVoiceCallLLMUnavailableAndProcessorFailuresStayOpaque(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/voice-calls/llm", nil)

		(&Handler{}).HandleVoiceCallLLM(response, request)

		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("processor failure", func(t *testing.T) {
		handler := &Handler{
			VoiceCallLLMProcessor: &fakeVoiceCallLLMProcessor{
				err: errors.New("database password and internal details"),
			},
			VoiceCallLLMAPIKey: testVoiceCallLLMAPIKey,
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/voice-calls/llm",
			strings.NewReader(
				`{"stream":true,"voice_call_id":"`+testVoiceCallLLMCallID+
					`","round_id":1,"messages":[{"role":"user","content":"hello"}]}`,
			),
		)
		request.Header.Set("Authorization", "Bearer "+testVoiceCallLLMAPIKey)

		handler.HandleVoiceCallLLM(response, request)

		if response.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
		}
		if strings.Contains(response.Body.String(), "database password") {
			t.Fatalf("response leaked processor error: %s", response.Body.String())
		}
	})

	t.Run("processor timeout", func(t *testing.T) {
		handler := &Handler{
			VoiceCallLLMProcessor: &fakeVoiceCallLLMProcessor{
				err: errVoiceCallAgentTurnTimeout,
			},
			VoiceCallLLMAPIKey: testVoiceCallLLMAPIKey,
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/voice-calls/llm",
			strings.NewReader(
				`{"stream":true,"voice_call_id":"`+testVoiceCallLLMCallID+
					`","round_id":1,"messages":[{"role":"user","content":"hello"}]}`,
			),
		)
		request.Header.Set("Authorization", "Bearer "+testVoiceCallLLMAPIKey)

		handler.HandleVoiceCallLLM(response, request)

		if response.Code != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusGatewayTimeout)
		}
	})
}

type fakeVoiceCallLLMProcessor struct {
	calls int
	input VoiceCallLLMInput
	reply VoiceCallLLMReply
	err   error
}

func (processor *fakeVoiceCallLLMProcessor) Reply(
	_ context.Context,
	input VoiceCallLLMInput,
) (VoiceCallLLMReply, error) {
	processor.calls++
	processor.input = input
	return processor.reply, processor.err
}

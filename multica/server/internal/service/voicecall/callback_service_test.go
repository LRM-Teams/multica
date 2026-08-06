package voicecall

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
)

func TestCallbackServiceMapsConversationStagesToSessionState(t *testing.T) {
	nonErrorStages := []volcenginertc.ConversationStageCode{
		volcenginertc.ConversationStageListening,
		volcenginertc.ConversationStageThinking,
		volcenginertc.ConversationStageAnswering,
		volcenginertc.ConversationStageInterrupted,
		volcenginertc.ConversationStageAnswerFinished,
	}
	for _, stage := range nonErrorStages {
		t.Run(stageNameForTest(stage), func(t *testing.T) {
			wantSession := Session{ID: "call-1", Status: StatusActive}
			store := &fakeCallbackStore{session: wantSession}
			service, err := NewCallbackService("volcengine", store)
			if err != nil {
				t.Fatalf("new callback service: %v", err)
			}

			session, err := service.HandleConversationStatus(
				context.Background(),
				volcenginertc.ConversationStatus{
					TaskID: "voice-task-1",
					Stage:  volcenginertc.ConversationStage{Code: stage},
				},
			)
			if err != nil {
				t.Fatalf("handle callback: %v", err)
			}
			if session != wantSession {
				t.Fatalf("session = %+v, want %+v", session, wantSession)
			}
			if store.activeCalls != 1 ||
				store.provider != "volcengine" ||
				store.taskID != "voice-task-1" ||
				store.failureCalls != 0 {
				t.Fatalf("store = %+v", store)
			}
		})
	}
}

func TestCallbackServiceMapsVoiceChatTaskStartToActive(t *testing.T) {
	wantSession := Session{ID: "call-1", Status: StatusActive}
	store := &fakeCallbackStore{session: wantSession}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}

	session, changed, err := service.HandleVoiceChatTaskEvent(
		context.Background(),
		volcenginertc.VoiceChatTaskEvent{
			TaskID:    "voice-task-1",
			EventType: volcenginertc.VoiceChatTaskStateChanged,
			RunStage:  volcenginertc.VoiceChatRunStageTaskStart,
		},
	)
	if err != nil {
		t.Fatalf("handle task event: %v", err)
	}
	if !changed || session != wantSession {
		t.Fatalf("session = %+v, changed = %t", session, changed)
	}
	if store.activeCalls != 1 ||
		store.provider != "volcengine" ||
		store.taskID != "voice-task-1" ||
		store.failureCalls != 0 {
		t.Fatalf("store = %+v", store)
	}
}

func TestCallbackServiceMapsVoiceChatProgressStagesToActive(t *testing.T) {
	stages := []volcenginertc.VoiceChatRunStage{
		volcenginertc.VoiceChatRunStageBeginAsking,
		volcenginertc.VoiceChatRunStageASRFinish,
		volcenginertc.VoiceChatRunStageLLMOutput,
		volcenginertc.VoiceChatRunStageAnswerStart,
		volcenginertc.VoiceChatRunStageAnswerFinish,
		volcenginertc.VoiceChatRunStageInterrupted,
		volcenginertc.VoiceChatRunStageReasoningStart,
		volcenginertc.VoiceChatRunStageASR,
		volcenginertc.VoiceChatRunStageLLM,
		volcenginertc.VoiceChatRunStageTTS,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			store := &fakeCallbackStore{
				session: Session{ID: "call-1", Status: StatusActive},
			}
			service, err := NewCallbackService("volcengine", store)
			if err != nil {
				t.Fatalf("new callback service: %v", err)
			}

			_, changed, err := service.HandleVoiceChatTaskEvent(
				context.Background(),
				volcenginertc.VoiceChatTaskEvent{
					TaskID:    "voice-task-1",
					EventType: volcenginertc.VoiceChatTaskStateChanged,
					RunStage:  stage,
				},
			)
			if err != nil {
				t.Fatalf("handle task event: %v", err)
			}
			if !changed || store.activeCalls != 1 {
				t.Fatalf(
					"changed = %t, active calls = %d",
					changed,
					store.activeCalls,
				)
			}
		})
	}
}

func TestCallbackServiceRecordsVoiceChatTaskError(t *testing.T) {
	wantSession := Session{ID: "call-1", Status: StatusFailed}
	store := &fakeCallbackStore{session: wantSession}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}

	session, changed, err := service.HandleVoiceChatTaskEvent(
		context.Background(),
		volcenginertc.VoiceChatTaskEvent{
			TaskID:    "voice-task-1",
			EventType: volcenginertc.VoiceChatTaskError,
			RunStage:  volcenginertc.VoiceChatRunStagePreParamCheck,
			ErrorInfo: &volcenginertc.VoiceChatTaskErrorInfo{
				ErrorCode: 1003006,
				Reason:    "ASR connection failed",
			},
		},
	)
	if err != nil {
		t.Fatalf("handle task event: %v", err)
	}
	if !changed || session != wantSession {
		t.Fatalf("session = %+v, changed = %t", session, changed)
	}
	if store.failureCalls != 1 ||
		store.provider != "volcengine" ||
		store.taskID != "voice-task-1" ||
		store.errorCode != "volcengine_1003006" ||
		store.activeCalls != 0 {
		t.Fatalf("store = %+v", store)
	}
}

func TestCallbackServiceRecordsProviderErrorCode(t *testing.T) {
	wantSession := Session{ID: "call-1", Status: StatusFailed}
	store := &fakeCallbackStore{session: wantSession}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}

	session, err := service.HandleConversationStatus(
		context.Background(),
		volcenginertc.ConversationStatus{
			TaskID: "voice-task-1",
			Stage: volcenginertc.ConversationStage{
				Code: volcenginertc.ConversationStageError,
			},
			ErrorInfo: &volcenginertc.ConversationError{
				ErrorCode: 1005002,
				Reason:    "TTS request failed",
			},
		},
	)
	if err != nil {
		t.Fatalf("handle callback: %v", err)
	}
	if session != wantSession {
		t.Fatalf("session = %+v, want %+v", session, wantSession)
	}
	if store.failureCalls != 1 ||
		store.provider != "volcengine" ||
		store.taskID != "voice-task-1" ||
		store.errorCode != "volcengine_1005002" ||
		store.activeCalls != 0 {
		t.Fatalf("store = %+v", store)
	}
}

func TestCallbackServiceRejectsInvalidAndPropagatesStoreErrors(t *testing.T) {
	if _, err := NewCallbackService("", &fakeCallbackStore{}); err == nil {
		t.Fatal("blank provider name accepted")
	}
	if _, err := NewCallbackService("volcengine", nil); err == nil {
		t.Fatal("nil store accepted")
	}

	store := &fakeCallbackStore{err: errors.New("database unavailable")}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}
	_, err = service.HandleConversationStatus(
		context.Background(),
		volcenginertc.ConversationStatus{
			TaskID: "voice-task-1",
			Stage: volcenginertc.ConversationStage{
				Code: volcenginertc.ConversationStageListening,
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("error = %v", err)
	}

	_, err = service.HandleConversationStatus(
		context.Background(),
		volcenginertc.ConversationStatus{
			TaskID: "voice-task-1",
			Stage:  volcenginertc.ConversationStage{Code: 99},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v", err)
	}
}

func TestCallbackServicePersistsFinalConversationSubtitleTurns(t *testing.T) {
	store := &fakeCallbackStore{}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}

	err = service.HandleConversationSubtitle(
		context.Background(),
		volcenginertc.ConversationSubtitle{
			Type: "subtitle",
			Data: []volcenginertc.ConversationSubtitleSegment{
				{
					Definite:  true,
					Paragraph: true,
					Text:      "  member question  ",
					UserID:    "voice-member-call_1",
					RoundID:   3,
				},
				{
					Definite:  true,
					Paragraph: true,
					Text:      "agent answer",
					UserID:    "voice-agent-call_1",
					RoundID:   3,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("handle subtitle: %v", err)
	}
	if len(store.turns) != 2 {
		t.Fatalf("turns = %+v, want two", store.turns)
	}
	member := store.turns[0]
	if member.Provider != "volcengine" ||
		member.ProviderTaskID != "voice-task-call_1" ||
		member.Sequence != 7 ||
		member.Speaker != SpeakerMember ||
		member.Transcript != "  member question  " ||
		member.ProviderTurnID != "voice-member-call_1:3" {
		t.Fatalf("member turn = %+v", member)
	}
	agent := store.turns[1]
	if agent.ProviderTaskID != "voice-task-call_1" ||
		agent.Sequence != 8 ||
		agent.Speaker != SpeakerAgent ||
		agent.Transcript != "agent answer" ||
		agent.ProviderTurnID != "voice-agent-call_1:3" {
		t.Fatalf("agent turn = %+v", agent)
	}
}

func TestCallbackServiceIgnoresStreamingAndEmptyFinalSubtitleSegments(t *testing.T) {
	store := &fakeCallbackStore{}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}

	err = service.HandleConversationSubtitle(
		context.Background(),
		volcenginertc.ConversationSubtitle{
			Type: "subtitle",
			Data: []volcenginertc.ConversationSubtitleSegment{
				{
					Definite:  true,
					Paragraph: false,
					Text:      "streaming clause",
					UserID:    "voice-member-call-1",
					RoundID:   0,
				},
				{
					Definite:  true,
					Paragraph: true,
					Text:      " \n\t ",
					UserID:    "voice-member-call-1",
					RoundID:   0,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("handle subtitle: %v", err)
	}
	if len(store.turns) != 0 {
		t.Fatalf("turns = %+v, want none", store.turns)
	}
}

func TestCallbackServiceRejectsInvalidFinalSubtitleIdentityAndSequence(t *testing.T) {
	tests := []struct {
		name    string
		segment volcenginertc.ConversationSubtitleSegment
	}{
		{
			name: "paragraph is not definite",
			segment: volcenginertc.ConversationSubtitleSegment{
				Paragraph: true,
				Text:      "not final",
				UserID:    "voice-member-call-1",
			},
		},
		{
			name: "unknown speaker identity",
			segment: volcenginertc.ConversationSubtitleSegment{
				Definite:  true,
				Paragraph: true,
				Text:      "unknown",
				UserID:    "external-user",
			},
		},
		{
			name: "invalid call nonce",
			segment: volcenginertc.ConversationSubtitleSegment{
				Definite:  true,
				Paragraph: true,
				Text:      "invalid nonce",
				UserID:    "voice-member-call/1",
			},
		},
		{
			name: "sequence overflow",
			segment: volcenginertc.ConversationSubtitleSegment{
				Definite:  true,
				Paragraph: true,
				Text:      "too large",
				UserID:    "voice-agent-call-1",
				RoundID:   math.MaxInt64,
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeCallbackStore{}
			service, err := NewCallbackService("volcengine", store)
			if err != nil {
				t.Fatalf("new callback service: %v", err)
			}
			err = service.HandleConversationSubtitle(
				context.Background(),
				volcenginertc.ConversationSubtitle{
					Type: "subtitle",
					Data: []volcenginertc.ConversationSubtitleSegment{
						testCase.segment,
					},
				},
			)
			if err == nil {
				t.Fatal("invalid subtitle accepted")
			}
			if len(store.turns) != 0 {
				t.Fatalf("turns = %+v, want none", store.turns)
			}
		})
	}
}

func TestCallbackServiceRejectsMixedCallSubtitleBatchBeforePersistence(t *testing.T) {
	store := &fakeCallbackStore{}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}
	err = service.HandleConversationSubtitle(
		context.Background(),
		volcenginertc.ConversationSubtitle{
			Type: "subtitle",
			Data: []volcenginertc.ConversationSubtitleSegment{
				{
					Definite:  true,
					Paragraph: true,
					Text:      "first call",
					UserID:    "voice-member-call-1",
				},
				{
					Definite:  true,
					Paragraph: true,
					Text:      "second call",
					UserID:    "voice-agent-call-2",
				},
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "mixes multiple calls") {
		t.Fatalf("error = %v", err)
	}
	if len(store.turns) != 0 {
		t.Fatalf("turns = %+v, want none", store.turns)
	}
}

func TestCallbackServicePropagatesSubtitleStoreError(t *testing.T) {
	store := &fakeCallbackStore{turnErr: errors.New("database unavailable")}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}
	err = service.HandleConversationSubtitle(
		context.Background(),
		volcenginertc.ConversationSubtitle{
			Type: "subtitle",
			Data: []volcenginertc.ConversationSubtitleSegment{{
				Definite:  true,
				Paragraph: true,
				Text:      "persist me",
				UserID:    "voice-member-call-1",
			}},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("error = %v", err)
	}
}

type fakeCallbackStore struct {
	activeCalls  int
	failureCalls int
	session      Session
	provider     string
	taskID       string
	errorCode    string
	err          error
	turns        []ProviderTurnInput
	turnErr      error
}

func (store *fakeCallbackStore) ApplyProviderActive(
	_ context.Context,
	provider string,
	taskID string,
) (Session, error) {
	store.activeCalls++
	store.provider = provider
	store.taskID = taskID
	return store.session, store.err
}

func (store *fakeCallbackStore) ApplyProviderFailure(
	_ context.Context,
	provider string,
	taskID string,
	errorCode string,
) (Session, error) {
	store.failureCalls++
	store.provider = provider
	store.taskID = taskID
	store.errorCode = errorCode
	return store.session, store.err
}

func (store *fakeCallbackStore) UpsertProviderTurn(
	_ context.Context,
	input ProviderTurnInput,
) (Turn, error) {
	store.turns = append(store.turns, input)
	return Turn{}, store.turnErr
}

func stageNameForTest(stage volcenginertc.ConversationStageCode) string {
	return "stage_" + strconv.Itoa(int(stage))
}

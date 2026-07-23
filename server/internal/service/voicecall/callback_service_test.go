package voicecall

import (
	"context"
	"errors"
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
			store := &fakeCallbackStore{}
			service, err := NewCallbackService("volcengine", store)
			if err != nil {
				t.Fatalf("new callback service: %v", err)
			}

			err = service.HandleConversationStatus(
				context.Background(),
				volcenginertc.ConversationStatus{
					TaskID: "voice-task-1",
					Stage:  volcenginertc.ConversationStage{Code: stage},
				},
			)
			if err != nil {
				t.Fatalf("handle callback: %v", err)
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

func TestCallbackServiceRecordsProviderErrorCode(t *testing.T) {
	store := &fakeCallbackStore{}
	service, err := NewCallbackService("volcengine", store)
	if err != nil {
		t.Fatalf("new callback service: %v", err)
	}

	err = service.HandleConversationStatus(
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
	err = service.HandleConversationStatus(
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

	err = service.HandleConversationStatus(
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

type fakeCallbackStore struct {
	activeCalls  int
	failureCalls int
	provider     string
	taskID       string
	errorCode    string
	err          error
}

func (store *fakeCallbackStore) ApplyProviderActive(
	_ context.Context,
	provider string,
	taskID string,
) (Session, error) {
	store.activeCalls++
	store.provider = provider
	store.taskID = taskID
	return Session{}, store.err
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
	return Session{}, store.err
}

func stageNameForTest(stage volcenginertc.ConversationStageCode) string {
	return "stage_" + strconv.Itoa(int(stage))
}

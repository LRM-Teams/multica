package voicecall

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
)

type CallbackStore interface {
	ApplyProviderActive(
		ctx context.Context,
		provider string,
		providerTaskID string,
	) (Session, error)
	ApplyProviderFailure(
		ctx context.Context,
		provider string,
		providerTaskID string,
		errorCode string,
	) (Session, error)
}

type CallbackService struct {
	providerName string
	store        CallbackStore
}

func NewCallbackService(providerName string, store CallbackStore) (*CallbackService, error) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, errors.New("voice call callback provider name is required")
	}
	if store == nil {
		return nil, errors.New("voice call callback store is required")
	}
	return &CallbackService{providerName: providerName, store: store}, nil
}

func (service *CallbackService) HandleConversationStatus(
	ctx context.Context,
	status volcenginertc.ConversationStatus,
) error {
	taskID := strings.TrimSpace(status.TaskID)
	if taskID == "" {
		return errors.New("voice call callback task ID is required")
	}

	switch status.Stage.Code {
	case volcenginertc.ConversationStageError:
		if status.ErrorInfo == nil || status.ErrorInfo.ErrorCode <= 0 {
			return errors.New("voice call provider error details are required")
		}
		errorCode := service.providerName + "_" +
			strconv.FormatInt(status.ErrorInfo.ErrorCode, 10)
		if _, err := service.store.ApplyProviderFailure(
			ctx,
			service.providerName,
			taskID,
			errorCode,
		); err != nil {
			return fmt.Errorf("apply voice call provider failure: %w", err)
		}
		return nil
	case volcenginertc.ConversationStageListening,
		volcenginertc.ConversationStageThinking,
		volcenginertc.ConversationStageAnswering,
		volcenginertc.ConversationStageInterrupted,
		volcenginertc.ConversationStageAnswerFinished:
		if _, err := service.store.ApplyProviderActive(
			ctx,
			service.providerName,
			taskID,
		); err != nil {
			return fmt.Errorf("apply voice call provider activity: %w", err)
		}
		return nil
	default:
		return fmt.Errorf(
			"unsupported voice call provider stage %d",
			status.Stage.Code,
		)
	}
}

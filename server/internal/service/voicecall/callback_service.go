package voicecall

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	UpsertProviderTurn(
		ctx context.Context,
		input ProviderTurnInput,
	) (Turn, error)
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

func (service *CallbackService) HandleConversationSubtitle(
	ctx context.Context,
	subtitle volcenginertc.ConversationSubtitle,
) error {
	inputs := make([]ProviderTurnInput, 0, len(subtitle.Data))
	callbackTaskID := ""
	for index, segment := range subtitle.Data {
		if !segment.Paragraph {
			continue
		}
		if !segment.Definite {
			return fmt.Errorf(
				"voice call subtitle data[%d] final paragraph is not definite",
				index,
			)
		}
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}

		taskID, speaker, err := providerSubtitleIdentity(segment.UserID)
		if err != nil {
			return fmt.Errorf("voice call subtitle data[%d]: %w", index, err)
		}
		if callbackTaskID == "" {
			callbackTaskID = taskID
		} else if taskID != callbackTaskID {
			return errors.New("voice call subtitle callback mixes multiple calls")
		}
		sequence, err := providerSubtitleTurnSequence(segment.RoundID, speaker)
		if err != nil {
			return fmt.Errorf("voice call subtitle data[%d]: %w", index, err)
		}
		inputs = append(inputs, ProviderTurnInput{
			Provider:       service.providerName,
			ProviderTaskID: taskID,
			Sequence:       sequence,
			Speaker:        speaker,
			Transcript:     segment.Text,
			ProviderTurnID: segment.UserID + ":" + strconv.FormatInt(segment.RoundID, 10),
		})
	}

	for _, input := range inputs {
		if _, err := service.store.UpsertProviderTurn(ctx, input); err != nil {
			return fmt.Errorf("upsert voice call provider subtitle turn: %w", err)
		}
	}
	return nil
}

func providerSubtitleIdentity(userID string) (string, Speaker, error) {
	userID = strings.TrimSpace(userID)
	var nonce string
	var speaker Speaker
	switch {
	case strings.HasPrefix(userID, providerMemberIDPrefix):
		nonce = strings.TrimPrefix(userID, providerMemberIDPrefix)
		speaker = SpeakerMember
	case strings.HasPrefix(userID, providerAgentIDPrefix):
		nonce = strings.TrimPrefix(userID, providerAgentIDPrefix)
		speaker = SpeakerAgent
	default:
		return "", "", errors.New("speaker identity is not scoped to a voice call")
	}
	if err := validateNonce(nonce); err != nil {
		return "", "", fmt.Errorf("speaker identity has an invalid call nonce: %w", err)
	}
	return providerTaskIDPrefix + nonce, speaker, nil
}

func providerSubtitleTurnSequence(roundID int64, speaker Speaker) (int64, error) {
	var offset int64
	switch speaker {
	case SpeakerMember:
		offset = 1
	case SpeakerAgent:
		offset = 2
	default:
		return 0, errors.New("speaker must be member or agent")
	}
	if roundID < 0 || roundID > (math.MaxInt64-offset)/2 {
		return 0, errors.New("round ID cannot be represented as a call turn sequence")
	}
	return roundID*2 + offset, nil
}

package voicecall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
)

type VolcengineVoiceClient interface {
	StartVoiceChat(
		ctx context.Context,
		request volcenginertc.StartVoiceChatRequest,
	) (volcenginertc.Response, error)
	StopVoiceChat(
		ctx context.Context,
		request volcenginertc.StopVoiceChatRequest,
	) (volcenginertc.Response, error)
}

type VolcengineTokenSigner interface {
	AppID() string
	Sign(roomID, userID string) (volcenginertc.SignedRoomToken, error)
}

type VolcengineProviderConfig struct {
	CustomLLMURL        string
	CustomLLMAPIKey     string
	TTSVoiceID          string
	CallbackURL         string
	CallbackSignature   string
	CompensationTimeout time.Duration
}

type VolcengineProvider struct {
	appID               string
	customLLMURL        string
	customLLMAPIKey     string
	ttsVoiceID          string
	callbackURL         string
	callbackSignature   string
	compensationTimeout time.Duration
	client              VolcengineVoiceClient
	tokenSigner         VolcengineTokenSigner
}

func NewVolcengineProvider(
	config VolcengineProviderConfig,
	client VolcengineVoiceClient,
	tokenSigner VolcengineTokenSigner,
) (*VolcengineProvider, error) {
	if client == nil {
		return nil, errors.New("Volcengine voice client is required")
	}
	if tokenSigner == nil {
		return nil, errors.New("Volcengine room token signer is required")
	}
	appID := strings.TrimSpace(tokenSigner.AppID())
	if appID == "" {
		return nil, errors.New("Volcengine room token signer AppID is required")
	}
	if config.CompensationTimeout <= 0 {
		return nil, errors.New("Volcengine compensation timeout must be positive")
	}

	customLLMURL := strings.TrimSpace(config.CustomLLMURL)
	customLLMAPIKey := strings.TrimSpace(config.CustomLLMAPIKey)
	ttsVoiceID := strings.TrimSpace(config.TTSVoiceID)
	callbackURL := strings.TrimSpace(config.CallbackURL)
	callbackSignature := strings.TrimSpace(config.CallbackSignature)
	if _, err := volcenginertc.BuildStartConfiguration(
		volcenginertc.StartConfigurationInput{
			TargetUserID:      "configuration-member",
			AgentUserID:       "configuration-agent",
			VoiceCallID:       "configuration-call",
			SystemMessages:    []string{"configuration validation"},
			CustomLLMURL:      customLLMURL,
			CustomLLMAPIKey:   customLLMAPIKey,
			TTSVoiceID:        ttsVoiceID,
			CallbackURL:       callbackURL,
			CallbackSignature: callbackSignature,
		},
	); err != nil {
		return nil, fmt.Errorf("validate Volcengine voice configuration: %w", err)
	}

	return &VolcengineProvider{
		appID:               appID,
		customLLMURL:        customLLMURL,
		customLLMAPIKey:     customLLMAPIKey,
		ttsVoiceID:          ttsVoiceID,
		callbackURL:         callbackURL,
		callbackSignature:   callbackSignature,
		compensationTimeout: config.CompensationTimeout,
		client:              client,
		tokenSigner:         tokenSigner,
	}, nil
}

func (provider *VolcengineProvider) Start(
	ctx context.Context,
	input ProviderStartInput,
) (ProviderStartResult, error) {
	configuration, err := volcenginertc.BuildStartConfiguration(
		volcenginertc.StartConfigurationInput{
			TargetUserID:      input.TargetUserID,
			AgentUserID:       input.AgentUserID,
			VoiceCallID:       input.CallID,
			WelcomeMessage:    input.WelcomeMessage,
			SystemMessages:    input.SystemMessages,
			CustomLLMURL:      provider.customLLMURL,
			CustomLLMAPIKey:   provider.customLLMAPIKey,
			TTSVoiceID:        provider.ttsVoiceID,
			CallbackURL:       provider.callbackURL,
			CallbackSignature: provider.callbackSignature,
		},
	)
	if err != nil {
		return ProviderStartResult{}, fmt.Errorf("build Volcengine conversation configuration: %w", err)
	}

	signedToken, err := provider.tokenSigner.Sign(input.RoomID, input.TargetUserID)
	if err != nil {
		return ProviderStartResult{}, fmt.Errorf("sign room token: %w", err)
	}
	_, err = provider.client.StartVoiceChat(ctx, volcenginertc.StartVoiceChatRequest{
		AppID:       provider.appID,
		RoomID:      input.RoomID,
		TaskID:      input.TaskID,
		Config:      configuration.Config,
		AgentConfig: configuration.AgentConfig,
	})
	if err != nil {
		var providerError *volcenginertc.ProviderError
		if errors.As(err, &providerError) {
			return ProviderStartResult{}, err
		}

		compensationContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			provider.compensationTimeout,
		)
		defer cancel()
		_, stopErr := provider.client.StopVoiceChat(
			compensationContext,
			volcenginertc.StopVoiceChatRequest{
				AppID:  provider.appID,
				RoomID: input.RoomID,
				TaskID: input.TaskID,
			},
		)
		if stopErr != nil {
			return ProviderStartResult{}, &ProviderStartUncertainError{
				Err: errors.Join(
					err,
					fmt.Errorf("compensate Volcengine voice task: %w", stopErr),
				),
			}
		}
		return ProviderStartResult{}, err
	}

	return ProviderStartResult{
		AppID:     provider.appID,
		Token:     signedToken.Value,
		ExpiresAt: signedToken.ExpiresAt,
	}, nil
}

func (provider *VolcengineProvider) Stop(
	ctx context.Context,
	identity ProviderCallIdentity,
) error {
	_, err := provider.client.StopVoiceChat(ctx, volcenginertc.StopVoiceChatRequest{
		AppID:  provider.appID,
		RoomID: identity.RoomID,
		TaskID: identity.TaskID,
	})
	if err != nil {
		return fmt.Errorf("stop Volcengine voice task: %w", err)
	}
	return nil
}

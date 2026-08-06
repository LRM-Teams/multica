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
	ArkEndpointID       string
	ASRAppID            string
	TTSAppID            string
	SpeechAccessToken   string
	TTSVoiceID          string
	CallbackURL         string
	CallbackSignature   string
	CompensationTimeout time.Duration
}

type VolcengineProvider struct {
	appID               string
	arkEndpointID       string
	asrAppID            string
	ttsAppID            string
	speechAccessToken   string
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

	arkEndpointID := strings.TrimSpace(config.ArkEndpointID)
	asrAppID := strings.TrimSpace(config.ASRAppID)
	ttsAppID := strings.TrimSpace(config.TTSAppID)
	speechAccessToken := strings.TrimSpace(config.SpeechAccessToken)
	ttsVoiceID := strings.TrimSpace(config.TTSVoiceID)
	callbackURL := strings.TrimSpace(config.CallbackURL)
	callbackSignature := strings.TrimSpace(config.CallbackSignature)
	if _, err := volcenginertc.BuildStartConfiguration(
		volcenginertc.StartConfigurationInput{
			TargetUserID:      "configuration-member",
			AgentUserID:       "configuration-agent",
			SystemMessages:    []string{"configuration validation"},
			ArkEndpointID:     arkEndpointID,
			ASRAppID:          asrAppID,
			TTSAppID:          ttsAppID,
			SpeechAccessToken: speechAccessToken,
			TTSVoiceID:        ttsVoiceID,
			CallbackURL:       callbackURL,
			CallbackSignature: callbackSignature,
		},
	); err != nil {
		return nil, fmt.Errorf("validate Volcengine voice configuration: %w", err)
	}

	return &VolcengineProvider{
		appID:               appID,
		arkEndpointID:       arkEndpointID,
		asrAppID:            asrAppID,
		ttsAppID:            ttsAppID,
		speechAccessToken:   speechAccessToken,
		ttsVoiceID:          ttsVoiceID,
		callbackURL:         callbackURL,
		callbackSignature:   callbackSignature,
		compensationTimeout: config.CompensationTimeout,
		client:              client,
		tokenSigner:         tokenSigner,
	}, nil
}

func (provider *VolcengineProvider) Prepare(
	_ context.Context,
	input ProviderPrepareInput,
) (ProviderPrepareResult, error) {
	signedToken, err := provider.tokenSigner.Sign(input.RoomID, input.TargetUserID)
	if err != nil {
		return ProviderPrepareResult{}, fmt.Errorf("sign room token: %w", err)
	}
	return ProviderPrepareResult{
		AppID:     provider.appID,
		Token:     signedToken.Value,
		ExpiresAt: signedToken.ExpiresAt,
	}, nil
}

func (provider *VolcengineProvider) Connect(
	ctx context.Context,
	input ProviderConnectInput,
) error {
	functionCallbackURL, err := volcenginertc.BuildFunctionCallbackURL(
		provider.callbackURL,
		input.RoomID,
	)
	if err != nil {
		return fmt.Errorf("build Volcengine function callback URL: %w", err)
	}
	configuration, err := volcenginertc.BuildStartConfiguration(
		volcenginertc.StartConfigurationInput{
			TargetUserID:        input.TargetUserID,
			AgentUserID:         input.AgentUserID,
			WelcomeMessage:      input.WelcomeMessage,
			SystemMessages:      input.SystemMessages,
			ArkEndpointID:       provider.arkEndpointID,
			ASRAppID:            provider.asrAppID,
			TTSAppID:            provider.ttsAppID,
			SpeechAccessToken:   provider.speechAccessToken,
			TTSVoiceID:          provider.ttsVoiceID,
			CallbackURL:         provider.callbackURL,
			FunctionCallbackURL: functionCallbackURL,
			CallbackSignature:   provider.callbackSignature,
		},
	)
	if err != nil {
		return fmt.Errorf("build Volcengine conversation configuration: %w", err)
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
			return err
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
			return &ProviderStartUncertainError{
				Err: errors.Join(
					err,
					fmt.Errorf("compensate Volcengine voice task: %w", stopErr),
				),
			}
		}
		return err
	}

	return nil
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

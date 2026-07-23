package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	voiceCallCallbackPath         = "/api/voice-calls/callback"
	defaultVoiceCallTTSVoiceID    = "zh_male_m191_uranus_bigtts"
	defaultVoiceCallTokenTTL      = 30 * time.Minute
	defaultVoiceCallOperationWait = 10 * time.Second
)

var voiceCallOptInEnvironmentNames = []string{
	"VOLCENGINE_RTC_APP_ID",
	"VOLCENGINE_RTC_APP_KEY",
	"VOLCENGINE_RTC_ACCESS_KEY_ID",
	"VOLCENGINE_RTC_SECRET_ACCESS_KEY",
	"VOLCENGINE_RTC_SESSION_TOKEN",
	"VOLCENGINE_RTC_ENDPOINT",
	"VOLCENGINE_RTC_REGION",
	"VOLCENGINE_RTC_ARK_ENDPOINT_ID",
	"VOLCENGINE_RTC_ARK_MODEL_NAME",
	"VOLCENGINE_RTC_TTS_VOICE_ID",
	"VOLCENGINE_RTC_CALLBACK_URL",
	"VOLCENGINE_RTC_CALLBACK_SIGNATURE",
}

var voiceCallEnvironmentNames = append([]string{
	"VOLCENGINE_RTC_TOKEN_TTL",
	"VOLCENGINE_RTC_COMPENSATION_TIMEOUT",
	"VOLCENGINE_RTC_CLEANUP_TIMEOUT",
}, voiceCallOptInEnvironmentNames...)

type voiceCallRuntimeConfig struct {
	AppID               string
	AppKey              string
	AccessKeyID         string
	SecretAccessKey     string
	SessionToken        string
	Endpoint            string
	Region              string
	ArkEndpointID       string
	ArkModelName        string
	TTSVoiceID          string
	CallbackURL         string
	CallbackSignature   string
	TokenTTL            time.Duration
	CompensationTimeout time.Duration
	CleanupTimeout      time.Duration
}

func loadVoiceCallRuntimeConfig(
	getenv func(string) string,
) (voiceCallRuntimeConfig, bool, error) {
	if getenv == nil {
		return voiceCallRuntimeConfig{}, false, errors.New("voice call environment reader is required")
	}
	enabled := false
	for _, name := range voiceCallOptInEnvironmentNames {
		if strings.TrimSpace(getenv(name)) != "" {
			enabled = true
			break
		}
	}
	if !enabled {
		return voiceCallRuntimeConfig{}, false, nil
	}

	config := voiceCallRuntimeConfig{
		AppID:             strings.TrimSpace(getenv("VOLCENGINE_RTC_APP_ID")),
		AppKey:            strings.TrimSpace(getenv("VOLCENGINE_RTC_APP_KEY")),
		AccessKeyID:       strings.TrimSpace(getenv("VOLCENGINE_RTC_ACCESS_KEY_ID")),
		SecretAccessKey:   strings.TrimSpace(getenv("VOLCENGINE_RTC_SECRET_ACCESS_KEY")),
		SessionToken:      strings.TrimSpace(getenv("VOLCENGINE_RTC_SESSION_TOKEN")),
		Endpoint:          strings.TrimSpace(getenv("VOLCENGINE_RTC_ENDPOINT")),
		Region:            strings.TrimSpace(getenv("VOLCENGINE_RTC_REGION")),
		ArkEndpointID:     strings.TrimSpace(getenv("VOLCENGINE_RTC_ARK_ENDPOINT_ID")),
		ArkModelName:      strings.TrimSpace(getenv("VOLCENGINE_RTC_ARK_MODEL_NAME")),
		TTSVoiceID:        strings.TrimSpace(getenv("VOLCENGINE_RTC_TTS_VOICE_ID")),
		CallbackURL:       strings.TrimSpace(getenv("VOLCENGINE_RTC_CALLBACK_URL")),
		CallbackSignature: strings.TrimSpace(getenv("VOLCENGINE_RTC_CALLBACK_SIGNATURE")),
	}

	required := []struct {
		name  string
		value string
	}{
		{name: "access key id", value: config.AccessKeyID},
		{name: "secret access key", value: config.SecretAccessKey},
		{name: "AppID", value: config.AppID},
		{name: "AppKey", value: config.AppKey},
	}
	for _, field := range required {
		if field.value == "" {
			return voiceCallRuntimeConfig{}, true, fmt.Errorf(
				"voice call %s is required after VOLCENGINE_RTC_* opt-in",
				field.name,
			)
		}
	}
	if (config.ArkEndpointID == "") == (config.ArkModelName == "") {
		return voiceCallRuntimeConfig{}, true, errors.New(
			"voice calling requires exactly one VOLCENGINE_RTC_ARK_ENDPOINT_ID or VOLCENGINE_RTC_ARK_MODEL_NAME",
		)
	}
	if config.TTSVoiceID == "" {
		config.TTSVoiceID = strings.TrimSpace(getenv("DOUBAO_TTS_SPEAKER_ID"))
	}
	if config.TTSVoiceID == "" {
		config.TTSVoiceID = defaultVoiceCallTTSVoiceID
	}
	if config.CallbackURL == "" {
		publicURL := strings.TrimRight(
			strings.TrimSpace(getenv("MULTICA_PUBLIC_URL")),
			"/",
		)
		if publicURL == "" {
			return voiceCallRuntimeConfig{}, true, errors.New(
				"voice calling requires VOLCENGINE_RTC_CALLBACK_URL or MULTICA_PUBLIC_URL",
			)
		}
		config.CallbackURL = publicURL + voiceCallCallbackPath
	}
	if config.CallbackSignature == "" {
		return voiceCallRuntimeConfig{}, true, errors.New(
			"voice calling requires VOLCENGINE_RTC_CALLBACK_SIGNATURE",
		)
	}

	var err error
	config.TokenTTL, err = voiceCallDuration(
		getenv,
		"VOLCENGINE_RTC_TOKEN_TTL",
		defaultVoiceCallTokenTTL,
	)
	if err != nil {
		return voiceCallRuntimeConfig{}, true, err
	}
	config.CompensationTimeout, err = voiceCallDuration(
		getenv,
		"VOLCENGINE_RTC_COMPENSATION_TIMEOUT",
		defaultVoiceCallOperationWait,
	)
	if err != nil {
		return voiceCallRuntimeConfig{}, true, err
	}
	config.CleanupTimeout, err = voiceCallDuration(
		getenv,
		"VOLCENGINE_RTC_CLEANUP_TIMEOUT",
		defaultVoiceCallOperationWait,
	)
	if err != nil {
		return voiceCallRuntimeConfig{}, true, err
	}
	return config, true, nil
}

func voiceCallDuration(
	getenv func(string) string,
	name string,
	fallback time.Duration,
) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return value, nil
}

func configureVoiceCallService(
	h *handler.Handler,
	queries *db.Queries,
	getenv func(string) string,
) error {
	config, enabled, err := loadVoiceCallRuntimeConfig(getenv)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if h == nil || queries == nil {
		return errors.New("voice call runtime requires a configured handler and queries")
	}

	client, err := volcenginertc.New(volcenginertc.Config{
		AccessKeyID:     config.AccessKeyID,
		SecretAccessKey: config.SecretAccessKey,
		SessionToken:    config.SessionToken,
		Endpoint:        config.Endpoint,
		Region:          config.Region,
	})
	if err != nil {
		return fmt.Errorf("initialize Volcengine RTC client: %w", err)
	}
	tokenSigner, err := volcenginertc.NewRoomTokenSigner(volcenginertc.RoomTokenConfig{
		AppID:  config.AppID,
		AppKey: config.AppKey,
		TTL:    config.TokenTTL,
	})
	if err != nil {
		return fmt.Errorf("initialize Volcengine RTC room token signer: %w", err)
	}
	provider, err := voicecall.NewVolcengineProvider(
		voicecall.VolcengineProviderConfig{
			ArkEndpointID:       config.ArkEndpointID,
			ArkModelName:        config.ArkModelName,
			TTSVoiceID:          config.TTSVoiceID,
			CallbackURL:         config.CallbackURL,
			CallbackSignature:   config.CallbackSignature,
			CompensationTimeout: config.CompensationTimeout,
		},
		client,
		tokenSigner,
	)
	if err != nil {
		return fmt.Errorf("initialize Volcengine voice provider: %w", err)
	}
	store, err := voicecall.NewPostgresStore(queries)
	if err != nil {
		return fmt.Errorf("initialize voice call store: %w", err)
	}
	authorizer, err := handler.NewVoiceCallDMAuthorizer(h)
	if err != nil {
		return fmt.Errorf("initialize voice call authorizer: %w", err)
	}
	contextBuilder, err := handler.NewVoiceCallContextBuilder(h)
	if err != nil {
		return fmt.Errorf("initialize voice call context builder: %w", err)
	}
	callService, err := voicecall.NewService(
		voicecall.ServiceConfig{
			ProviderName:   "volcengine",
			IDGenerator:    uuid.NewString,
			CleanupTimeout: config.CleanupTimeout,
		},
		store,
		authorizer,
		contextBuilder,
		provider,
	)
	if err != nil {
		return fmt.Errorf("initialize voice call service: %w", err)
	}
	callbackService, err := voicecall.NewCallbackService("volcengine", store)
	if err != nil {
		return fmt.Errorf("initialize voice call callback service: %w", err)
	}
	h.VoiceCallService = callService
	h.VoiceCallCallbackProcessor = callbackService
	h.VoiceCallCallbackSignature = config.CallbackSignature
	return nil
}

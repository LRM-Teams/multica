package volcenginertc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	volcengineASRResourceID = "volc.seedasr.sauc.duration"
	volcengineTTSResourceID = "seed-tts-2.0"
)

type StartConfigurationInput struct {
	TargetUserID      string
	AgentUserID       string
	WelcomeMessage    string
	SystemMessages    []string
	ArkEndpointID     string
	ASRAppID          string
	TTSAppID          string
	SpeechAccessToken string
	TTSVoiceID        string
	CallbackURL       string
	CallbackSignature string
}

type StartConfiguration struct {
	Config      json.RawMessage
	AgentConfig json.RawMessage
}

type startASRConfig struct {
	Provider          string                 `json:"Provider"`
	ProviderParams    startASRProviderParams `json:"ProviderParams"`
	TurnDetectionMode int                    `json:"TurnDetectionMode"`
	VADConfig         startVADConfig         `json:"VADConfig"`
}

type startASRProviderParams struct {
	Mode            string             `json:"Mode"`
	Credential      startASRCredential `json:"Credential"`
	StreamMode      int                `json:"StreamMode"`
	EnableNonstream bool               `json:"enable_nonstream"`
}

type startASRCredential struct {
	AppID         string `json:"AppId"`
	AccessToken   string `json:"AccessToken"`
	APIResourceID string `json:"ApiResourceId"`
}

type startVADConfig struct {
	SilenceTime int  `json:"SilenceTime"`
	AIVAD       bool `json:"AIVAD"`
}

type startLLMConfig struct {
	Mode           string   `json:"Mode"`
	EndpointID     string   `json:"EndPointId"`
	SystemMessages []string `json:"SystemMessages"`
	HistoryLength  int      `json:"HistoryLength"`
	MaxTokens      int      `json:"MaxTokens"`
	ThinkingType   string   `json:"ThinkingType"`
	Prefill        bool     `json:"Prefill"`
}

type startTTSConfig struct {
	AutoActive     bool                   `json:"AutoActive"`
	Provider       string                 `json:"Provider"`
	ProviderParams startTTSProviderParams `json:"ProviderParams"`
	Prefill        bool                   `json:"Prefill"`
}

type startTTSProviderParams struct {
	Credential           startTTSCredential `json:"Credential"`
	VolcanoTTSParameters string             `json:"VolcanoTTSParameters"`
}

type startTTSCredential struct {
	AppID      string `json:"AppId"`
	Token      string `json:"Token"`
	ResourceID string `json:"ResourceId"`
}

type volcanoTTSParameters struct {
	Request volcanoTTSRequest `json:"req_params"`
}

type volcanoTTSRequest struct {
	Speaker string `json:"speaker"`
}

type startSubtitleConfig struct {
	ServerMessageURL       string `json:"ServerMessageUrl"`
	ServerMessageSignature string `json:"ServerMessageSignature"`
	DisableRTSSubtitle     bool   `json:"DisableRTSSubtitle"`
	SubtitleMode           int    `json:"SubtitleMode"`
}

type startAgentConfig struct {
	TargetUserIDs                   []string `json:"TargetUserId"`
	UserID                          string   `json:"UserId"`
	WelcomeMessage                  string   `json:"WelcomeMessage,omitempty"`
	EnableConversationStateCallback bool     `json:"EnableConversationStateCallback"`
	ServerMessageURLForRTS          string   `json:"ServerMessageURLForRTS"`
	ServerMessageSignatureForRTS    string   `json:"ServerMessageSignatureForRTS"`
}

type startConversationConfig struct {
	ASRConfig      startASRConfig      `json:"ASRConfig"`
	LLMConfig      startLLMConfig      `json:"LLMConfig"`
	TTSConfig      startTTSConfig      `json:"TTSConfig"`
	SubtitleConfig startSubtitleConfig `json:"SubtitleConfig"`
	InterruptMode  int                 `json:"InterruptMode"`
}

func BuildStartConfiguration(input StartConfigurationInput) (StartConfiguration, error) {
	targetUserID := strings.TrimSpace(input.TargetUserID)
	if err := validateRoomTokenID("TargetUserId", targetUserID); err != nil {
		return StartConfiguration{}, err
	}
	agentUserID := strings.TrimSpace(input.AgentUserID)
	if err := validateRoomTokenID("Agent UserId", agentUserID); err != nil {
		return StartConfiguration{}, err
	}
	if targetUserID == agentUserID {
		return StartConfiguration{}, errors.New("volcengine RTC Agent UserId must differ from TargetUserId")
	}

	endpointID := strings.TrimSpace(input.ArkEndpointID)
	if endpointID == "" {
		return StartConfiguration{}, errors.New("volcengine RTC Ark endpoint ID is required")
	}
	if len(input.SystemMessages) == 0 {
		return StartConfiguration{}, errors.New("volcengine RTC SystemMessages requires at least one message")
	}
	systemMessages := append([]string(nil), input.SystemMessages...)
	for _, message := range systemMessages {
		if strings.TrimSpace(message) == "" {
			return StartConfiguration{}, errors.New("volcengine RTC SystemMessages must not contain blank messages")
		}
	}

	ttsVoiceID := strings.TrimSpace(input.TTSVoiceID)
	if ttsVoiceID == "" {
		return StartConfiguration{}, errors.New("volcengine RTC TTS voice is required")
	}
	asrAppID := strings.TrimSpace(input.ASRAppID)
	if asrAppID == "" {
		return StartConfiguration{}, errors.New("volcengine RTC ASR AppId is required")
	}
	ttsAppID := strings.TrimSpace(input.TTSAppID)
	if ttsAppID == "" {
		return StartConfiguration{}, errors.New("volcengine RTC TTS AppId is required")
	}
	speechAccessToken := strings.TrimSpace(input.SpeechAccessToken)
	if speechAccessToken == "" {
		return StartConfiguration{}, errors.New("volcengine RTC speech access token is required")
	}
	callbackURL := strings.TrimSpace(input.CallbackURL)
	if err := validatePublicHTTPSURL(callbackURL); err != nil {
		return StartConfiguration{}, fmt.Errorf("volcengine RTC callback URL: %w", err)
	}
	callbackSignature := strings.TrimSpace(input.CallbackSignature)
	if callbackSignature == "" {
		return StartConfiguration{}, errors.New("volcengine RTC callback signature is required")
	}

	nativeTTS, err := json.Marshal(volcanoTTSParameters{
		Request: volcanoTTSRequest{Speaker: ttsVoiceID},
	})
	if err != nil {
		return StartConfiguration{}, fmt.Errorf("encode volcengine RTC TTS parameters: %w", err)
	}
	config, err := json.Marshal(startConversationConfig{
		ASRConfig: startASRConfig{
			Provider: "volcano",
			ProviderParams: startASRProviderParams{
				Mode: "bigmodel",
				Credential: startASRCredential{
					AppID:         asrAppID,
					AccessToken:   speechAccessToken,
					APIResourceID: volcengineASRResourceID,
				},
				StreamMode:      2,
				EnableNonstream: true,
			},
			TurnDetectionMode: 0,
			VADConfig: startVADConfig{
				SilenceTime: 600,
				AIVAD:       true,
			},
		},
		LLMConfig: startLLMConfig{
			Mode:           "ArkV3",
			EndpointID:     endpointID,
			SystemMessages: systemMessages,
			HistoryLength:  10,
			MaxTokens:      256,
			ThinkingType:   "disabled",
			Prefill:        false,
		},
		TTSConfig: startTTSConfig{
			AutoActive: true,
			Provider:   "volcano_bidirection",
			ProviderParams: startTTSProviderParams{
				Credential: startTTSCredential{
					AppID:      ttsAppID,
					Token:      speechAccessToken,
					ResourceID: volcengineTTSResourceID,
				},
				VolcanoTTSParameters: string(nativeTTS),
			},
			Prefill: true,
		},
		SubtitleConfig: startSubtitleConfig{
			ServerMessageURL:       callbackURL,
			ServerMessageSignature: callbackSignature,
			DisableRTSSubtitle:     false,
			SubtitleMode:           0,
		},
		InterruptMode: 0,
	})
	if err != nil {
		return StartConfiguration{}, fmt.Errorf("encode volcengine RTC Config: %w", err)
	}

	agentConfig, err := json.Marshal(startAgentConfig{
		TargetUserIDs:                   []string{targetUserID},
		UserID:                          agentUserID,
		WelcomeMessage:                  strings.TrimSpace(input.WelcomeMessage),
		EnableConversationStateCallback: true,
		ServerMessageURLForRTS:          callbackURL,
		ServerMessageSignatureForRTS:    callbackSignature,
	})
	if err != nil {
		return StartConfiguration{}, fmt.Errorf("encode volcengine RTC AgentConfig: %w", err)
	}

	return StartConfiguration{
		Config:      config,
		AgentConfig: agentConfig,
	}, nil
}

func validatePublicHTTPSURL(value string) error {
	publicURL, err := url.Parse(value)
	if err != nil ||
		publicURL.Scheme != "https" ||
		publicURL.Host == "" ||
		publicURL.User != nil ||
		publicURL.Fragment != "" {
		return errors.New("must be a public HTTPS URL without credentials or a fragment")
	}
	return nil
}

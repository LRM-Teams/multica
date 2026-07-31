package volcenginertc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	volcengineASRResourceID     = "volc.seedasr.sauc.duration"
	volcengineTTSResourceID     = "seed-tts-2.0"
	FunctionCallbackRoomIDQuery = "voice_call_room_id"
	VoiceAgentToolName          = "delegate_work_to_multica_agent"
	voiceAgentToolDescription   = "" +
		"当用户要求创建或修改 issue、执行开发工作、安排或推进任务、检查项目状态，" +
		"或做任何需要 Multica 真实工具和权限的操作时调用。" +
		"普通闲聊、解释和无需改变项目状态的回答不要调用。"
	voiceAgentRequestDescription = "完整保留用户要执行的任务、对象、约束和验收要求，使用中文。"
)

func BuildFunctionCallbackURL(callbackURL, roomID string) (string, error) {
	callbackURL = strings.TrimSpace(callbackURL)
	if err := validatePublicHTTPSURL(callbackURL); err != nil {
		return "", fmt.Errorf("volcengine RTC callback URL: %w", err)
	}
	roomID = strings.TrimSpace(roomID)
	if err := validateRoomTokenID("RoomId", roomID); err != nil {
		return "", err
	}
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return "", fmt.Errorf("parse volcengine RTC callback URL: %w", err)
	}
	query := parsed.Query()
	query.Set(FunctionCallbackRoomIDQuery, roomID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type StartConfigurationInput struct {
	TargetUserID        string
	AgentUserID         string
	WelcomeMessage      string
	SystemMessages      []string
	ArkEndpointID       string
	ASRAppID            string
	TTSAppID            string
	SpeechAccessToken   string
	TTSVoiceID          string
	CallbackURL         string
	FunctionCallbackURL string
	CallbackSignature   string
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
	Mode            string `json:"Mode"`
	AppID           string `json:"AppId"`
	AccessToken     string `json:"AccessToken"`
	APIResourceID   string `json:"ApiResourceId"`
	StreamMode      int    `json:"StreamMode"`
	EnableNonstream bool   `json:"enable_nonstream"`
}

type startVADConfig struct {
	SilenceTime int  `json:"SilenceTime"`
	AIVAD       bool `json:"AIVAD"`
}

type startLLMConfig struct {
	Mode           string         `json:"Mode"`
	EndpointID     string         `json:"EndPointId"`
	SystemMessages []string       `json:"SystemMessages"`
	HistoryLength  int            `json:"HistoryLength"`
	MaxTokens      int            `json:"MaxTokens"`
	ThinkingType   string         `json:"ThinkingType"`
	Prefill        bool           `json:"Prefill"`
	Tools          []startLLMTool `json:"Tools"`
}

type startLLMTool struct {
	Type     string               `json:"type"`
	Function startLLMToolFunction `json:"function"`
}

type startLLMToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  startLLMToolParameters `json:"parameters"`
}

type startLLMToolParameters struct {
	Type                 string                          `json:"type"`
	Properties           startLLMToolParameterProperties `json:"properties"`
	Required             []string                        `json:"required"`
	AdditionalProperties bool                            `json:"additionalProperties"`
}

type startLLMToolParameterProperties struct {
	Request startLLMToolStringParameter `json:"request"`
}

type startLLMToolStringParameter struct {
	Type        string `json:"type"`
	Description string `json:"description"`
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

type startFunctionCallingConfig struct {
	ServerMessageURL       string `json:"ServerMessageUrl"`
	ServerMessageSignature string `json:"ServerMessageSignature"`
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
	ASRConfig             startASRConfig             `json:"ASRConfig"`
	LLMConfig             startLLMConfig             `json:"LLMConfig"`
	TTSConfig             startTTSConfig             `json:"TTSConfig"`
	SubtitleConfig        startSubtitleConfig        `json:"SubtitleConfig"`
	FunctionCallingConfig startFunctionCallingConfig `json:"FunctionCallingConfig"`
	InterruptMode         int                        `json:"InterruptMode"`
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
	functionCallbackURL := strings.TrimSpace(input.FunctionCallbackURL)
	if functionCallbackURL == "" {
		functionCallbackURL = callbackURL
	}
	if err := validatePublicHTTPSURL(functionCallbackURL); err != nil {
		return StartConfiguration{}, fmt.Errorf(
			"volcengine RTC function callback URL: %w",
			err,
		)
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
				Mode:            "bigmodel",
				AppID:           asrAppID,
				AccessToken:     speechAccessToken,
				APIResourceID:   volcengineASRResourceID,
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
			Tools: []startLLMTool{{
				Type: "function",
				Function: startLLMToolFunction{
					Name:        VoiceAgentToolName,
					Description: voiceAgentToolDescription,
					Parameters: startLLMToolParameters{
						Type: "object",
						Properties: startLLMToolParameterProperties{
							Request: startLLMToolStringParameter{
								Type:        "string",
								Description: voiceAgentRequestDescription,
							},
						},
						Required:             []string{"request"},
						AdditionalProperties: false,
					},
				},
			}},
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
		FunctionCallingConfig: startFunctionCallingConfig{
			ServerMessageURL:       functionCallbackURL,
			ServerMessageSignature: callbackSignature,
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

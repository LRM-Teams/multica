package volcenginertc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildStartConfigurationMatchesCurrentVolcengineContract(t *testing.T) {
	configuration, err := BuildStartConfiguration(StartConfigurationInput{
		TargetUserID:      "member-1",
		AgentUserID:       "beckham-call-1",
		VoiceCallID:       "call-20260724",
		WelcomeMessage:    "你好，我是贝克汉姆。",
		SystemMessages:    []string{"You are Beckham.", "Keep spoken replies concise."},
		CustomLLMURL:      "https://multica.example.com/api/voice-calls/llm",
		CustomLLMAPIKey:   "llm-secret",
		TTSVoiceID:        "zh_male_m191_uranus_bigtts",
		CallbackURL:       "https://multica.example.com/api/voice-calls/callback",
		CallbackSignature: "callback-secret",
	})
	if err != nil {
		t.Fatalf("build start configuration: %v", err)
	}

	const wantConfig = `{"ASRConfig":{"Provider":"volcano","ProviderParams":{"Mode":"bigmodel","StreamMode":2,"ApiResourceId":"volc.seedasr.sauc.duration","enable_nonstream":true},"TurnDetectionMode":0,"VADConfig":{"SilenceTime":600,"AIVAD":true}},"LLMConfig":{"Mode":"CustomLLM","Url":"https://multica.example.com/api/voice-calls/llm","APIKey":"llm-secret","ModelName":"multica-beckham","SystemMessages":["You are Beckham.","Keep spoken replies concise."],"HistoryLength":10,"Prefill":false,"EnableRoundId":true,"Custom":"{\"voice_call_id\":\"call-20260724\"}"},"TTSConfig":{"AutoActive":true,"Provider":"volcano_bidirection","ProviderParams":{"Credential":{"ResourceId":"seed-tts-2.0"},"VolcanoTTSParameters":"{\"req_params\":{\"speaker\":\"zh_male_m191_uranus_bigtts\"}}"},"Prefill":true},"SubtitleConfig":{"ServerMessageUrl":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignature":"callback-secret","DisableRTSSubtitle":false,"SubtitleMode":0},"InterruptMode":0}`
	if string(configuration.Config) != wantConfig {
		t.Fatalf("Config = %s, want %s", configuration.Config, wantConfig)
	}
	const wantAgentConfig = `{"TargetUserId":["member-1"],"UserId":"beckham-call-1","WelcomeMessage":"你好，我是贝克汉姆。","EnableConversationStateCallback":true,"ServerMessageURLForRTS":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignatureForRTS":"callback-secret"}`
	if string(configuration.AgentConfig) != wantAgentConfig {
		t.Fatalf("AgentConfig = %s, want %s", configuration.AgentConfig, wantAgentConfig)
	}
}

func TestBuildStartConfigurationScopesCustomLLMToVoiceCall(t *testing.T) {
	configuration, err := BuildStartConfiguration(validStartConfigurationInput())
	if err != nil {
		t.Fatalf("build custom LLM configuration: %v", err)
	}
	var config struct {
		LLMConfig struct {
			Mode          string `json:"Mode"`
			URL           string `json:"Url"`
			APIKey        string `json:"APIKey"`
			Custom        string `json:"Custom"`
			EnableRoundID bool   `json:"EnableRoundId"`
		} `json:"LLMConfig"`
	}
	if err := json.Unmarshal(configuration.Config, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if config.LLMConfig.Mode != "CustomLLM" ||
		config.LLMConfig.URL != "https://multica.example.com/api/voice-calls/llm" ||
		config.LLMConfig.APIKey != "llm-secret" ||
		config.LLMConfig.Custom != `{"voice_call_id":"call-1"}` ||
		!config.LLMConfig.EnableRoundID {
		t.Fatalf("LLMConfig = %+v", config.LLMConfig)
	}
}

func TestBuildStartConfigurationRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*StartConfigurationInput)
		want   string
	}{
		{
			name:   "missing target user",
			mutate: func(input *StartConfigurationInput) { input.TargetUserID = "" },
			want:   "TargetUserId",
		},
		{
			name:   "invalid agent user",
			mutate: func(input *StartConfigurationInput) { input.AgentUserID = "beckham/call" },
			want:   "Agent UserId",
		},
		{
			name: "same participants",
			mutate: func(input *StartConfigurationInput) {
				input.AgentUserID = input.TargetUserID
			},
			want: "must differ",
		},
		{
			name:   "missing call ID",
			mutate: func(input *StartConfigurationInput) { input.VoiceCallID = "" },
			want:   "voice call ID",
		},
		{
			name: "insecure CustomLLM URL",
			mutate: func(input *StartConfigurationInput) {
				input.CustomLLMURL = "http://multica.example.com/llm"
			},
			want: "CustomLLM URL",
		},
		{
			name:   "missing CustomLLM API key",
			mutate: func(input *StartConfigurationInput) { input.CustomLLMAPIKey = "" },
			want:   "CustomLLM API key",
		},
		{
			name:   "empty system messages",
			mutate: func(input *StartConfigurationInput) { input.SystemMessages = nil },
			want:   "SystemMessages",
		},
		{
			name:   "blank system message",
			mutate: func(input *StartConfigurationInput) { input.SystemMessages = []string{" "} },
			want:   "SystemMessages",
		},
		{
			name:   "missing voice",
			mutate: func(input *StartConfigurationInput) { input.TTSVoiceID = "" },
			want:   "TTS voice",
		},
		{
			name: "insecure callback",
			mutate: func(input *StartConfigurationInput) {
				input.CallbackURL = "http://multica.example.com/callback"
			},
			want: "HTTPS",
		},
		{
			name:   "missing callback signature",
			mutate: func(input *StartConfigurationInput) { input.CallbackSignature = "" },
			want:   "callback signature",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := validStartConfigurationInput()
			testCase.mutate(&input)

			_, err := BuildStartConfiguration(input)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func validStartConfigurationInput() StartConfigurationInput {
	return StartConfigurationInput{
		TargetUserID:      "member-1",
		AgentUserID:       "beckham-call-1",
		VoiceCallID:       "call-1",
		SystemMessages:    []string{"You are Beckham."},
		CustomLLMURL:      "https://multica.example.com/api/voice-calls/llm",
		CustomLLMAPIKey:   "llm-secret",
		TTSVoiceID:        "zh_male_m191_uranus_bigtts",
		CallbackURL:       "https://multica.example.com/api/voice-calls/callback",
		CallbackSignature: "callback-secret",
	}
}

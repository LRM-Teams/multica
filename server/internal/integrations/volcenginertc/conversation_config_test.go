package volcenginertc

import (
	"strings"
	"testing"
)

func TestBuildStartConfigurationMatchesCurrentVolcengineContract(t *testing.T) {
	configuration, err := BuildStartConfiguration(StartConfigurationInput{
		TargetUserID:      "member-1",
		AgentUserID:       "beckham-call-1",
		WelcomeMessage:    "你好，我是贝克汉姆。",
		SystemMessages:    []string{"You are Beckham.", "Keep spoken replies concise."},
		ArkEndpointID:     "ep-20260723",
		TTSVoiceID:        "zh_male_m191_uranus_bigtts",
		CallbackURL:       "https://multica.example.com/api/voice-calls/callback",
		CallbackSignature: "callback-secret",
	})
	if err != nil {
		t.Fatalf("build start configuration: %v", err)
	}

	const wantConfig = `{"ASRConfig":{"Provider":"volcano","ProviderParams":{"Mode":"bigmodel","StreamMode":2,"ApiResourceId":"volc.seedasr.sauc.duration","enable_nonstream":true},"TurnDetectionMode":0,"VADConfig":{"SilenceTime":600,"AIVAD":true}},"LLMConfig":{"Mode":"ArkV3","EndPointId":"ep-20260723","SystemMessages":["You are Beckham.","Keep spoken replies concise."],"HistoryLength":10,"MaxTokens":256,"ThinkingType":"disabled","Prefill":false},"TTSConfig":{"AutoActive":true,"Provider":"volcano_bidirection","ProviderParams":{"Credential":{"ResourceId":"seed-tts-2.0"},"VolcanoTTSParameters":"{\"req_params\":{\"speaker\":\"zh_male_m191_uranus_bigtts\"}}"},"Prefill":true},"SubtitleConfig":{"ServerMessageUrl":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignature":"callback-secret","DisableRTSSubtitle":false,"SubtitleMode":0},"InterruptMode":0}`
	if string(configuration.Config) != wantConfig {
		t.Fatalf("Config = %s, want %s", configuration.Config, wantConfig)
	}
	const wantAgentConfig = `{"TargetUserId":["member-1"],"UserId":"beckham-call-1","WelcomeMessage":"你好，我是贝克汉姆。","EnableConversationStateCallback":true,"ServerMessageURLForRTS":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignatureForRTS":"callback-secret"}`
	if string(configuration.AgentConfig) != wantAgentConfig {
		t.Fatalf("AgentConfig = %s, want %s", configuration.AgentConfig, wantAgentConfig)
	}
}

func TestBuildStartConfigurationSupportsArkModelName(t *testing.T) {
	input := validStartConfigurationInput()
	input.ArkEndpointID = ""
	input.ArkModelName = "Doubao-Seed-1.6｜250615"

	configuration, err := BuildStartConfiguration(input)
	if err != nil {
		t.Fatalf("build Ark model configuration: %v", err)
	}
	config := string(configuration.Config)
	if !strings.Contains(config, `"Mode":"ArkV3"`) ||
		!strings.Contains(config, `"ModelName":"Doubao-Seed-1.6｜250615"`) ||
		strings.Contains(config, `"EndPointId"`) {
		t.Fatalf("Config = %s", config)
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
			name:   "missing Ark model selection",
			mutate: func(input *StartConfigurationInput) { input.ArkEndpointID = "" },
			want:   "exactly one Ark endpoint ID or model name",
		},
		{
			name: "ambiguous Ark model selection",
			mutate: func(input *StartConfigurationInput) {
				input.ArkModelName = "Doubao-Seed-1.6｜250615"
			},
			want: "exactly one Ark endpoint ID or model name",
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
		SystemMessages:    []string{"You are Beckham."},
		ArkEndpointID:     "ep-20260723",
		TTSVoiceID:        "zh_male_m191_uranus_bigtts",
		CallbackURL:       "https://multica.example.com/api/voice-calls/callback",
		CallbackSignature: "callback-secret",
	}
}

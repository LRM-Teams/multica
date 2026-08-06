package volcenginertc

import (
	"strings"
	"testing"
)

func TestBuildStartConfigurationMatchesCurrentVolcengineContract(t *testing.T) {
	configuration, err := BuildStartConfiguration(StartConfigurationInput{
		TargetUserID:        "member-1",
		AgentUserID:         "beckham-call-1",
		WelcomeMessage:      "你好，我是贝克汉姆。",
		SystemMessages:      []string{"You are Beckham.", "Keep spoken replies concise."},
		ArkEndpointID:       "ep-20260723",
		ASRAppID:            "speech-asr-app",
		TTSAppID:            "speech-tts-app",
		SpeechAccessToken:   "speech-access-token",
		TTSVoiceID:          "zh_male_m191_uranus_bigtts",
		CallbackURL:         "https://multica.example.com/api/voice-calls/callback",
		FunctionCallbackURL: "https://multica.example.com/api/voice-calls/callback?voice_call_room_id=voice-call-1",
		CallbackSignature:   "callback-secret",
	})
	if err != nil {
		t.Fatalf("build start configuration: %v", err)
	}

	const wantConfig = `{"ASRConfig":{"Provider":"volcano","ProviderParams":{"Mode":"bigmodel","AppId":"speech-asr-app","AccessToken":"speech-access-token","ApiResourceId":"volc.seedasr.sauc.duration","StreamMode":2,"enable_nonstream":true},"TurnDetectionMode":0,"VADConfig":{"SilenceTime":600,"AIVAD":true}},"LLMConfig":{"Mode":"ArkV3","EndPointId":"ep-20260723","SystemMessages":["You are Beckham.","Keep spoken replies concise."],"HistoryLength":10,"MaxTokens":256,"ThinkingType":"disabled","Prefill":false,"Tools":[{"type":"function","function":{"name":"delegate_work_to_multica_agent","description":"当用户要求创建或修改 issue、执行开发工作、安排或推进任务、检查项目状态，或做任何需要 Multica 真实工具和权限的操作时调用。普通闲聊、解释和无需改变项目状态的回答不要调用。","parameters":{"type":"object","properties":{"request":{"type":"string","description":"完整保留用户要执行的任务、对象、约束和验收要求，使用中文。"}},"required":["request"],"additionalProperties":false}}}]},"TTSConfig":{"AutoActive":true,"Provider":"volcano_bidirection","ProviderParams":{"Credential":{"AppId":"speech-tts-app","Token":"speech-access-token","ResourceId":"seed-tts-2.0"},"VolcanoTTSParameters":"{\"req_params\":{\"speaker\":\"zh_male_m191_uranus_bigtts\"}}"},"Prefill":true},"SubtitleConfig":{"ServerMessageUrl":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignature":"callback-secret","DisableRTSSubtitle":false,"SubtitleMode":0},"FunctionCallingConfig":{"ServerMessageUrl":"https://multica.example.com/api/voice-calls/callback?voice_call_room_id=voice-call-1","ServerMessageSignature":"callback-secret"},"InterruptMode":0}`
	if string(configuration.Config) != wantConfig {
		t.Fatalf("Config = %s, want %s", configuration.Config, wantConfig)
	}
	const wantAgentConfig = `{"TargetUserId":["member-1"],"UserId":"beckham-call-1","WelcomeMessage":"你好，我是贝克汉姆。","EnableConversationStateCallback":true,"ServerMessageURLForRTS":"https://multica.example.com/api/voice-calls/callback","ServerMessageSignatureForRTS":"callback-secret"}`
	if string(configuration.AgentConfig) != wantAgentConfig {
		t.Fatalf("AgentConfig = %s, want %s", configuration.AgentConfig, wantAgentConfig)
	}
}

func TestBuildStartConfigurationRequiresArkEndpoint(t *testing.T) {
	input := validStartConfigurationInput()
	input.ArkEndpointID = ""

	_, err := BuildStartConfiguration(input)
	if err == nil || !strings.Contains(err.Error(), "endpoint ID") {
		t.Fatalf("error = %v, want missing Ark endpoint ID", err)
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
			name:   "missing Ark endpoint",
			mutate: func(input *StartConfigurationInput) { input.ArkEndpointID = "" },
			want:   "endpoint ID",
		},
		{
			name:   "missing ASR AppId",
			mutate: func(input *StartConfigurationInput) { input.ASRAppID = "" },
			want:   "ASR AppId",
		},
		{
			name:   "missing TTS AppId",
			mutate: func(input *StartConfigurationInput) { input.TTSAppID = "" },
			want:   "TTS AppId",
		},
		{
			name: "missing speech access token",
			mutate: func(input *StartConfigurationInput) {
				input.SpeechAccessToken = ""
			},
			want: "speech access token",
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
		ASRAppID:          "speech-asr-app",
		TTSAppID:          "speech-tts-app",
		SpeechAccessToken: "speech-access-token",
		TTSVoiceID:        "zh_male_m191_uranus_bigtts",
		CallbackURL:       "https://multica.example.com/api/voice-calls/callback",
		CallbackSignature: "callback-secret",
	}
}

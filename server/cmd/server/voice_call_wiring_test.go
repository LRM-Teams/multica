package main

import (
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/realtime"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const testVoiceCallAppID = "123456789012345678901234"

func TestLoadVoiceCallRuntimeConfigKeepsUnconfiguredDeploymentsDisabled(t *testing.T) {
	config, enabled, err := loadVoiceCallRuntimeConfig(func(string) string { return "" })
	if err != nil {
		t.Fatalf("load disabled voice call config: %v", err)
	}
	if enabled {
		t.Fatal("voice calling enabled without an explicit VOLCENGINE_RTC_* setting")
	}
	if config != (voiceCallRuntimeConfig{}) {
		t.Fatalf("disabled voice call config = %#v, want zero value", config)
	}
}

func TestLoadVoiceCallRuntimeConfigRejectsPartialOptIn(t *testing.T) {
	values := map[string]string{
		"VOLCENGINE_RTC_APP_ID": testVoiceCallAppID,
	}
	_, enabled, err := loadVoiceCallRuntimeConfig(func(name string) string {
		return values[name]
	})
	if !enabled {
		t.Fatal("explicit RTC AppID did not opt in to voice calling")
	}
	if err == nil || !strings.Contains(err.Error(), "access key id") {
		t.Fatalf("partial voice call config error = %v, want missing access key id", err)
	}
	if strings.Contains(err.Error(), testVoiceCallAppID) {
		t.Fatalf("configuration error leaked a credential value: %v", err)
	}
}

func TestLoadVoiceCallRuntimeConfigDerivesCallbackAndReusesSpeechVoice(t *testing.T) {
	values := completeVoiceCallEnvironment()
	delete(values, "VOLCENGINE_RTC_CALLBACK_URL")
	delete(values, "VOLCENGINE_RTC_TTS_VOICE_ID")
	values["MULTICA_PUBLIC_URL"] = "https://multica.example.com/"
	values["DOUBAO_TTS_SPEAKER_ID"] = "shared-tts-voice"
	values["VOLCENGINE_RTC_TOKEN_TTL"] = "45m"
	values["VOLCENGINE_RTC_COMPENSATION_TIMEOUT"] = "12s"
	values["VOLCENGINE_RTC_CLEANUP_TIMEOUT"] = "14s"

	config, enabled, err := loadVoiceCallRuntimeConfig(func(name string) string {
		return values[name]
	})
	if err != nil {
		t.Fatalf("load complete voice call config: %v", err)
	}
	if !enabled {
		t.Fatal("complete voice call config was disabled")
	}
	if config.CallbackURL != "https://multica.example.com/api/voice-calls/callback" {
		t.Fatalf("callback URL = %q", config.CallbackURL)
	}
	if config.ArkEndpointID != "ep-voice-call" || config.ArkModelName != "" {
		t.Fatalf(
			"Ark selection = endpoint %q model %q",
			config.ArkEndpointID,
			config.ArkModelName,
		)
	}
	if config.TTSVoiceID != "shared-tts-voice" {
		t.Fatalf("TTS voice = %q, want shared speech voice", config.TTSVoiceID)
	}
	if config.APIVersion != "2025-06-01" {
		t.Fatalf("API version = %q, want current default", config.APIVersion)
	}
	if config.TokenTTL != 45*time.Minute ||
		config.CompensationTimeout != 12*time.Second ||
		config.CleanupTimeout != 14*time.Second {
		t.Fatalf(
			"durations = %s/%s/%s",
			config.TokenTTL,
			config.CompensationTimeout,
			config.CleanupTimeout,
		)
	}
}

func TestLoadVoiceCallRuntimeConfigPreservesExplicitAPIVersion(t *testing.T) {
	values := completeVoiceCallEnvironment()
	values["VOLCENGINE_RTC_API_VERSION"] = "2024-12-01"

	config, enabled, err := loadVoiceCallRuntimeConfig(func(name string) string {
		return values[name]
	})
	if err != nil {
		t.Fatalf("load voice call config: %v", err)
	}
	if !enabled {
		t.Fatal("complete voice call config was disabled")
	}
	if config.APIVersion != "2024-12-01" {
		t.Fatalf("API version = %q", config.APIVersion)
	}
}

func TestLoadVoiceCallRuntimeConfigRejectsInvalidDurationAndArkSelection(t *testing.T) {
	t.Run("duration", func(t *testing.T) {
		values := completeVoiceCallEnvironment()
		values["VOLCENGINE_RTC_TOKEN_TTL"] = "forever"
		_, _, err := loadVoiceCallRuntimeConfig(func(name string) string {
			return values[name]
		})
		if err == nil || !strings.Contains(err.Error(), "VOLCENGINE_RTC_TOKEN_TTL") {
			t.Fatalf("invalid duration error = %v", err)
		}
	})

	t.Run("missing Ark selection", func(t *testing.T) {
		values := completeVoiceCallEnvironment()
		delete(values, "VOLCENGINE_RTC_ARK_ENDPOINT_ID")
		_, _, err := loadVoiceCallRuntimeConfig(func(name string) string {
			return values[name]
		})
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("missing Ark selection error = %v", err)
		}
	})

	t.Run("ambiguous Ark selection", func(t *testing.T) {
		values := completeVoiceCallEnvironment()
		values["VOLCENGINE_RTC_ARK_MODEL_NAME"] = "Doubao-Seed-1.6｜250615"
		_, _, err := loadVoiceCallRuntimeConfig(func(name string) string {
			return values[name]
		})
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("ambiguous Ark selection error = %v", err)
		}
	})
}

func TestConfigureVoiceCallServiceBuildsCompleteRuntimeStack(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	values := completeVoiceCallEnvironment()
	for _, name := range voiceCallEnvironmentNames {
		t.Setenv(name, "")
	}
	_, handler := NewRouterWithOptions(
		testPool,
		realtime.NewHub(),
		events.New(),
		analytics.NoopClient{},
		nil,
		RouterOptions{},
	)
	if handler.VoiceCallService != nil ||
		handler.VoiceCallCallbackProcessor != nil ||
		handler.VoiceCallCallbackSignature != "" ||
		handler.VoiceCallLLMProcessor != nil ||
		handler.VoiceCallLLMAPIKey != "" {
		t.Fatal("voice call runtime unexpectedly configured before explicit opt-in")
	}

	err := configureVoiceCallService(
		handler,
		db.New(testPool),
		func(name string) string { return values[name] },
	)
	if err != nil {
		t.Fatalf("configure voice call service: %v", err)
	}
	if handler.VoiceCallService == nil {
		t.Fatal("complete voice call runtime stack was not attached to handler")
	}
	if handler.VoiceCallCallbackProcessor == nil {
		t.Fatal("voice call callback processor was not attached to handler")
	}
	if handler.VoiceCallCallbackSignature != values["VOLCENGINE_RTC_CALLBACK_SIGNATURE"] {
		t.Fatal("voice call callback signature was not attached to handler")
	}
	if handler.VoiceCallLLMAPIKey != "" || handler.VoiceCallLLMProcessor != nil {
		t.Fatal("obsolete CustomLLM bridge was attached to direct Ark runtime")
	}
}

func completeVoiceCallEnvironment() map[string]string {
	return map[string]string{
		"VOLCENGINE_RTC_APP_ID":               testVoiceCallAppID,
		"VOLCENGINE_RTC_APP_KEY":              "app-key",
		"VOLCENGINE_RTC_ACCESS_KEY_ID":        "access-key-id",
		"VOLCENGINE_RTC_SECRET_ACCESS_KEY":    "secret-access-key",
		"VOLCENGINE_RTC_ARK_ENDPOINT_ID":      "ep-voice-call",
		"VOLCENGINE_RTC_TTS_VOICE_ID":         "voice-id",
		"VOLCENGINE_RTC_CALLBACK_URL":         "https://multica.example.com/api/voice-calls/callback",
		"VOLCENGINE_RTC_CALLBACK_SIGNATURE":   "callback-signature",
		"VOLCENGINE_RTC_TOKEN_TTL":            "30m",
		"VOLCENGINE_RTC_COMPENSATION_TIMEOUT": "10s",
		"VOLCENGINE_RTC_CLEANUP_TIMEOUT":      "10s",
	}
}

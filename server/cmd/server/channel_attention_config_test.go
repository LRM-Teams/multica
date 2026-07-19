package main

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

var channelAttentionEnvNames = []string{
	"CHANNEL_UNMENTIONED_MODE",
	"CHANNEL_ATTENTION_ENABLED",
	"CHANNEL_ATTENTION_DEBOUNCE",
	"CHANNEL_ATTENTION_MAX_WAIT",
	"CHANNEL_ATTENTION_CONTEXT_MESSAGES",
	"CHANNEL_ATTENTION_MEMORY_BUDGET",
	"CHANNEL_ATTENTION_MAX_OUTPUT_TOKENS",
	"CHANNEL_ATTENTION_TOOLS_ENABLED",
	"CHANNEL_ATTENTION_MAX_CONCURRENT_PER_RUNTIME",
}

func TestChannelAttentionConfigDefaults(t *testing.T) {
	clearChannelAttentionEnv(t)

	var cfg handler.Config
	applyChannelAttentionConfigFromEnv(&cfg)

	assertChannelAttentionConfig(t, cfg, handler.Config{
		ChannelUnmentionedMode:                  "attention_round",
		ChannelAttentionEnabled:                 true,
		ChannelAttentionDebounce:                3 * time.Second,
		ChannelAttentionMaxWait:                 8 * time.Second,
		ChannelAttentionContextMessages:         8,
		ChannelAttentionMemoryBudgetBytes:       4096,
		ChannelAttentionMaxOutputTokens:         96,
		ChannelAttentionToolsEnabled:            false,
		ChannelAttentionMaxConcurrentPerRuntime: 16,
	})
}

func TestChannelAttentionConfigOverridesAndLegacyRollback(t *testing.T) {
	t.Setenv("CHANNEL_UNMENTIONED_MODE", " LEGACY_FULL ")
	t.Setenv("CHANNEL_ATTENTION_ENABLED", "false")
	t.Setenv("CHANNEL_ATTENTION_DEBOUNCE", "2s")
	t.Setenv("CHANNEL_ATTENTION_MAX_WAIT", "12s")
	t.Setenv("CHANNEL_ATTENTION_CONTEXT_MESSAGES", "6")
	t.Setenv("CHANNEL_ATTENTION_MEMORY_BUDGET", "4KiB")
	t.Setenv("CHANNEL_ATTENTION_MAX_OUTPUT_TOKENS", "80")
	t.Setenv("CHANNEL_ATTENTION_TOOLS_ENABLED", "true")
	t.Setenv("CHANNEL_ATTENTION_MAX_CONCURRENT_PER_RUNTIME", "7")

	var cfg handler.Config
	applyChannelAttentionConfigFromEnv(&cfg)

	assertChannelAttentionConfig(t, cfg, handler.Config{
		ChannelUnmentionedMode:                  "legacy_full",
		ChannelAttentionEnabled:                 false,
		ChannelAttentionDebounce:                2 * time.Second,
		ChannelAttentionMaxWait:                 8 * time.Second,
		ChannelAttentionContextMessages:         6,
		ChannelAttentionMemoryBudgetBytes:       4096,
		ChannelAttentionMaxOutputTokens:         80,
		ChannelAttentionToolsEnabled:            false,
		ChannelAttentionMaxConcurrentPerRuntime: 7,
	})
}

func TestChannelAttentionConfigBoundsRestrictedProbeBudgets(t *testing.T) {
	clearChannelAttentionEnv(t)
	t.Setenv("CHANNEL_ATTENTION_DEBOUNCE", "1250ms")
	t.Setenv("CHANNEL_ATTENTION_MAX_WAIT", "1500ms")
	t.Setenv("CHANNEL_ATTENTION_MAX_OUTPUT_TOKENS", "512")
	t.Setenv("CHANNEL_ATTENTION_CONTEXT_MESSAGES", "80")
	t.Setenv("CHANNEL_ATTENTION_MEMORY_BUDGET", "64KiB")
	t.Setenv("CHANNEL_ATTENTION_MAX_CONCURRENT_PER_RUNTIME", "160")

	var cfg handler.Config
	applyChannelAttentionConfigFromEnv(&cfg)

	if cfg.ChannelAttentionDebounce != 2*time.Second {
		t.Fatalf("debounce = %s, want lower bound 2s", cfg.ChannelAttentionDebounce)
	}
	if cfg.ChannelAttentionMaxWait != 8*time.Second {
		t.Fatalf("max wait = %s, want safe default 8s when not greater than debounce", cfg.ChannelAttentionMaxWait)
	}
	if cfg.ChannelAttentionMaxOutputTokens != 96 {
		t.Fatalf("max output tokens = %d, want upper bound 96", cfg.ChannelAttentionMaxOutputTokens)
	}
	if cfg.ChannelAttentionContextMessages != 8 || cfg.ChannelAttentionMemoryBudgetBytes != 4096 || cfg.ChannelAttentionMaxConcurrentPerRuntime != 16 {
		t.Fatalf("restricted bounds = context:%d memory:%d concurrency:%d", cfg.ChannelAttentionContextMessages, cfg.ChannelAttentionMemoryBudgetBytes, cfg.ChannelAttentionMaxConcurrentPerRuntime)
	}

	t.Setenv("CHANNEL_ATTENTION_DEBOUNCE", "30s")
	t.Setenv("CHANNEL_ATTENTION_MAX_WAIT", "4s")
	applyChannelAttentionConfigFromEnv(&cfg)
	if cfg.ChannelAttentionDebounce != 5*time.Second {
		t.Fatalf("debounce = %s, want upper bound 5s", cfg.ChannelAttentionDebounce)
	}
	if cfg.ChannelAttentionMaxWait != 8*time.Second {
		t.Fatalf("max wait = %s, want safe default 8s when not greater than debounce", cfg.ChannelAttentionMaxWait)
	}
}

func TestChannelAttentionConfigInvalidValuesFailClosedToDefaults(t *testing.T) {
	for _, name := range channelAttentionEnvNames {
		t.Setenv(name, "invalid")
	}

	var cfg handler.Config
	applyChannelAttentionConfigFromEnv(&cfg)

	assertChannelAttentionConfig(t, cfg, handler.Config{
		ChannelUnmentionedMode:                  "attention_round",
		ChannelAttentionEnabled:                 true,
		ChannelAttentionDebounce:                3 * time.Second,
		ChannelAttentionMaxWait:                 8 * time.Second,
		ChannelAttentionContextMessages:         8,
		ChannelAttentionMemoryBudgetBytes:       4096,
		ChannelAttentionMaxOutputTokens:         96,
		ChannelAttentionToolsEnabled:            false,
		ChannelAttentionMaxConcurrentPerRuntime: 16,
	})
}

func clearChannelAttentionEnv(t *testing.T) {
	t.Helper()
	for _, name := range channelAttentionEnvNames {
		t.Setenv(name, "")
	}
}

func assertChannelAttentionConfig(t *testing.T, got, want handler.Config) {
	t.Helper()
	if got.ChannelUnmentionedMode != want.ChannelUnmentionedMode ||
		got.ChannelAttentionEnabled != want.ChannelAttentionEnabled ||
		got.ChannelAttentionDebounce != want.ChannelAttentionDebounce ||
		got.ChannelAttentionMaxWait != want.ChannelAttentionMaxWait ||
		got.ChannelAttentionContextMessages != want.ChannelAttentionContextMessages ||
		got.ChannelAttentionMemoryBudgetBytes != want.ChannelAttentionMemoryBudgetBytes ||
		got.ChannelAttentionMaxOutputTokens != want.ChannelAttentionMaxOutputTokens ||
		got.ChannelAttentionToolsEnabled != want.ChannelAttentionToolsEnabled ||
		got.ChannelAttentionMaxConcurrentPerRuntime != want.ChannelAttentionMaxConcurrentPerRuntime {
		t.Fatalf("channel attention config = %+v, want %+v", got, want)
	}
}

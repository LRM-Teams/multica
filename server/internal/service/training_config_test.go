// SPDX-License-Identifier: Apache-2.0

package service

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadTrainingConfig_DisabledByDefault(t *testing.T) {
	// Clear any existing env vars
	clearTrainingEnv(t)

	cfg := LoadTrainingConfig()
	assert.Empty(t, cfg.BridgeStubURL)
	assert.Empty(t, cfg.AdminAPIKey)
	assert.Equal(t, 1.0, cfg.DefaultReward)
	assert.Equal(t, "http://db_bridge_stub:9100/v1", cfg.ProxyURL)
}

func TestLoadTrainingConfig_Enabled(t *testing.T) {
	clearTrainingEnv(t)

	os.Setenv("AREAL_BRIDGE_STUB_URL", "http://localhost:9100/v1")
	os.Setenv("AREAL_ADMIN_API_KEY", "test-key-123")
	os.Setenv("TRAINING_DEFAULT_REWARD", "0.5")
	os.Setenv("AREAL_PROXY_URL", "http://custom-proxy:9100/v1")

	cfg := LoadTrainingConfig()
	assert.Equal(t, "http://localhost:9100/v1", cfg.BridgeStubURL)
	assert.Equal(t, "test-key-123", cfg.AdminAPIKey)
	assert.Equal(t, 0.5, cfg.DefaultReward)
	assert.Equal(t, "http://custom-proxy:9100/v1", cfg.ProxyURL)
}

func TestLoadTrainingConfig_InvalidDefaultReward(t *testing.T) {
	clearTrainingEnv(t)

	os.Setenv("AREAL_BRIDGE_STUB_URL", "http://localhost:9100/v1")
	os.Setenv("AREAL_ADMIN_API_KEY", "test-key-123")
	os.Setenv("TRAINING_DEFAULT_REWARD", "not-a-number")

	cfg := LoadTrainingConfig()
	assert.Equal(t, 1.0, cfg.DefaultReward) // Falls back to 1.0
}

func TestNewTrainingSessionDeps_NilWhenNotConfigured(t *testing.T) {
	clearTrainingEnv(t)

	// No BridgeStubURL
	cfg := TrainingConfig{AdminAPIKey: "test-key"}
	deps := NewTrainingSessionDeps(cfg, nil)
	assert.Nil(t, deps)

	// No AdminAPIKey
	cfg = TrainingConfig{BridgeStubURL: "http://localhost:9100/v1"}
	deps = NewTrainingSessionDeps(cfg, nil)
	assert.Nil(t, deps)

	// Neither
	cfg = TrainingConfig{}
	deps = NewTrainingSessionDeps(cfg, nil)
	assert.Nil(t, deps)
}

func TestNewTrainingSessionDeps_CreatedWhenConfigured(t *testing.T) {
	clearTrainingEnv(t)

	cfg := TrainingConfig{
		BridgeStubURL: "http://localhost:9100/v1",
		AdminAPIKey:   "test-key-123",
		DefaultReward: 0.75,
		ProxyURL:      "http://proxy:9100/v1",
	}

	// Note: We pass nil as db.Queries since we don't need a real DB for this test
	// The interfaces are satisfied by nil in this case
	deps := NewTrainingSessionDeps(cfg, nil)

	assert.NotNil(t, deps)
	assert.NotNil(t, deps.RL)
	assert.NotNil(t, deps.Closer)
	assert.Equal(t, "http://proxy:9100/v1", deps.ProxyURL)
	assert.Equal(t, 0.75, deps.DefaultReward)
}

func TestTaskService_WithTraining(t *testing.T) {
	svc := &TaskService{}
	assert.Nil(t, svc.Training)

	cfg := TrainingConfig{
		BridgeStubURL: "http://localhost:9100/v1",
		AdminAPIKey:   "test-key-123",
	}
	deps := NewTrainingSessionDeps(cfg, nil)

	result := svc.WithTraining(deps)
	assert.Same(t, svc, result) // Returns same instance for chaining
	assert.Same(t, deps, svc.Training)
}

func clearTrainingEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"AREAL_BRIDGE_STUB_URL",
		"AREAL_ADMIN_API_KEY",
		"TRAINING_DEFAULT_REWARD",
		"AREAL_PROXY_URL",
	}
	for _, env := range envVars {
		if err := os.Unsetenv(env); err != nil {
			t.Logf("Warning: Could not unset env var %s: %v", env, err)
		}
	}
}

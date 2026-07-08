// SPDX-License-Identifier: Apache-2.0

package service

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

// When config is missing but Queries is non-nil (the production path), deps
// MUST be non-nil with Lookup+Store set and RL/Closer nil — this lets the
// open-hook loud-error guard (training.go:161-166) fire for training targets
// instead of silently no-oping.
func TestNewTrainingSessionDeps_GuardDepsWhenConfigMissing(t *testing.T) {
	clearTrainingEnv(t)

	// db.New(nil) creates a non-nil *db.Queries (pointer is non-nil; methods
	// would panic on nil db, but we don't call them here).
	q := db.New(nil)

	// No BridgeStubURL
	cfg := TrainingConfig{AdminAPIKey: "test-key"}
	deps := NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps, "deps must be non-nil so the open-hook guard is reachable")
	assert.Nil(t, deps.RL, "RL must be nil when bridge config missing")
	assert.Nil(t, deps.Closer, "Closer must be nil when bridge config missing")
	assert.NotNil(t, deps.Lookup, "Lookup must be set so the guard can resolve training targets")
	assert.NotNil(t, deps.Store, "Store must be set")

	// No AdminAPIKey
	cfg = TrainingConfig{BridgeStubURL: "http://localhost:9100/v1"}
	deps = NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps)
	assert.Nil(t, deps.RL)
	assert.Nil(t, deps.Closer)
	assert.NotNil(t, deps.Lookup)

	// Neither
	cfg = TrainingConfig{}
	deps = NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps)
	assert.Nil(t, deps.RL)
	assert.Nil(t, deps.Closer)
	assert.NotNil(t, deps.Lookup)
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

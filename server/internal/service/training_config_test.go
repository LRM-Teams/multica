// SPDX-License-Identifier: Apache-2.0

package service

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	assert.True(t, cfg.InteractionDAGEnabled, "DAG recording defaults on for trained rollouts")
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
	assert.True(t, cfg.InteractionDAGEnabled)
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

// In the production path (q != nil) with config present, NewTrainingSessionDeps
// must wire a non-nil DAG whose enabled flag mirrors cfg.InteractionDAGEnabled.
// This is the U10 seam that makes segment-DAG recording active in prod; until
// wired, the recording hooks no-op (interaction_dag_seams.go gates on DAG==nil).
func TestNewTrainingSessionDeps_DAGWiredInProductionPath(t *testing.T) {
	clearTrainingEnv(t)

	q := db.New(nil)

	// Enabled (the default): DAG is non-nil and active.
	cfg := TrainingConfig{
		BridgeStubURL:         "http://localhost:9100/v1",
		AdminAPIKey:           "test-key-123",
		InteractionDAGEnabled: true,
	}
	deps := NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps)
	assert.NotNil(t, deps.DAG, "DAG must be wired in the config-present production path")
	assert.True(t, deps.DAG.Enabled())
	require.NotNil(t, deps.DAG.msgs, "production DAG must read local task messages")
	assert.Equal(t, q, deps.DAG.msgs)

	// Disabled: DAG is still wired (non-nil) but reports disabled.
	cfg.InteractionDAGEnabled = false
	deps = NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps.DAG)
	assert.False(t, deps.DAG.Enabled())
}

// Non-training env-dispatch records trajectories locally and must not depend
// on AReaL admin credentials. Production commonly configures the bridge URL
// for other services without an admin key; that guard path still needs a DAG
// with message access so completed sandbox tasks produce assembled segments.
func TestNewTrainingSessionDeps_LocalDAGWiredWithoutTrainingCredentials(t *testing.T) {
	clearTrainingEnv(t)

	q := db.New(nil)
	cfg := TrainingConfig{
		BridgeStubURL:         "http://localhost:9100/v1",
		InteractionDAGEnabled: true,
	}

	deps := NewTrainingSessionDeps(cfg, q)
	require.NotNil(t, deps)
	require.NotNil(t, deps.DAG, "local env-dispatch DAG must not require training credentials")
	assert.True(t, deps.DAG.Enabled())
	assert.Equal(t, q, deps.DAG.store)
	assert.Equal(t, q, deps.DAG.msgs)
	assert.Nil(t, deps.DAG.client, "local recording must not synthesize an AReaL client")
}

// INTERACTION_DAG_ENABLED unset -> defaults true; "false"/"0" -> false.
func TestLoadTrainingConfig_DAGEnabledDefaultAndDisable(t *testing.T) {
	clearTrainingEnv(t)

	cfg := LoadTrainingConfig()
	assert.True(t, cfg.InteractionDAGEnabled, "unset -> default true")

	os.Setenv("INTERACTION_DAG_ENABLED", "false")
	cfg = LoadTrainingConfig()
	assert.False(t, cfg.InteractionDAGEnabled)

	os.Setenv("INTERACTION_DAG_ENABLED", "0")
	cfg = LoadTrainingConfig()
	assert.False(t, cfg.InteractionDAGEnabled)

	os.Setenv("INTERACTION_DAG_ENABLED", "true")
	cfg = LoadTrainingConfig()
	assert.True(t, cfg.InteractionDAGEnabled)
}

func TestLoadTrainingConfig_DiagnosisRuntimeSettings(t *testing.T) {
	clearTrainingEnv(t)

	t.Setenv("DIAGNOSIS_AGENT_PATH", "/usr/local/bin/pi")
	t.Setenv("DIAGNOSIS_AGENT_MODEL", "anthropic/claude-sonnet-5")
	t.Setenv("DIAGNOSIS_AGENT_TIMEOUT_SECONDS", "120")
	t.Setenv("DIAGNOSIS_AGENT_SCORE_MAX", "20")

	cfg := LoadTrainingConfig()
	assert.Equal(t, "/usr/local/bin/pi", cfg.DiagnosisAgentPath)
	assert.Equal(t, "anthropic/claude-sonnet-5", cfg.DiagnosisAgentModel)
	assert.Equal(t, 120*time.Second, cfg.DiagnosisAgentTimeout)
	assert.Equal(t, 20, cfg.DiagnosisAgentScoreMax)
}

func TestLoadTrainingConfig_DiagnosisInvalidTimeoutAndScoreMax(t *testing.T) {
	clearTrainingEnv(t)

	os.Setenv("DIAGNOSIS_AGENT_TIMEOUT_SECONDS", "not-a-number")
	os.Setenv("DIAGNOSIS_AGENT_SCORE_MAX", "oops")

	cfg := LoadTrainingConfig()
	assert.Equal(t, 60*time.Second, cfg.DiagnosisAgentTimeout, "invalid timeout falls back to 60s")
	assert.Equal(t, 10, cfg.DiagnosisAgentScoreMax, "invalid score max falls back to 10")
}

// Quickstart Scenario 6: sandbox is the default execution mode; "server" is
// accepted as the deprecated fallback; anything else falls back to sandbox.
func TestLoadTrainingConfig_DiagnosisExecutionMode(t *testing.T) {
	clearTrainingEnv(t)

	cfg := LoadTrainingConfig()
	assert.Equal(t, "sandbox", cfg.DiagnosisExecutionMode, "default execution mode is sandbox")

	t.Setenv("DIAGNOSIS_EXECUTION_MODE", "server")
	cfg = LoadTrainingConfig()
	assert.Equal(t, "server", cfg.DiagnosisExecutionMode, "deprecated server fallback remains selectable")

	t.Setenv("DIAGNOSIS_EXECUTION_MODE", "bogus")
	cfg = LoadTrainingConfig()
	assert.Equal(t, "sandbox", cfg.DiagnosisExecutionMode, "invalid mode falls back to sandbox")
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
		"INTERACTION_DAG_ENABLED",
		"DIAGNOSIS_AGENT_PATH",
		"DIAGNOSIS_AGENT_MODEL",
		"DIAGNOSIS_AGENT_TIMEOUT_SECONDS",
		"DIAGNOSIS_AGENT_SCORE_MAX",
		"DIAGNOSIS_EXECUTION_MODE",
	}
	for _, env := range envVars {
		if err := os.Unsetenv(env); err != nil {
			t.Logf("Warning: Could not unset env var %s: %v", env, err)
		}
	}
}

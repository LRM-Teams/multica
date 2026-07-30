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

// DIAGNOSIS_AGENT_* configure the Pi diagnosis agent that scores each LLM
// output at root-task terminal. Off by default; the trigger (training.go)
// additionally gates on the interaction DAG being enabled + configured, so a
// non-nil Diagnoser is only wired in the full bridge+DAG production path.

func TestLoadTrainingConfig_DiagnosisDisabledByDefault(t *testing.T) {
	clearTrainingEnv(t)

	cfg := LoadTrainingConfig()
	assert.False(t, cfg.DiagnosisAgentEnabled, "diagnosis defaults off")
	// Defaults are populated even when disabled, so flipping the flag on needs
	// no other env to run.
	assert.Equal(t, 60*time.Second, cfg.DiagnosisAgentTimeout)
	assert.Equal(t, 10, cfg.DiagnosisAgentScoreMax)
}

func TestLoadTrainingConfig_DiagnosisEnabled(t *testing.T) {
	clearTrainingEnv(t)

	os.Setenv("DIAGNOSIS_AGENT_ENABLED", "true")
	os.Setenv("DIAGNOSIS_AGENT_PATH", "/usr/local/bin/pi")
	os.Setenv("DIAGNOSIS_AGENT_MODEL", "anthropic/claude-sonnet-5")
	os.Setenv("DIAGNOSIS_AGENT_TIMEOUT_SECONDS", "120")
	os.Setenv("DIAGNOSIS_AGENT_SCORE_MAX", "20")

	cfg := LoadTrainingConfig()
	assert.True(t, cfg.DiagnosisAgentEnabled)
	assert.Equal(t, "/usr/local/bin/pi", cfg.DiagnosisAgentPath)
	assert.Equal(t, "anthropic/claude-sonnet-5", cfg.DiagnosisAgentModel)
	assert.Equal(t, 120*time.Second, cfg.DiagnosisAgentTimeout)
	assert.Equal(t, 20, cfg.DiagnosisAgentScoreMax)
}

func TestLoadTrainingConfig_DiagnosisOnDemandEnabled(t *testing.T) {
	clearTrainingEnv(t)
	t.Setenv(diagnosisAgentOnDemandEnabledEnv, "true")

	cfg := LoadTrainingConfig()
	assert.True(t, cfg.DiagnosisAgentOnDemandEnabled)
}

func TestLoadTrainingConfig_DiagnosisInvalidTimeoutAndScoreMax(t *testing.T) {
	clearTrainingEnv(t)

	os.Setenv("DIAGNOSIS_AGENT_ENABLED", "true")
	os.Setenv("DIAGNOSIS_AGENT_TIMEOUT_SECONDS", "not-a-number")
	os.Setenv("DIAGNOSIS_AGENT_SCORE_MAX", "oops")

	cfg := LoadTrainingConfig()
	assert.True(t, cfg.DiagnosisAgentEnabled)
	assert.Equal(t, 60*time.Second, cfg.DiagnosisAgentTimeout, "invalid timeout falls back to 60s")
	assert.Equal(t, 10, cfg.DiagnosisAgentScoreMax, "invalid score max falls back to 10")
}

// In the production path (q != nil) with bridge + DAG configured, enabling
// diagnosis wires a non-nil Diagnoser; disabling leaves it nil. The trigger
// (maybeDiagnoseProject, training.go) gates on deps.Diagnosis == nil, so a nil
// diagnoser is a clean no-op.
func TestNewTrainingSessionDeps_DiagnosisWiredWhenEnabled(t *testing.T) {
	clearTrainingEnv(t)

	q := db.New(nil)

	// Enabled: Diagnosis is a non-nil Diagnoser (a *DiagnosisAgentRunner).
	cfg := TrainingConfig{
		BridgeStubURL:         "http://localhost:9100/v1",
		AdminAPIKey:           "test-key-123",
		InteractionDAGEnabled: true,
		DiagnosisAgentEnabled: true,
	}
	deps := NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps)
	assert.NotNil(t, deps.DAG, "DAG must be wired so the diagnosis trigger can fire")
	assert.NotNil(t, deps.Diagnosis, "Diagnosis must be wired when enabled in the production path")

	// Disabled: Diagnosis is nil even with DAG wired.
	cfg.DiagnosisAgentEnabled = false
	deps = NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps.DAG)
	assert.Nil(t, deps.Diagnosis, "Diagnosis must be nil when disabled")
}

// Diagnosis requires the full bridge path: when bridge config is missing
// (guard-deps path), deps is non-nil so the open-hook loud-error guard is
// reachable. The local env-dispatch DAG remains wired, while Diagnosis stays
// nil because it requires the full training bridge.
func TestNewTrainingSessionDeps_DiagnosisNilWhenBridgeConfigMissing(t *testing.T) {
	clearTrainingEnv(t)

	q := db.New(nil)

	// Diagnosis enabled but bridge config missing: guard-deps path returns
	// non-nil deps with Lookup+Store and a local-only DAG.
	cfg := TrainingConfig{
		AdminAPIKey:           "test-key-123", // no BridgeStubURL
		DiagnosisAgentEnabled: true,
	}
	deps := NewTrainingSessionDeps(cfg, q)
	assert.NotNil(t, deps, "deps non-nil so the open-hook guard is reachable")
	require.NotNil(t, deps.DAG, "local env-dispatch DAG remains available without bridge config")
	assert.Nil(t, deps.DAG.client)
	assert.Nil(t, deps.Diagnosis, "Diagnosis nil when bridge config missing - requires the full path")
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
		"DIAGNOSIS_AGENT_ENABLED",
		"DIAGNOSIS_AGENT_PATH",
		"DIAGNOSIS_AGENT_MODEL",
		"DIAGNOSIS_AGENT_TIMEOUT_SECONDS",
		"DIAGNOSIS_AGENT_SCORE_MAX",
	}
	for _, env := range envVars {
		if err := os.Unsetenv(env); err != nil {
			t.Logf("Warning: Could not unset env var %s: %v", env, err)
		}
	}
}

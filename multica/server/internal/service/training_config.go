// SPDX-License-Identifier: Apache-2.0

package service

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/multica-ai/multica/server/internal/arealrl"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TrainingConfig holds the env-derived config for the RL training bridge.
// All fields are optional — when empty, training is not configured and the
// session-open/close hooks are no-ops.
type TrainingConfig struct {
	BridgeStubURL string  // AREAL_BRIDGE_STUB_URL — db_bridge stub base URL
	AdminAPIKey   string  // AREAL_ADMIN_API_KEY — authenticates start_session
	DefaultReward float64 // TRAINING_DEFAULT_REWARD — fallback reward (default 1.0)
	ProxyURL      string  // AREAL_PROXY_URL — URL the trained pi routes to (default http://db_bridge_stub:9100/v1)
	// InteractionDAGEnabled gates segment-DAG recording for trained rollouts
	// (INTERACTION_DAG_ENABLED). Defaults true (on for trained rollouts); set
	// "false"/"0" to disable. A disabled recorder is a no-op at the seams.
	InteractionDAGEnabled bool
	// DiagnosisAgent* runtime settings are retained for the non-training,
	// on-demand diagnosis handler. Training session construction does not use
	// these settings.
	DiagnosisAgentPath     string        // DIAGNOSIS_AGENT_PATH (empty -> pi on PATH)
	DiagnosisAgentModel    string        // DIAGNOSIS_AGENT_MODEL
	DiagnosisAgentTimeout  time.Duration // DIAGNOSIS_AGENT_TIMEOUT_SECONDS (default 60s)
	DiagnosisAgentScoreMax int           // DIAGNOSIS_AGENT_SCORE_MAX (default 10)
	// On-demand diagnosis uses a persistent Pi RPC session and per-segment
	// paging.
	DiagnosisAgentPageTurnLimit          int // DIAGNOSIS_AGENT_PAGE_TURN_LIMIT (default 20)
	DiagnosisAgentPageByteLimit          int // DIAGNOSIS_AGENT_PAGE_BYTE_LIMIT (default 24576)
	DiagnosisAgentEmergencyContextPct    int // DIAGNOSIS_AGENT_HARD_CONTEXT_PERCENT (default 80)
	DiagnosisAgentMaxRefetchesPerSegment int // DIAGNOSIS_AGENT_MAX_REFETCHES_PER_SEGMENT (default 2)
	DiagnosisAgentMaxRunTimeoutSecs      int // DIAGNOSIS_AGENT_MAX_RUN_TIMEOUT_SECONDS (default 0 = unset)
}

const (
	arealBridgeStubURLEnv                   = "AREAL_BRIDGE_STUB_URL"
	arealAdminAPIKeyEnv                     = "AREAL_ADMIN_API_KEY"
	trainingDefaultRewardEnv                = "TRAINING_DEFAULT_REWARD"
	arealProxyURLEnv                        = "AREAL_PROXY_URL"
	interactionDAGEnabledEnv                = "INTERACTION_DAG_ENABLED"
	diagnosisAgentPathEnv                   = "DIAGNOSIS_AGENT_PATH"
	diagnosisAgentModelEnv                  = "DIAGNOSIS_AGENT_MODEL"
	diagnosisAgentTimeoutSecsEnv            = "DIAGNOSIS_AGENT_TIMEOUT_SECONDS"
	diagnosisAgentScoreMaxEnv               = "DIAGNOSIS_AGENT_SCORE_MAX"
	diagnosisAgentPageTurnLimitEnv          = "DIAGNOSIS_AGENT_PAGE_TURN_LIMIT"
	diagnosisAgentPageByteLimitEnv          = "DIAGNOSIS_AGENT_PAGE_BYTE_LIMIT"
	diagnosisAgentEmergencyContextPctEnv    = "DIAGNOSIS_AGENT_HARD_CONTEXT_PERCENT"
	diagnosisAgentMaxRefetchesPerSegEnv     = "DIAGNOSIS_AGENT_MAX_REFETCHES_PER_SEGMENT"
	diagnosisAgentMaxRunTimeoutSecsEnv      = "DIAGNOSIS_AGENT_MAX_RUN_TIMEOUT_SECONDS"
	defaultProxyURL                         = "http://db_bridge_stub:9100/v1"
	defaultDiagnosisAgentTimeout            = 60 * time.Second
	defaultDiagnosisAgentScoreMax           = 10
	defaultDiagnosisAgentPageTurnLimit      = 20
	defaultDiagnosisAgentPageByteLimit      = 24576
	defaultDiagnosisAgentEmergencyCtxPct    = 80
	defaultDiagnosisAgentMaxRefetchesPerSeg = 2
)

// LoadTrainingConfig reads training config from environment variables. Returns
// zero-value TrainingConfig if AREAL_BRIDGE_STUB_URL is empty (training disabled).
func LoadTrainingConfig() TrainingConfig {
	cfg := TrainingConfig{
		BridgeStubURL: os.Getenv(arealBridgeStubURLEnv),
		AdminAPIKey:   os.Getenv(arealAdminAPIKeyEnv),
		ProxyURL:      os.Getenv(arealProxyURLEnv),
	}

	if cfg.ProxyURL == "" {
		cfg.ProxyURL = defaultProxyURL
	}

	cfg.DefaultReward = 1.0
	if raw := os.Getenv(trainingDefaultRewardEnv); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			slog.Warn("invalid env var, using default", "name", trainingDefaultRewardEnv, "value", raw, "default", 1.0, "error", err)
		} else {
			cfg.DefaultReward = v
		}
	}

	cfg.InteractionDAGEnabled = true // default: on for trained rollouts
	if raw := os.Getenv(interactionDAGEnabledEnv); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			slog.Warn("invalid env var, using default", "name", interactionDAGEnabledEnv, "value", raw, "default", true, "error", err)
		} else {
			cfg.InteractionDAGEnabled = v
		}
	}

	// Diagnosis agent runtime settings. Path/Model empty -> the runner resolves
	// the pi executable via PATH (agentpkg.New) and sends no --model flag.
	cfg.DiagnosisAgentPath = os.Getenv(diagnosisAgentPathEnv)
	cfg.DiagnosisAgentModel = os.Getenv(diagnosisAgentModelEnv)
	cfg.DiagnosisAgentTimeout = defaultDiagnosisAgentTimeout
	if raw := os.Getenv(diagnosisAgentTimeoutSecsEnv); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			slog.Warn("invalid env var, using default", "name", diagnosisAgentTimeoutSecsEnv, "value", raw, "default", defaultDiagnosisAgentTimeout, "error", err)
		} else {
			cfg.DiagnosisAgentTimeout = time.Duration(v) * time.Second
		}
	}
	cfg.DiagnosisAgentScoreMax = defaultDiagnosisAgentScoreMax
	if raw := os.Getenv(diagnosisAgentScoreMaxEnv); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			slog.Warn("invalid env var, using default", "name", diagnosisAgentScoreMaxEnv, "value", raw, "default", defaultDiagnosisAgentScoreMax, "error", err)
		} else {
			cfg.DiagnosisAgentScoreMax = v
		}
	}
	return cfg
}

// NewTrainingSessionDeps constructs a *TrainingSessionDeps from config.
//
// When the bridge is not configured (BridgeStubURL or AdminAPIKey empty) AND q
// is non-nil, returns a non-nil deps with Lookup+Store set but RL/Closer nil.
// This lets the session-open hook's loud-error guard (training.go:161-166)
// fire when a training target is requested despite missing config, instead of
// silently no-oping and letting the trained task run un-proxied. The close
// hook no-ops on nil Closer (training.go:257-259).
//
// When q is nil (test-only — production always passes a real *db.Queries),
// preserves the old contract: nil deps when config missing, non-nil deps with
// RL/Closer when config set.
func NewTrainingSessionDeps(cfg TrainingConfig, q *db.Queries) *TrainingSessionDeps {
	if q == nil {
		if cfg.BridgeStubURL == "" || cfg.AdminAPIKey == "" {
			return nil
		}
		client := arealrl.New(cfg.BridgeStubURL, cfg.AdminAPIKey)
		return &TrainingSessionDeps{
			RL:            client,
			Closer:        client,
			ProxyURL:      cfg.ProxyURL,
			DefaultReward: cfg.DefaultReward,
		}
	}

	if cfg.BridgeStubURL == "" || cfg.AdminAPIKey == "" {
		return &TrainingSessionDeps{
			Lookup:        q,
			Store:         q,
			Creator:       q,
			ProxyURL:      cfg.ProxyURL,
			DefaultReward: cfg.DefaultReward,
			DAG:           NewInteractionDAGServiceWithMessages(q, q, nil, cfg.InteractionDAGEnabled),
			// RL, Closer nil — open hook loud-errors if a training target is hit.
		}
	}

	client := arealrl.New(cfg.BridgeStubURL, cfg.AdminAPIKey)

	return &TrainingSessionDeps{
		Lookup:        q,
		Store:         q,
		Creator:       q,
		RL:            client,
		Closer:        client,
		ProxyURL:      cfg.ProxyURL,
		DefaultReward: cfg.DefaultReward,
		DAG:           NewInteractionDAGServiceWithMessages(q, q, client, cfg.InteractionDAGEnabled),
	}
}

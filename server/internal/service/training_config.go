// SPDX-License-Identifier: Apache-2.0

package service

import (
	"log/slog"
	"os"
	"strconv"

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
}

const (
	arealBridgeStubURLEnv    = "AREAL_BRIDGE_STUB_URL"
	arealAdminAPIKeyEnv      = "AREAL_ADMIN_API_KEY"
	trainingDefaultRewardEnv = "TRAINING_DEFAULT_REWARD"
	arealProxyURLEnv         = "AREAL_PROXY_URL"
	defaultProxyURL          = "http://db_bridge_stub:9100/v1"
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

	return cfg
}

// NewTrainingSessionDeps constructs a *TrainingSessionDeps from config. Returns
// nil if training is not configured (BridgeStubURL or AdminAPIKey empty).
func NewTrainingSessionDeps(cfg TrainingConfig, q *db.Queries) *TrainingSessionDeps {
	if cfg.BridgeStubURL == "" || cfg.AdminAPIKey == "" {
		return nil
	}

	client := arealrl.New(cfg.BridgeStubURL, cfg.AdminAPIKey)

	return &TrainingSessionDeps{
		Lookup:        q,
		Store:         q,
		RL:            client,
		Closer:        client,
		ProxyURL:      cfg.ProxyURL,
		DefaultReward: cfg.DefaultReward,
	}
}

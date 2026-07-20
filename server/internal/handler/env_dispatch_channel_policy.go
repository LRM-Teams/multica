package handler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/multica-ai/multica/server/internal/service"
)

// envDispatchSandboxConfig is the canonical shape of an env-agent binding's
// sandbox_config column: the sandbox template plus an optional external model
// runtime. It is the only place the caller's API key is persisted; the runtime
// never appears on SandboxInstanceRef, in HTTP responses, errors, or logs.
type envDispatchSandboxConfig struct {
	Template string                        `json:"template"`
	Runtime  *service.ExternalModelRuntime `json:"runtime,omitempty"`
}

// marshalEnvDispatchSandboxConfig serializes a resolved per-agent sandbox policy
// into the canonical binding config. The runtime is normalized (trimmed and
// validated) before encoding so the stored value is canonical; an empty template
// defaults to "default". Error messages never format runtime values.
func marshalEnvDispatchSandboxConfig(policy service.ResolvedPerAgentSandboxPolicy) (json.RawMessage, error) {
	runtime, err := service.NormalizeExternalModelRuntime(policy.Runtime)
	if err != nil {
		return nil, fmt.Errorf("normalize runtime policy: %w", err)
	}
	template := policy.Template
	if template == "" {
		template = "default"
	}
	out, err := json.Marshal(envDispatchSandboxConfig{Template: template, Runtime: runtime})
	if err != nil {
		return nil, fmt.Errorf("encode sandbox config: %w", err)
	}
	return out, nil
}

// decodeEnvDispatchSandboxConfig decodes a stored binding config strictly: a
// malformed or partial policy is an error, replacing the prior permissive
// json.Unmarshal-with-ignored-error pattern. An empty or "{}" config decodes to
// the default template with no runtime, so members without an override still
// provision. The runtime, when present, is re-normalized so downstream
// consumers receive canonical values. Error messages never format runtime values.
func decodeEnvDispatchSandboxConfig(raw json.RawMessage) (envDispatchSandboxConfig, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return envDispatchSandboxConfig{Template: "default"}, nil
	}
	var cfg envDispatchSandboxConfig
	if err := json.Unmarshal(trimmed, &cfg); err != nil {
		return envDispatchSandboxConfig{}, fmt.Errorf("decode sandbox config: %w", err)
	}
	runtime, err := service.NormalizeExternalModelRuntime(cfg.Runtime)
	if err != nil {
		return envDispatchSandboxConfig{}, fmt.Errorf("normalize stored runtime: %w", err)
	}
	if cfg.Template == "" {
		cfg.Template = "default"
	}
	cfg.Runtime = runtime
	return cfg, nil
}

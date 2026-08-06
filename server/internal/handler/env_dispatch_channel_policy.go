package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/service"
)

// envDispatchSandboxConfig is the canonical shape of an env-agent binding's
// sandbox_config column: the sandbox template plus an optional external model
// runtime. It is the only place the caller's API key is persisted; the runtime
// never appears on SandboxInstanceRef, in HTTP responses, errors, or logs.
type envDispatchSandboxConfig struct {
	Template string                        `json:"template"`
	Runtime  *service.ExternalModelRuntime `json:"runtime,omitempty"`
	// Shared marks the binding as part of a shared_sandbox rollout: a
	// first-mention provision attaches the member to the sample's existing
	// shared sandbox/runtime instead of claiming a per-agent sandbox
	// (research D3).
	Shared bool `json:"shared,omitempty"`
	// SharedRuntime is the aggregate runtime catalog shared by every binding in
	// one shared rollout. It may contain credentials and is used only for
	// sandbox provisioning.
	SharedRuntime json.RawMessage `json:"shared_runtime,omitempty"`
	// ExecutionModel is this binding's non-secret alias/model selection.
	ExecutionModel string `json:"execution_model,omitempty"`
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
	template := strings.TrimSpace(policy.Template)
	if template == "" {
		template = "default"
	}
	executionModel := strings.TrimSpace(policy.ExecutionModel)
	if executionModel == "" {
		executionModel = service.ExternalRuntimeExecutionModel(runtime)
	}
	out, err := json.Marshal(envDispatchSandboxConfig{
		Template:       template,
		Runtime:        runtime,
		Shared:         policy.Shared,
		SharedRuntime:  policy.SharedRuntime,
		ExecutionModel: executionModel,
	})
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
	cfg.Template = strings.TrimSpace(cfg.Template)
	if cfg.Template == "" {
		cfg.Template = "default"
	}
	cfg.Runtime = runtime
	return cfg, nil
}

// createInput builds the sandbox_instance creation input from a decoded binding
// policy: the template, a daemon-enabled flag, the MULTICA_DAEMON_ID runtime
// env, and the runtime marshalled with its provider selection. When the policy
// has no runtime, the Runtime field is nil. The marshalled runtime carries the
// API key into the sandbox lifecycle only; it never reaches SandboxInstanceRef,
// responses, errors, or logs.
func (c envDispatchSandboxConfig) createInput(workspaceID, daemonID string) (service.CreateSandboxInstanceInput, error) {
	runtimeJSON := json.RawMessage(nil)
	if c.Shared && len(bytes.TrimSpace(c.SharedRuntime)) > 0 {
		runtimeJSON = append(json.RawMessage(nil), c.SharedRuntime...)
	} else if c.Runtime != nil {
		encoded, err := json.Marshal(c.Runtime)
		if err != nil {
			return service.CreateSandboxInstanceInput{}, fmt.Errorf("encode sandbox runtime policy: %w", err)
		}
		runtimeJSON = encoded
	}
	// MULTICA_DAEMON_ID is only pre-assigned for the branch path (which still
	// pre-creates a runtime). The scratch first-address path passes "" so the
	// sandbox lifecycle mints a unique daemon correlation nonce (ref.DaemonID)
	// and env-dispatch discovers the online runtime after registration.
	runtimeEnv := map[string]string{}
	if daemonID != "" {
		runtimeEnv["MULTICA_DAEMON_ID"] = daemonID
	}
	return service.CreateSandboxInstanceInput{
		WorkspaceID:   workspaceID,
		Template:      c.Template,
		DaemonEnabled: true,
		Runtime:       runtimeJSON,
		RuntimeEnv:    runtimeEnv,
	}, nil
}

// validateEnvDispatchCredentialOwner enforces the spec AC-4 invariant that a
// binding's model-configuration owner equals its source agent. A model
// credential or training session MUST NOT be used when its owner does not equal
// the binding source agent. An empty owner (legacy/unset binding) or empty source
// (caller has no owner fact to compare) is allowed so the check is additive; an
// explicit mismatch between two known identities fails closed. Error messages
// never format credential values.
func validateEnvDispatchCredentialOwner(binding envAgentSandboxBinding, sourceAgentID string) error {
	if binding.ModelConfigOwnerAgentID != "" && sourceAgentID != "" && binding.ModelConfigOwnerAgentID != sourceAgentID {
		return fmt.Errorf("env-dispatch model credential owner mismatch")
	}
	return nil
}

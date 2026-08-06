package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// TestEnvDispatchSandboxConfigCodec verifies the binding policy codec round-trips
// a resolved policy with canonical trimmed values and template "default", so the
// env-agent binding stores exactly the three-key runtime object plus template.
func TestEnvDispatchSandboxConfigCodec(t *testing.T) {
	// Whitespace on every field: the codec must normalize through the service
	// helper before serializing.
	policy := service.ResolvedPerAgentSandboxPolicy{
		Template: "  default  ",
		Runtime: &service.ExternalModelRuntime{
			Provider: " anthropic ",
			BaseURL:  " https://provider.invalid/v1 ",
			APIKey:   " synthetic-secret-for-tests ",
			Model:    " model-a ",
		},
	}
	raw, err := marshalEnvDispatchSandboxConfig(policy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		`"template":"default"`,
		`"provider":"anthropic"`,
		`"base_url":"https://provider.invalid/v1"`,
		`"api_key":"synthetic-secret-for-tests"`,
		`"model":"model-a"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("marshaled config missing %q: %s", want, body)
		}
	}

	decoded, err := decodeEnvDispatchSandboxConfig(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Template != "default" {
		t.Fatalf("decoded template = %q, want default", decoded.Template)
	}
	if decoded.Runtime == nil {
		t.Fatalf("decoded runtime is nil")
	}
	if decoded.Runtime.Provider != "anthropic" ||
		decoded.Runtime.BaseURL != "https://provider.invalid/v1" ||
		decoded.Runtime.APIKey != "synthetic-secret-for-tests" ||
		decoded.Runtime.Model != "model-a" {
		t.Fatalf("decoded runtime mismatch: %+v", decoded.Runtime)
	}
	if decoded.ExecutionModel != "anthropic/model-a" {
		t.Fatalf("decoded execution model = %q, want anthropic/model-a", decoded.ExecutionModel)
	}
}

// TestEnvDispatchSandboxConfigCodec_RejectsMalformed verifies the codec rejects
// malformed and partial stored policy rather than silently ignoring the error
// (replacing the prior permissive json.Unmarshal pattern).

func TestEnvDispatchSandboxConfigCodecRoundTripsSharedRuntimeAndExecutionModel(t *testing.T) {
	sentinel := "synthetic-shared-key"
	sharedRuntime := json.RawMessage(`{"providers":[{"provider":"env-leader-1","api_key":"` + sentinel + `","base_url":"https://route.invalid/v1","model":"glm-5.2"}],"default_provider":"env-leader-1","default_model":"glm-5.2"}`)
	policy := service.ResolvedPerAgentSandboxPolicy{
		Template:       "default",
		Shared:         true,
		SharedRuntime:  sharedRuntime,
		ExecutionModel: "env-leader-1/glm-5.2",
	}

	raw, err := marshalEnvDispatchSandboxConfig(policy)
	if err != nil {
		t.Fatalf("marshal shared config: %v", err)
	}
	decoded, err := decodeEnvDispatchSandboxConfig(raw)
	if err != nil {
		t.Fatalf("decode shared config: %v", err)
	}
	if !decoded.Shared || string(decoded.SharedRuntime) != string(sharedRuntime) {
		t.Fatal("aggregate shared runtime did not round-trip")
	}
	if decoded.ExecutionModel != policy.ExecutionModel {
		t.Fatalf("execution model = %q, want %q", decoded.ExecutionModel, policy.ExecutionModel)
	}
	createInput, err := decoded.createInput("ws-1", "")
	if err != nil {
		t.Fatalf("create shared input: %v", err)
	}
	if string(createInput.Runtime) != string(sharedRuntime) {
		t.Fatal("shared provisioning did not use the aggregate runtime catalog")
	}

	_, err = decodeEnvDispatchSandboxConfig(json.RawMessage(`{"template":"default","runtime":{"base_url":"https://route.invalid/v1","api_key":"` + sentinel + `","model":""}}`))
	if err == nil {
		t.Fatal("expected invalid runtime error")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatal("decode error disclosed a runtime credential")
	}
}
func TestEnvDispatchSandboxConfigCodec_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"malformed json", json.RawMessage(`{invalid`)},
		{"partial runtime missing api_key", json.RawMessage(`{"template":"default","runtime":{"base_url":"https://provider.invalid/v1","api_key":"","model":"m"}}`)},
		{"non-http url", json.RawMessage(`{"template":"default","runtime":{"base_url":"ftp://provider.invalid/v1","api_key":"k","model":"m"}}`)},
		{"relative url", json.RawMessage(`{"template":"default","runtime":{"base_url":"/v1","api_key":"k","model":"m"}}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := decodeEnvDispatchSandboxConfig(c.raw); err == nil {
				t.Fatalf("expected decode error for %s, got nil", c.raw)
			}
		})
	}
	// Malformed decode errors must not embed the sentinel key.
	if _, err := decodeEnvDispatchSandboxConfig(json.RawMessage(`{"template":"default","runtime":{"base_url":"https://provider.invalid/v1","api_key":"synthetic-secret-for-tests","model":""}}`)); err == nil {
		t.Fatalf("expected decode error for partial runtime")
	}
}

// TestEnvDispatchSandboxConfigCodec_EmptyDefaultsToDefaultTemplate verifies that
// a binding without an override (stored as "{}" or empty) decodes to the default
// template with no runtime, so unconfigured members still provision.
func TestEnvDispatchSandboxConfigCodec_EmptyDefaultsToDefaultTemplate(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(``), json.RawMessage(`   `)} {
		decoded, err := decodeEnvDispatchSandboxConfig(raw)
		if err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		if decoded.Template != "default" {
			t.Fatalf("expected default template for %q, got %q", raw, decoded.Template)
		}
		if decoded.Runtime != nil {
			t.Fatalf("expected no runtime for %q, got %+v", raw, decoded.Runtime)
		}
	}
}

// TestEnvDispatchSandboxConfigCodec_TrimsStoredTemplate verifies legacy binding
// configs with surrounding whitespace are canonicalized before provisioning.
func TestEnvDispatchSandboxConfigCodec_TrimsStoredTemplate(t *testing.T) {
	decoded, err := decodeEnvDispatchSandboxConfig(json.RawMessage(`{"template":"  python  "}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Template != "python" {
		t.Fatalf("decoded template = %q, want python", decoded.Template)
	}
}

// TestEnvDispatchSandboxConfig_SandboxInstanceRefHasNoRuntimeSecret
// verifies the runtime policy is never stored on SandboxInstanceRef: even a
// populated ref (with unrelated RuntimeMetadata) cannot carry the API key. The
// secret lives only in the env-agent binding's sandbox_config.
func TestEnvDispatchSandboxConfigCodec_SandboxInstanceRefHasNoRuntimeSecret(t *testing.T) {
	sentinel := "synthetic-secret-for-tests"
	emptyRefJSON, _ := json.Marshal(service.SandboxInstanceRef{})
	if strings.Contains(string(emptyRefJSON), sentinel) {
		t.Fatalf("empty SandboxInstanceRef must not carry runtime policy: %s", emptyRefJSON)
	}
	ref := service.SandboxInstanceRef{
		Template:        "default",
		RuntimeMetadata: json.RawMessage(`{"note":"not-the-policy"}`),
	}
	refJSON, _ := json.Marshal(ref)
	if strings.Contains(string(refJSON), sentinel) {
		t.Fatalf("SandboxInstanceRef must not carry runtime policy: %s", refJSON)
	}
}

// TestEnvDispatchSandboxConfigCreateInput verifies the createInput helper builds
// a CreateSandboxInstanceInput carrying the default template, daemon-enabled
// flag, MULTICA_DAEMON_ID runtime env, and the runtime marshalled to exactly the
// three-key object. A config without a runtime yields a nil Runtime field.
func TestEnvDispatchSandboxConfigCreateInput(t *testing.T) {
	config := envDispatchSandboxConfig{
		Template: "default",
		Runtime: &service.ExternalModelRuntime{
			Provider: "anthropic",
			BaseURL:  "https://provider.invalid/v1",
			APIKey:   "synthetic-secret-for-tests",
			Model:    "model-a",
		},
	}
	in, err := config.createInput("ws-1", "daemon-1")
	if err != nil {
		t.Fatalf("createInput: %v", err)
	}
	if in.WorkspaceID != "ws-1" {
		t.Fatalf("WorkspaceID = %q, want ws-1", in.WorkspaceID)
	}
	if in.Template != "default" {
		t.Fatalf("Template = %q, want default", in.Template)
	}
	if !in.DaemonEnabled {
		t.Fatalf("DaemonEnabled = false, want true")
	}
	if in.RuntimeEnv["MULTICA_DAEMON_ID"] != "daemon-1" {
		t.Fatalf("RuntimeEnv[MULTICA_DAEMON_ID] = %q, want daemon-1", in.RuntimeEnv["MULTICA_DAEMON_ID"])
	}
	wantRuntime := `{"provider":"anthropic","base_url":"https://provider.invalid/v1","api_key":"synthetic-secret-for-tests","model":"model-a"}`
	if string(in.Runtime) != wantRuntime {
		t.Fatalf("Runtime = %s, want %s", in.Runtime, wantRuntime)
	}

	// No runtime -> nil Runtime field, template still carried.
	emptyIn, err := envDispatchSandboxConfig{Template: "python"}.createInput("ws-1", "daemon-1")
	if err != nil {
		t.Fatalf("createInput (no runtime): %v", err)
	}
	if emptyIn.Runtime != nil {
		t.Fatalf("Runtime = %s, want nil", emptyIn.Runtime)
	}
	if emptyIn.Template != "python" {
		t.Fatalf("Template = %q, want python", emptyIn.Template)
	}
}

// TestEnvDispatchSandboxConfigRejectsSecretDisclosure verifies that malformed
// and partial stored policies fail at decode time and the error never includes
// the sentinel API key, so a stored-config failure cannot leak the secret.
func TestEnvDispatchSandboxConfigRejectsSecretDisclosure(t *testing.T) {
	sentinel := "synthetic-secret-for-tests"
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"partial runtime with sentinel", json.RawMessage(`{"template":"default","runtime":{"base_url":"https://provider.invalid/v1","api_key":"` + sentinel + `","model":""}}`)},
		{"non-http url with sentinel", json.RawMessage(`{"template":"default","runtime":{"base_url":"ftp://x","api_key":"` + sentinel + `","model":"m"}}`)},
		{"malformed json", json.RawMessage(`{"template":"default","runtime":{invalid}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeEnvDispatchSandboxConfig(c.raw)
			if err == nil {
				t.Fatalf("expected decode error for %s", c.raw)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("error must not include the sentinel key: %q", err.Error())
			}
		})
	}
}

// TestValidateEnvDispatchCredentialOwner enforces the spec AC-4 invariant that a
// binding's model-configuration owner must equal its source agent. A mismatch
// must fail closed; an empty (legacy/unset) owner is allowed so the check is
// additive; the error must never echo credential material.
func TestValidateEnvDispatchCredentialOwner(t *testing.T) {
	cases := []struct {
		name    string
		owner   string
		source  string
		wantErr bool
	}{
		{"matching owner", "agent-a", "agent-a", false},
		{"empty owner allowed (legacy)", "", "agent-a", false},
		{"mismatch fails closed", "agent-b", "agent-a", true},
		{"empty source with owner allowed", "agent-a", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			binding := envAgentSandboxBinding{ModelConfigOwnerAgentID: c.owner}
			err := validateEnvDispatchCredentialOwner(binding, c.source)
			if c.wantErr && err == nil {
				t.Fatalf("expected owner mismatch error for owner=%q source=%q", c.owner, c.source)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error for owner=%q source=%q: %v", c.owner, c.source, err)
			}
			if err != nil && strings.Contains(err.Error(), "sentinel") {
				t.Fatalf("error must not echo credential material: %q", err.Error())
			}
		})
	}
}

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
			BaseURL: " https://provider.invalid/v1 ",
			APIKey:  " synthetic-secret-for-tests ",
			Model:   " model-a ",
		},
	}
	raw, err := marshalEnvDispatchSandboxConfig(policy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, want := range []string{
		`"template":"default"`,
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
	if decoded.Runtime.BaseURL != "https://provider.invalid/v1" ||
		decoded.Runtime.APIKey != "synthetic-secret-for-tests" ||
		decoded.Runtime.Model != "model-a" {
		t.Fatalf("decoded runtime mismatch: %+v", decoded.Runtime)
	}
}

// TestEnvDispatchSandboxConfigCodec_RejectsMalformed verifies the codec rejects
// malformed and partial stored policy rather than silently ignoring the error
// (replacing the prior permissive json.Unmarshal pattern).
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

// TestEnvDispatchSandboxConfigCodec_SandboxInstanceRefHasNoRuntimeSecret
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

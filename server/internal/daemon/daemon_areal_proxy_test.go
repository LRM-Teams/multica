// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestArealProxyExecOverrideNil verifies a task with no proxy config leaves the
// caller's runtime configuration untouched (ok=false).
func TestArealProxyExecOverrideNil(t *testing.T) {
	if _, _, _, _, ok := arealProxyExecOverride(nil); ok {
		t.Fatal("nil proxy: want ok=false")
	}
}

// TestArealProxyExecOverrideFull confirms the override maps the proxy config to
// pi's real arg/env contract (§4.4): Model "openai/areal-default" (which Pi
// should preserve as a provider-prefixed model id), the api-key injected via
// pi's `--api-key` flag, and the base_url exported as the env var a deployment's
// models.json `areal` provider references.
func TestArealProxyExecOverrideFull(t *testing.T) {
	p := &ArealProxy{
		Provider: "openai",
		Model:    "areal-default",
		APIKey:   "proxy-key-123",
		BaseURL:  "http://db_bridge_stub:9100/v1",
	}
	model, args, envKey, envVal, ok := arealProxyExecOverride(p)
	if !ok {
		t.Fatal("want ok=true")
	}
	if model != "openai/areal-default" {
		t.Errorf("model = %q, want openai/areal-default", model)
	}
	if !reflect.DeepEqual(args, []string{"--api-key", "proxy-key-123"}) {
		t.Errorf("args = %v, want [--api-key proxy-key-123]", args)
	}
	if envKey != arealProxyBaseURLEnvVar || envVal != "http://db_bridge_stub:9100/v1" {
		t.Errorf("env = %q=%q, want %s=http://db_bridge_stub:9100/v1", envKey, envVal, arealProxyBaseURLEnvVar)
	}
}

// TestArealProxyExecOverrideDefaults confirms provider/model fall back to the
// fixed areal/areal-default when the context object omits them.
func TestArealProxyExecOverrideDefaults(t *testing.T) {
	model, args, _, _, ok := arealProxyExecOverride(&ArealProxy{APIKey: "pk", BaseURL: "u"})
	if !ok || model != "openai/areal-default" {
		t.Fatalf("defaults: model=%q ok=%v, want openai/areal-default true", model, ok)
	}
	if !reflect.DeepEqual(args, []string{"--api-key", "pk"}) {
		t.Errorf("args = %v, want [--api-key pk]", args)
	}
}

func TestArealProxyExecOverrideKeepsSessionKeysDistinctAndRedacted(t *testing.T) {
	const firstKey = "synthetic-session-key-a"
	const secondKey = "synthetic-session-key-b"
	first := &ArealProxy{
		Provider: "openai", Model: "areal-default",
		APIKey: firstKey, BaseURL: "https://proxy.invalid/v1",
	}
	second := &ArealProxy{
		Provider: "openai", Model: "areal-default",
		APIKey: secondKey, BaseURL: "https://proxy.invalid/v1",
	}

	_, firstArgs, _, _, firstOK := arealProxyExecOverride(first)
	_, secondArgs, _, _, secondOK := arealProxyExecOverride(second)
	if !firstOK || !secondOK || len(firstArgs) != 2 || len(secondArgs) != 2 {
		t.Fatal("proxy overrides did not produce one credential argument each")
	}
	if firstArgs[1] != firstKey || secondArgs[1] != secondKey || firstArgs[1] == secondArgs[1] {
		t.Fatal("proxy overrides did not retain their task-scoped credentials")
	}

	for _, rendered := range []string{
		fmt.Sprint(first), fmt.Sprintf("%+v", first), fmt.Sprintf("%#v", first),
		fmt.Sprint(second), fmt.Sprintf("%+v", second), fmt.Sprintf("%#v", second),
	} {
		if strings.Contains(rendered, firstKey) || strings.Contains(rendered, secondKey) {
			t.Fatal("formatted ArealProxy disclosed a session credential")
		}
	}
}

package researchrun

import (
	"testing"
	"time"
)

func TestFingerprintExecutionTargetPreservesAggregateProtocol(t *testing.T) {
	target := ExecutionTarget{
		Adapter: "agent_inbox", AgentID: "agent-a", RuntimeID: "runtime-a",
		Provider: "codex", Model: "gpt-test",
	}
	config := ExecutionTargetConfigIdentity{
		RuntimeMode: "local", RuntimePinnedVersion: "1.2.3",
		ProviderStateFingerprint: "daemon-fingerprint", RuntimeConfig: `{"sandbox":true}`,
		CustomEnv: `{"TOKEN":"redacted"}`, CustomArgs: `["--safe"]`,
		MCPConfig: `{"servers":[]}`, ThinkingLevel: "high",
	}
	got := FingerprintExecutionTarget(target, config)
	wantAggregate := ExecutionTargetFingerprint(
		target.AgentID, target.RuntimeID, target.Provider, target.Model,
		config.RuntimeMode, config.RuntimePinnedVersion, config.ProviderStateFingerprint,
		config.RuntimeConfig, config.CustomEnv, config.CustomArgs, config.MCPConfig,
		config.ThinkingLevel,
	)
	if got.ConfigFingerprint != wantAggregate {
		t.Fatalf("aggregate fingerprint changed: got=%s want=%s", got.ConfigFingerprint, wantAggregate)
	}
	if got.AgentConfigFingerprint == "" || got.RuntimeConfigFingerprint == "" || got.ProviderConfigFingerprint == "" {
		t.Fatalf("scoped fingerprints missing: %+v", got)
	}
}

func TestCircuitTargetsSeparateExecutionFailureDomains(t *testing.T) {
	base := ExecutionTarget{
		Adapter: "agent_inbox", AgentID: "agent-a", RuntimeID: "runtime-a",
		Provider: "Codex", ConfigFingerprint: "target-a",
		AgentConfigFingerprint: "agent-config-a", RuntimeConfigFingerprint: "runtime-config-a",
		ProviderConfigFingerprint: "provider-config-a",
	}
	agent, err := CircuitTargetForExecution(base, CircuitAgent)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CircuitTargetForExecution(base, CircuitRuntime)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := CircuitTargetForExecution(base, CircuitProvider)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := CircuitTargetForExecution(base, CircuitAdapter)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Key == runtime.Key || runtime.Key == provider.Key || provider.Key == adapter.Key {
		t.Fatalf("scope keys overlap: agent=%s runtime=%s provider=%s adapter=%s", agent.Key, runtime.Key, provider.Key, adapter.Key)
	}

	agentChanged := base
	agentChanged.Model = "other-model"
	agentChanged.AgentConfigFingerprint = "agent-config-b"
	agentChanged.ConfigFingerprint = "target-b"
	changedAgent, _ := CircuitTargetForExecution(agentChanged, CircuitAgent)
	changedRuntime, _ := CircuitTargetForExecution(agentChanged, CircuitRuntime)
	if changedAgent.Key != agent.Key || changedAgent.ConfigFingerprint == agent.ConfigFingerprint {
		t.Fatalf("agent configuration must retain identity and change fingerprint: before=%+v after=%+v", agent, changedAgent)
	}
	if changedRuntime != runtime {
		t.Fatalf("agent-only configuration contaminated runtime circuit: before=%+v after=%+v", runtime, changedRuntime)
	}

	otherRuntime := base
	otherRuntime.RuntimeID = "runtime-b"
	otherRuntime.RuntimeConfigFingerprint = "runtime-config-b"
	otherRuntime.ProviderConfigFingerprint = "provider-config-b"
	otherRuntimeTarget, _ := CircuitTargetForExecution(otherRuntime, CircuitRuntime)
	otherProviderTarget, _ := CircuitTargetForExecution(otherRuntime, CircuitProvider)
	if otherRuntimeTarget.Key == runtime.Key || otherProviderTarget.Key == provider.Key {
		t.Fatalf("different runtime/provider configuration shared a circuit: runtime=%+v provider=%+v", otherRuntimeTarget, otherProviderTarget)
	}
}

func TestCircuitFailurePoliciesAreBounded(t *testing.T) {
	tests := []struct {
		name        string
		disposition FailureDisposition
		threshold   int
		window      time.Duration
		openFor     time.Duration
	}{
		{"rate limit", failureDisposition(FailureRateLimited), 2, time.Minute, time.Minute},
		{"provider", failureDisposition(FailureProvider), 3, 2 * time.Minute, 2 * time.Minute},
		{"runtime", failureDisposition(FailureRuntimeLost), 2, 2 * time.Minute, time.Minute},
		{"timeout", failureDisposition(FailureTimeout), 3, 5 * time.Minute, 2 * time.Minute},
		{"credential", failureDisposition(FailureCredential), 1, 15 * time.Minute, 15 * time.Minute},
		{"internal", failureDisposition(FailureInternal), 1, 10 * time.Minute, 10 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, ok := policyForCircuitFailure(test.disposition)
			if !ok || policy.Threshold != test.threshold || policy.Window != test.window || policy.OpenDuration != test.openFor {
				t.Fatalf("policy=%+v ok=%v", policy, ok)
			}
		})
	}
	if _, ok := policyForCircuitFailure(failureDisposition(FailurePermission)); ok {
		t.Fatal("non-circuit failure received a circuit policy")
	}
}

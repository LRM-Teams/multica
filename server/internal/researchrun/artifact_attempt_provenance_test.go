package researchrun

import "testing"

func TestAttemptArtifactContentHashesImmutableDispatchFacts(t *testing.T) {
	first := Attempt{
		TaskID: "task-1", AttemptNumber: 2, AssignedAgentID: "agent-1", DispatchKey: "dispatch-2",
		ExecutionTarget: ExecutionTarget{
			Adapter: "agent_inbox", AgentID: "agent-1", RuntimeID: "runtime-1", Provider: "openai", Model: "gpt-5",
			ConfigFingerprint: "target-fp", AgentConfigFingerprint: "agent-fp",
			RuntimeConfigFingerprint: "runtime-fp", ProviderConfigFingerprint: "provider-fp",
		},
		Status: AttemptStatusDispatching,
	}
	firstHash, err := ArtifactContentHash(ArtifactKindAttempt, attemptArtifactContent(first))
	if err != nil {
		t.Fatal(err)
	}

	lifecycleChanged := first
	lifecycleChanged.Status = AttemptStatusSucceeded
	lifecycleChanged.ResultHash = "sha256:result"
	lifecycleHash, err := ArtifactContentHash(ArtifactKindAttempt, attemptArtifactContent(lifecycleChanged))
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != lifecycleHash {
		t.Fatalf("lifecycle projection changed immutable attempt hash: %q != %q", firstHash, lifecycleHash)
	}

	semanticChanged := first
	semanticChanged.ExecutionTarget.Model = "gpt-5.1"
	semanticHash, err := ArtifactContentHash(ArtifactKindAttempt, attemptArtifactContent(semanticChanged))
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == semanticHash {
		t.Fatal("different execution targets must not share an attempt version hash")
	}
}

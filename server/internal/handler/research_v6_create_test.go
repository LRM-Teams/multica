package handler

import (
	"context"
	"testing"
)

func TestResearchV6UserCreateDoesNotRequireBootstrapFlag(t *testing.T) {
	if !researchV6UserCreateEnabled(Config{ResearchV6BootstrapEnabled: false}) {
		t.Fatal("users must be able to create V6 runs without RESEARCH_V6_BOOTSTRAP_ENABLED")
	}
}

func TestResearchV6DirectorReadinessRequiresOnlineRuntimeWithoutCapabilityMarker(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID := createHandlerTestAgentWithIsolatedRuntime(t)

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime
		SET metadata = '{}'::jsonb, status = 'online', last_seen_at = now()
		WHERE id = $1
	`, runtimeID); err != nil {
		t.Fatalf("mark runtime online without capability marker: %v", err)
	}
	if readiness := testHandler.researchV6DirectorReadiness(ctx, parseUUID(testWorkspaceID), parseUUID(agentID)); readiness != nil {
		t.Fatalf("readiness with online runtime and no V6 capability marker = %#v, want ready", readiness)
	}

	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("mark runtime offline: %v", err)
	}
	offline := testHandler.researchV6DirectorReadiness(ctx, parseUUID(testWorkspaceID), parseUUID(agentID))
	if offline == nil || offline.code != "research.v6.director_runtime_offline" || !offline.retryable {
		t.Fatalf("readiness with offline runtime = %#v, want retryable offline", offline)
	}
}

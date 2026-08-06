package daemon

import (
	"reflect"
	"testing"
)

// Reviewer control for the locked agent×runtime resident contract:
// a turn/surface role change must not rotate the provider process.
//
// ManagedRole is channel-scoped (same agent can be group_manager in A and
// member in B). It must not live on the process identity or fingerprint.
func TestBarryCanonicalRuntimeFingerprintExcludesManagedRole(t *testing.T) {
	// Compile/structural control: field must not exist on the identity type.
	rt := reflect.TypeOf(canonicalAgentRuntimeIdentity{})
	if _, ok := rt.FieldByName("ManagedRole"); ok {
		t.Fatal("ManagedRole must not be a field on canonicalAgentRuntimeIdentity")
	}
	paramsT := reflect.TypeOf(canonicalAgentRuntimeIdentityParams{})
	if _, ok := paramsT.FieldByName("ManagedRole"); ok {
		t.Fatal("ManagedRole must not be a field on canonicalAgentRuntimeIdentityParams")
	}

	base := canonicalAgentRuntimeIdentityParams{
		AgentID:     "agent-a",
		RuntimeID:   "runtime-a",
		Provider:    "grok",
		Executable:  "/usr/local/bin/grok",
		WorkDir:     "/var/lib/multica/agent-a/workspace",
		Environment: map[string]string{"PATH": "/usr/bin"},
		WorkspaceID: "workspace-a",
	}
	a, err := newCanonicalAgentRuntimeIdentity(base)
	if err != nil {
		t.Fatalf("identity A: %v", err)
	}
	// Role only appears on full TaskContextForEnv / prompt path — not here.
	// Two builds with identical process-stable params must match.
	b, err := newCanonicalAgentRuntimeIdentity(base)
	if err != nil {
		t.Fatalf("identity B: %v", err)
	}
	if gotA, gotB := a.fingerprint(), b.fingerprint(); gotA != gotB {
		t.Fatalf("fingerprint unstable: a=%s b=%s", gotA, gotB)
	}
}

package handler

import (
	"strings"
	"testing"
)

func TestAgentResearchAttemptAuthorizationQueryFencesPrincipalAndActiveMembership(t *testing.T) {
	wantFragments := []string{
		"a.workspace_id = $1::uuid",
		"a.session_id = $2::uuid",
		"a.id = $3::uuid",
		"a.assigned_agent_id = $4::uuid",
		"fm.workspace_id = s.workspace_id",
		"fm.fleet_id = s.fleet_id",
		"fm.agent_id = a.assigned_agent_id",
		"fm.status = 'active'",
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(agentResearchAttemptAuthorizationQuery, fragment) {
			t.Fatalf("attempt authorization query missing %q", fragment)
		}
	}
}

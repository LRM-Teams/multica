package handler

import (
	"strings"
	"testing"
)

func TestAgentResearchAttemptAuthorizationQueryFencesPrincipalAndActiveMembership(t *testing.T) {
	wantFragments := []string{
		"SELECT a.task_id::text, COALESCE(a.inbox_task_id::text, '')",
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

func TestResearchAttemptCredentialMatchesExactInboxBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expected string
		bound    string
		want     bool
	}{
		{name: "exact", expected: "inbox-1", bound: "inbox-1", want: true},
		{name: "different attempt credential", expected: "inbox-1", bound: "inbox-2"},
		{name: "unbound attempt", expected: "", bound: "inbox-1"},
		{name: "unbound credential", expected: "inbox-1", bound: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := researchAttemptCredentialMatches(tc.expected, tc.bound); got != tc.want {
				t.Fatalf("researchAttemptCredentialMatches(%q,%q)=%v want=%v", tc.expected, tc.bound, got, tc.want)
			}
		})
	}
}

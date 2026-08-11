package main

import (
	"os"
	"strings"
	"testing"
)

// Members Directory (ADR 0013 / #2772): Agent human-API is dual-mounted under
// /api/members/agents (primary) and /api/agents (legacy alias for mobile/CLI).
func TestHumanAgentRoutesDualMountedUnderMembersAndLegacyAgents(t *testing.T) {
	raw, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	source := string(raw)

	if !strings.Contains(source, `r.Route("/api/agents", registerHumanAgentRoutes)`) {
		t.Fatal("missing legacy dual-mount: r.Route(\"/api/agents\", registerHumanAgentRoutes)")
	}
	if !strings.Contains(source, `r.Route("/api/members/agents", registerHumanAgentRoutes)`) {
		t.Fatal("missing members dual-mount: r.Route(\"/api/members/agents\", registerHumanAgentRoutes)")
	}
	if !strings.Contains(source, "registerHumanAgentRoutes :=") && !strings.Contains(source, "registerHumanAgentRoutes:=") {
		// either form of short var decl
		if !strings.Contains(source, "registerHumanAgentRoutes") {
			t.Fatal("missing shared registerHumanAgentRoutes registration function")
		}
	}
	// Directory-critical handlers must live inside the shared registrar so both
	// prefixes expose the same contract.
	for _, needle := range []string{
		"h.ListAgents",
		"h.GetAgent",
		"h.CreateAgent",
		"h.UpdateAgent",
		"h.ArchiveAgent",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("shared agent registrar missing handler ref %s", needle)
		}
	}
}

func TestHumanAgentReminderRouteIsReadOnly(t *testing.T) {
	raw, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	source := string(raw)
	if !strings.Contains(source, `r.Get("/reminders", h.ListAgentReminders)`) {
		t.Fatal("missing read-only human Agent Reminder route")
	}
	for _, method := range []string{"Post", "Put", "Patch", "Delete"} {
		if strings.Contains(source, `r.`+method+`("/reminders",`) {
			t.Fatalf("human Agent Reminder route unexpectedly exposes %s", method)
		}
	}
}

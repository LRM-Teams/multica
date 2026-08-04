package main

import (
	"strings"
	"testing"
)

func TestAgentCmdHasNoSubcommands(t *testing.T) {
	if len(agentCmd.Commands()) != 0 {
		names := make([]string, 0, len(agentCmd.Commands()))
		for _, c := range agentCmd.Commands() {
			names = append(names, c.Name())
		}
		t.Fatalf("agent CLI must have no subcommands after cut, got %v", names)
	}
}

func TestAgentCmdRunEPointsToWorkspaceInfo(t *testing.T) {
	err := agentCmd.RunE(agentCmd, nil)
	if err == nil {
		t.Fatal("expected error directing users away from agent CLI")
	}
	if !strings.Contains(err.Error(), "workspace info --agents") {
		t.Fatalf("error should point to workspace info --agents, got: %q", err)
	}
}

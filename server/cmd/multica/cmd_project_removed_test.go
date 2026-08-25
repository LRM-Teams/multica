package main

import (
	"strings"
	"testing"
)

func TestProjectCommandRemovedAndWorkspaceInfoIsMigrationPath(t *testing.T) {
	for _, command := range rootCmd.Commands() {
		if command.Name() == "project" {
			t.Fatal("legacy project command must not be registered")
		}
	}
	if !strings.Contains(workspaceInfoCmd.Long, "workspace info --projects") {
		t.Fatal("workspace info help should document the project migration path")
	}
}

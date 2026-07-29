package main

import (
	"os"
	"strings"
	"testing"
)

func TestAgentTaskCancelRouteIsDedicatedAndHumanRouteRemains(t *testing.T) {
	raw, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	source := string(raw)
	if !strings.Contains(source, `r.Post("/tasks/{taskId}/cancel", h.CancelAgentTask)`) {
		t.Fatal("missing POST /api/agent/tasks/{taskId}/cancel -> CancelAgentTask route")
	}
	if !strings.Contains(source, `r.Post("/api/tasks/{taskId}/cancel", h.CancelTaskByUser)`) {
		t.Fatal("human POST /api/tasks/{taskId}/cancel contract was removed")
	}
}

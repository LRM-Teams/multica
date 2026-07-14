package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestClaimedRadarCompletionExecutesOnlyPersistedFirstOutput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	fixture := createScheduledRadarExecutorFixture(t)
	run, task, err := testHandler.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
		WorkspaceID:    fixture.supervisor.WorkspaceID,
		AgentID:        fixture.supervisor.ID,
		TriggerKind:    "scheduled",
		TriggerRef:     "persisted-completion-" + uuid.NewString(),
		CooldownKey:    "workspace_supervisor_radar",
		ContextSummary: "persisted completion output test",
		ScheduledFor:   time.Now(),
		Prompt:         "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'dispatched' WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}

	firstPlan := `{"actions":[{"type":"no_action","target_kind":"none","reason":"first persisted plan","confidence":"high","risk_level":"low"}]}`
	firstResult, err := json.Marshal(TaskCompleteRequest{Output: firstPlan})
	if err != nil {
		t.Fatal(err)
	}
	first, err := testHandler.TaskService.CompleteDaemonTask(ctx, task.ID, firstResult, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.CompletedNow || first.RadarRun == nil {
		t.Fatalf("first completion = completed:%v run:%+v", first.CompletedNow, first.RadarRun)
	}

	retryPlan := `{"actions":[{"type":"no_action","target_kind":"none","reason":"retry must be ignored","confidence":"high","risk_level":"low"}]}`
	retryResult, err := json.Marshal(TaskCompleteRequest{Output: retryPlan})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := testHandler.TaskService.CompleteDaemonTask(ctx, task.ID, retryResult, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if retry.CompletedNow || retry.RadarRun != nil {
		t.Fatalf("retry completion = completed:%v run:%+v, want false/nil", retry.CompletedNow, retry.RadarRun)
	}

	testHandler.handleClaimedAgentRadarTask(ctx, first.Task, first.RadarRun)
	var status string
	var actionPlan []byte
	if err := testPool.QueryRow(ctx, `SELECT status, action_plan FROM agent_radar_run WHERE id = $1`, run.ID).Scan(&status, &actionPlan); err != nil {
		t.Fatal(err)
	}
	if status != "no_action" {
		t.Fatalf("run status = %q, want no_action", status)
	}
	if string(actionPlan) == "" || !jsonContainsString(actionPlan, "first persisted plan") {
		t.Fatalf("stored action plan = %s, want first persisted plan", actionPlan)
	}
	if jsonContainsString(actionPlan, "retry must be ignored") {
		t.Fatalf("stored action plan used retry output: %s", actionPlan)
	}
}

func jsonContainsString(raw []byte, want string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return containsJSONText(value, want)
}

func containsJSONText(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []any:
		for _, item := range typed {
			if containsJSONText(item, want) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsJSONText(item, want) {
				return true
			}
		}
	}
	return false
}

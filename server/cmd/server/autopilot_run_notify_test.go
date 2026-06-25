package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- pure helpers (no DB) ------------------------------------------------

func TestAutopilotRunReport(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"output", `{"output":"hello"}`, "hello"},
		{"trimmed", `{"output":"  hi \n"}`, "hi"},
		{"no output field", `{"pr_url":"https://x"}`, ""},
		{"invalid json", `not json`, ""},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := autopilotRunReport([]byte(c.in)); got != c.want {
				t.Fatalf("autopilotRunReport(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTruncateForInbox(t *testing.T) {
	if got := truncateForInbox("short", 10); got != "short" {
		t.Fatalf("short string mutated: %q", got)
	}
	// Rune-safe: 6 CJK chars capped at 3 must not split a multi-byte char.
	got := truncateForInbox("报告内容很长", 3)
	if got != "报告内…" {
		t.Fatalf("truncateForInbox CJK = %q, want %q", got, "报告内…")
	}
}

// --- delivery (DB-backed) ------------------------------------------------

// TestAutopilotRunOnlyDeliversReportToCreatorInbox is the regression for the
// "agent made a scheduled autopilot but I never received the report" bug:
// run_only dispatches a bare agent task and notifies nobody, so the creator
// must be notified out of band. On completion the run's stored output should
// land in the creator's inbox.
func TestAutopilotRunOnlyDeliversReportToCreatorInbox(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)
	registerAutopilotListeners(bus, autopilotSvc)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Title:              "Progress checker",
		Description:        pgtype.Text{},
		AssigneeType:       "agent",
		AssigneeID:         parseUUID(agentID),
		Status:             "active",
		ExecutionMode:      "run_only",
		IssueTitleTemplate: pgtype.Text{},
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateAutopilot: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, ap.ID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM inbox_item WHERE recipient_id = $1 AND type = 'autopilot_run_completed'`, testUserID)
	})

	run, err := autopilotSvc.DispatchAutopilot(ctx, ap, pgtype.UUID{}, "manual", nil)
	if err != nil {
		t.Fatalf("DispatchAutopilot: %v", err)
	}
	if !run.TaskID.Valid {
		t.Fatal("run_only dispatch did not link a task")
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'dispatched', dispatched_at = now() WHERE id = $1`,
		run.TaskID,
	); err != nil {
		t.Fatalf("mark task dispatched: %v", err)
	}
	task, err := queries.StartAgentTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("StartAgentTask: %v", err)
	}

	const report = "进度报告:7 步冒烟已过 5 步,最大阻塞是创建房间。"
	result, _ := json.Marshal(map[string]string{"output": report})
	if _, err := taskSvc.CompleteTask(ctx, task.ID, result, "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	// The completion chain (EventTaskCompleted → SyncRunFromTask →
	// EventAutopilotRunDone → notifyCreatorOnAutopilotRunDone) runs inline on
	// the synchronous bus, so the inbox item exists by now.
	var gotBody string
	if err := testPool.QueryRow(ctx, `
		SELECT body FROM inbox_item
		WHERE workspace_id = $1 AND recipient_id = $2 AND type = 'autopilot_run_completed'
		ORDER BY created_at DESC LIMIT 1`,
		testWorkspaceID, testUserID,
	).Scan(&gotBody); err != nil {
		t.Fatalf("expected an autopilot_run_completed inbox item for the creator: %v", err)
	}
	if !strings.Contains(gotBody, "创建房间") {
		t.Fatalf("inbox body missing the report content, got: %q", gotBody)
	}
}

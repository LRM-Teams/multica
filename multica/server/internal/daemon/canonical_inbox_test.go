package daemon

import (
	"context"
	"log/slog"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func canonicalInboxTaskForTest(task Task) Task {
	eventID := task.ID
	if eventID == "" {
		eventID = "event-test"
	}
	task.InboxEvent = &AgentInboxLease{
		ID:           eventID,
		DeliveryID:   "delivery-" + eventID,
		LeaseToken:   "lease-" + eventID,
		RequiresWake: true,
		RuntimeID:    task.RuntimeID,
	}
	return task
}

func (d *Daemon) executeAndDrain(ctx context.Context, backend agent.Backend, prompt string, opts agent.ExecOptions, taskLog *slog.Logger, taskID string) (agent.Result, int32, error) {
	return d.executeAndDrainForTask(ctx, backend, prompt, opts, taskLog, canonicalInboxTaskForTest(Task{ID: taskID}))
}

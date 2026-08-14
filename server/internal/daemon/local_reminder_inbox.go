package daemon

import (
	"context"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LocalReminderInbox is the single due-to-Agent admission seam. Timer fire,
// catch-up, and durable receipt replay may all reach this object, but only a
// due revision that has not already been accepted is handed to the resident
// runtime. Server receipt delivery is deliberately outside this module.
type LocalReminderInbox struct {
	daemon *Daemon
}

func (inbox *LocalReminderInbox) AcceptDue(job protocol.ReminderTimerJob) bool {
	if inbox == nil || inbox.daemon == nil || job.LocalInput == nil || strings.TrimSpace(job.LocalInput.Title) == "" {
		return false
	}
	d := inbox.daemon
	runtimeID, ok := d.reminderCache.runtimeFor(job)
	if !ok {
		return false
	}
	d.mu.Lock()
	runtime, known := d.runtimeIndex[runtimeID]
	d.mu.Unlock()
	if !known || runtime.WorkspaceID == "" {
		return false
	}
	local := job.LocalInput
	payload := protocol.ReminderOwnerInputPayload{
		WorkspaceID: runtime.WorkspaceID,
		AgentID:     job.OwnerAgentID,
		RuntimeID:   runtimeID,
		ReminderID:  job.ReminderID,
		Version:     job.Version,
		Title:       local.Title,
		Anchor:      local.Anchor,
		Occurrence: protocol.ReminderOwnerInputOccurrence{
			OccurrenceID: local.Occurrence.OccurrenceID,
			ScheduledFor: local.Occurrence.ScheduledFor,
			DueAt:        local.Occurrence.DueAt,
			Cadence:      local.Occurrence.Cadence,
			Timezone:     local.Occurrence.Timezone,
		},
	}
	return d.acceptReminderOwnerInput(context.Background(), payload, "") == reminderOwnerInputAccepted
}

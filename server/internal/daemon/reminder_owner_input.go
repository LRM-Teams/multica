package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type reminderOwnerInputOutcome string

const (
	reminderOwnerInputAccepted        reminderOwnerInputOutcome = "accepted"
	reminderOwnerInputDiscardedBusy   reminderOwnerInputOutcome = "discarded_busy"
	reminderOwnerInputInjectionFailed reminderOwnerInputOutcome = "injection_failed"
	reminderOwnerInputRejected        reminderOwnerInputOutcome = "rejected"

	reminderOwnerInputMaxPayloadBytes = 16 << 10
	reminderOwnerInputMaxExcerptBytes = 4 << 10
	reminderOwnerInputMaxTargetBytes  = 512
	reminderOwnerInputMaxIDBytes      = 128
)

// handleReminderOwnerInput is a terminal best-effort receive path. Every
// outcome is consumed here: none enters MessageCoordinator, Pending Notice,
// recovery, Activity, or another durable/user-visible projection.
func (d *Daemon) handleReminderOwnerInput(ctx context.Context, payload protocol.ReminderOwnerInputPayload) reminderOwnerInputOutcome {
	if reason := validateReminderOwnerInputPayload(payload); reason != "" {
		d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputRejected, reason)
		return reminderOwnerInputRejected
	}
	if d == nil || d.canonicalRuntimes == nil {
		return reminderOwnerInputRejected
	}

	d.mu.Lock()
	runtime, runtimeKnown := d.runtimeIndex[payload.RuntimeID]
	workspace := d.workspaces[payload.WorkspaceID]
	d.mu.Unlock()
	if !runtimeKnown || runtime.WorkspaceID != payload.WorkspaceID || !workspaceHasServerCapability(workspace, protocol.DaemonCapabilityReminderTransientInput) {
		d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputRejected, "runtime_or_capability_mismatch")
		return reminderOwnerInputRejected
	}
	attachment, ok := d.currentAttachmentForRuntimeAgent(payload.RuntimeID, payload.AgentID)
	if !ok || attachment.WorkspaceID != payload.WorkspaceID || int64(attachment.AttachmentGeneration) != payload.PlacementGeneration {
		d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputRejected, "owner_placement_mismatch")
		return reminderOwnerInputRejected
	}
	if !d.canonicalRuntimes.hasResidentBackend(payload.AgentID, payload.RuntimeID) {
		if err := d.ensureResidentMessageRuntime(ctx, payload.AgentID, payload.RuntimeID, nil); err != nil {
			d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputInjectionFailed, "resident_runtime_unavailable")
			return reminderOwnerInputInjectionFailed
		}
	}
	input := agent.ResidentReminderInput{
		ReminderID: payload.ReminderID,
		Version:    payload.Version,
		Title:      payload.Title,
		Anchor: agent.ResidentReminderAnchor{
			Available:           payload.Anchor.Available,
			ChannelID:           payload.Anchor.ChannelID,
			MessageID:           payload.Anchor.MessageID,
			ThreadRootMessageID: payload.Anchor.ThreadRootMessageID,
			Target:              payload.Anchor.Target,
			ReplyTarget:         payload.Anchor.ReplyTarget,
			Excerpt:             payload.Anchor.Excerpt,
		},
		Occurrence: agent.ResidentReminderOccurrence{
			OccurrenceID: payload.Occurrence.OccurrenceID,
			ScheduledFor: payload.Occurrence.ScheduledFor,
			DueAt:        payload.Occurrence.DueAt,
			Cadence:      payload.Occurrence.Cadence,
			Timezone:     payload.Occurrence.Timezone,
		},
	}
	err := d.canonicalRuntimes.handoffIdleReminderInput(ctx, payload.AgentID, payload.RuntimeID, input)
	switch {
	case errors.Is(err, ErrCanonicalAgentRuntimeBusy):
		d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputDiscardedBusy, "owner_busy")
		return reminderOwnerInputDiscardedBusy
	case err != nil:
		d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputInjectionFailed, "native_input_rejected")
		return reminderOwnerInputInjectionFailed
	default:
		d.recordReminderOwnerInputOutcome(payload, reminderOwnerInputAccepted, "")
		return reminderOwnerInputAccepted
	}
}

func workspaceHasServerCapability(workspace *workspaceState, capability string) bool {
	if workspace == nil {
		return false
	}
	for _, current := range workspace.serverCapabilities {
		if current == capability {
			return true
		}
	}
	return false
}

func validateReminderOwnerInputPayload(payload protocol.ReminderOwnerInputPayload) string {
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > reminderOwnerInputMaxPayloadBytes {
		return "payload_too_large"
	}
	for _, id := range []string{payload.WorkspaceID, payload.AgentID, payload.RuntimeID, payload.ReminderID, payload.Occurrence.OccurrenceID} {
		if strings.TrimSpace(id) == "" || len(id) > reminderOwnerInputMaxIDBytes {
			return "invalid_identity"
		}
	}
	if payload.PlacementGeneration < 1 || payload.Version < 1 || strings.TrimSpace(payload.Title) == "" || utf8.RuneCountInString(payload.Title) > 500 {
		return "invalid_version_or_title"
	}
	if len(payload.Anchor.Excerpt) > reminderOwnerInputMaxExcerptBytes || len(payload.Anchor.Target) > reminderOwnerInputMaxTargetBytes || len(payload.Anchor.ReplyTarget) > reminderOwnerInputMaxTargetBytes || len(payload.Occurrence.Cadence) > 256 || len(payload.Occurrence.Timezone) > 128 {
		return "bounded_context_exceeded"
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Occurrence.ScheduledFor); err != nil {
		return "invalid_scheduled_for"
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Occurrence.DueAt); err != nil {
		return "invalid_due_at"
	}
	anchorMetadata := []string{payload.Anchor.ChannelID, payload.Anchor.MessageID, payload.Anchor.ThreadRootMessageID, payload.Anchor.Target, payload.Anchor.ReplyTarget, payload.Anchor.Excerpt}
	if !payload.Anchor.Available {
		for _, value := range anchorMetadata {
			if value != "" {
				return "unavailable_anchor_leaked_metadata"
			}
		}
		return ""
	}
	for _, required := range []string{payload.Anchor.ChannelID, payload.Anchor.MessageID, payload.Anchor.Target, payload.Anchor.ReplyTarget} {
		if strings.TrimSpace(required) == "" || len(required) > reminderOwnerInputMaxTargetBytes {
			return "invalid_available_anchor"
		}
	}
	return ""
}

func (d *Daemon) recordReminderOwnerInputOutcome(payload protocol.ReminderOwnerInputPayload, outcome reminderOwnerInputOutcome, reason string) {
	if d == nil || d.logger == nil {
		return
	}
	fields := []any{
		"outcome", string(outcome),
		"reason_code", reason,
		"workspace_id", payload.WorkspaceID,
		"runtime_id", payload.RuntimeID,
		"agent_id", payload.AgentID,
		"reminder_id", payload.ReminderID,
		"version", payload.Version,
	}
	if outcome == reminderOwnerInputInjectionFailed || outcome == reminderOwnerInputRejected {
		d.logger.Warn("transient Reminder owner input", fields...)
		return
	}
	d.logger.Info("transient Reminder owner input", fields...)
}

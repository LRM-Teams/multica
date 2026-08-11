package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func formatResidentReminderInput(input ResidentReminderInput) (string, error) {
	if strings.TrimSpace(input.ReminderID) == "" || input.Version < 1 || strings.TrimSpace(input.Title) == "" || utf8.RuneCountInString(input.Title) > 500 {
		return "", errors.New("resident Reminder id, positive version, and bounded title are required")
	}
	if strings.TrimSpace(input.Occurrence.OccurrenceID) == "" {
		return "", errors.New("resident Reminder occurrence id is required")
	}
	if _, err := time.Parse(time.RFC3339Nano, input.Occurrence.ScheduledFor); err != nil {
		return "", errors.New("resident Reminder scheduled_for is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, input.Occurrence.DueAt); err != nil {
		return "", errors.New("resident Reminder due_at is invalid")
	}
	if input.Anchor.Available {
		if strings.TrimSpace(input.Anchor.ChannelID) == "" || strings.TrimSpace(input.Anchor.MessageID) == "" || strings.TrimSpace(input.Anchor.Target) == "" || strings.TrimSpace(input.Anchor.ReplyTarget) == "" {
			return "", errors.New("available resident Reminder Anchor requires message and return surface")
		}
	} else if input.Anchor.ChannelID != "" || input.Anchor.MessageID != "" || input.Anchor.ThreadRootMessageID != "" || input.Anchor.Target != "" || input.Anchor.ReplyTarget != "" || input.Anchor.Excerpt != "" {
		return "", errors.New("unavailable resident Reminder Anchor must not carry metadata")
	}

	type reminderAnchor struct {
		Available           bool   `json:"available"`
		ChannelID           string `json:"channel_id,omitempty"`
		MessageID           string `json:"message_id,omitempty"`
		ThreadRootMessageID string `json:"thread_root_message_id,omitempty"`
		Target              string `json:"target,omitempty"`
		ReplyTarget         string `json:"reply_target,omitempty"`
		Excerpt             string `json:"excerpt,omitempty"`
	}
	type reminderOccurrence struct {
		OccurrenceID string `json:"occurrence_id"`
		ScheduledFor string `json:"scheduled_for"`
		DueAt        string `json:"due_at"`
		Cadence      string `json:"cadence,omitempty"`
		Timezone     string `json:"timezone,omitempty"`
	}
	payload := struct {
		Kind       string             `json:"kind"`
		ReminderID string             `json:"reminder_id"`
		Version    int64              `json:"version"`
		Title      string             `json:"title"`
		Anchor     reminderAnchor     `json:"anchor"`
		Occurrence reminderOccurrence `json:"occurrence"`
	}{
		Kind:       "reminder",
		ReminderID: input.ReminderID,
		Version:    input.Version,
		Title:      input.Title,
		Anchor: reminderAnchor{
			Available: input.Anchor.Available, ChannelID: input.Anchor.ChannelID, MessageID: input.Anchor.MessageID,
			ThreadRootMessageID: input.Anchor.ThreadRootMessageID, Target: input.Anchor.Target,
			ReplyTarget: input.Anchor.ReplyTarget, Excerpt: input.Anchor.Excerpt,
		},
		Occurrence: reminderOccurrence{
			OccurrenceID: input.Occurrence.OccurrenceID, ScheduledFor: input.Occurrence.ScheduledFor,
			DueAt: input.Occurrence.DueAt, Cadence: input.Occurrence.Cadence, Timezone: input.Occurrence.Timezone,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal resident Reminder input: %w", err)
	}
	return "Private Reminder system input received while the owner Agent was idle. This transient input is not a canonical Message or proof that the reminded work completed. " +
		"Use the immutable Anchor only as context. If a visible response is warranted, send it with `multica message send --target <reply_target>` using the explicit anchor.reply_target; final assistant output is not delivered.\n" + string(raw), nil
}

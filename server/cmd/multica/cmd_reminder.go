package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var reminderCmd = &cobra.Command{
	Use:   "reminder",
	Short: "Schedule and manage future agent self-wakes",
}

var reminderScheduleCmd = &cobra.Command{Use: "schedule", Short: "Schedule a self-wake", RunE: runReminderSchedule}
var reminderListCmd = &cobra.Command{Use: "list", Short: "List your reminders", RunE: runReminderList}
var reminderSnoozeCmd = &cobra.Command{Use: "snooze", Short: "Snooze a reminder", RunE: runReminderSnooze}
var reminderUpdateCmd = &cobra.Command{Use: "update", Short: "Update a scheduled reminder", RunE: runReminderUpdate}
var reminderCancelCmd = &cobra.Command{Use: "cancel", Short: "Cancel a scheduled reminder", RunE: runReminderCancel}
var reminderLogCmd = &cobra.Command{Use: "log", Short: "Show a reminder's immutable lifecycle log", RunE: runReminderLog}

func init() {
	reminderCmd.AddCommand(reminderScheduleCmd, reminderListCmd, reminderSnoozeCmd, reminderUpdateCmd, reminderCancelCmd, reminderLogCmd)

	reminderScheduleCmd.Flags().String("title", "", "Reminder title")
	reminderScheduleCmd.Flags().Int64("delay-seconds", 0, "Delay before waking (60 seconds to 90 days)")
	reminderScheduleCmd.Flags().String("fire-at", "", "Absolute RFC3339 wake time")
	reminderScheduleCmd.Flags().String("repeat", "", "Recurring rule: every:Nm|Nh|Nd, daily@HH:MM, or weekly:days@HH:MM")
	reminderScheduleCmd.Flags().String("message-id", "", "Anchor channel message ID")
	reminderScheduleCmd.Flags().String("output", "json", "Output format: json")
	_ = reminderScheduleCmd.MarkFlagRequired("title")
	_ = reminderScheduleCmd.MarkFlagRequired("message-id")

	reminderListCmd.Flags().String("status", "active", "Filter: active, scheduled, firing, fired, cancelled, or all")
	reminderListCmd.Flags().String("output", "json", "Output format: json")

	for _, cmd := range []*cobra.Command{reminderSnoozeCmd, reminderUpdateCmd, reminderCancelCmd, reminderLogCmd} {
		cmd.Flags().String("id", "", "Reminder UUID or unique prefix")
		cmd.Flags().String("output", "json", "Output format: json")
		_ = cmd.MarkFlagRequired("id")
	}
	reminderSnoozeCmd.Flags().Int64("delay-seconds", 0, "New delay before waking")
	reminderSnoozeCmd.Flags().String("fire-at", "", "New absolute RFC3339 wake time")
	reminderUpdateCmd.Flags().String("title", "", "New reminder title")
	reminderUpdateCmd.Flags().Int64("delay-seconds", 0, "New delay before waking")
	reminderUpdateCmd.Flags().String("fire-at", "", "New absolute RFC3339 wake time")
	reminderUpdateCmd.Flags().String("cadence", "", "New recurring rule: every:Nm|Nh|Nd, daily@HH:MM, or weekly:days@HH:MM")
}

func reminderScheduleBody(cmd *cobra.Command, includeTitle bool) (map[string]any, error) {
	body := map[string]any{}
	if includeTitle {
		title, _ := cmd.Flags().GetString("title")
		if title != "" {
			body["title"] = title
		}
	}
	delay, _ := cmd.Flags().GetInt64("delay-seconds")
	fireAt, _ := cmd.Flags().GetString("fire-at")
	repeat := ""
	if cmd.Flags().Lookup("repeat") != nil {
		repeat, _ = cmd.Flags().GetString("repeat")
	}
	selected := 0
	if delay > 0 {
		selected++
	}
	if fireAt != "" {
		selected++
	}
	if repeat != "" {
		selected++
	}
	if selected != 1 {
		if cmd.Flags().Lookup("repeat") != nil {
			return nil, fmt.Errorf("provide exactly one of --delay-seconds, --fire-at, or --repeat")
		}
		return nil, fmt.Errorf("provide exactly one of --delay-seconds or --fire-at")
	}
	if delay > 0 {
		body["delay_seconds"] = delay
	} else if fireAt != "" {
		body["fire_at"] = fireAt
	} else {
		body["repeat"] = repeat
	}
	if includeTitle {
		messageID, _ := cmd.Flags().GetString("message-id")
		messageID = strings.TrimSpace(messageID)
		if messageID == "" {
			return nil, fmt.Errorf("--message-id is required")
		}
		body["message_id"] = messageID
	}
	return body, nil
}

func runReminderSchedule(cmd *cobra.Command, _ []string) error {
	body, err := reminderScheduleBody(cmd, true)
	if err != nil {
		return err
	}
	return postReminder(cmd, "/api/agent/reminders/schedule", body)
}

func runReminderList(cmd *cobra.Command, _ []string) error {
	status, _ := cmd.Flags().GetString("status")
	return postReminder(cmd, "/api/agent/reminders/list", map[string]any{"status": status})
}

func runReminderSnooze(cmd *cobra.Command, _ []string) error {
	body, err := reminderScheduleBody(cmd, false)
	if err != nil {
		return err
	}
	body["id"], _ = cmd.Flags().GetString("id")
	return postReminder(cmd, "/api/agent/reminders/snooze", body)
}

func runReminderUpdate(cmd *cobra.Command, _ []string) error {
	body, err := reminderUpdateBody(cmd)
	if err != nil {
		return err
	}
	return postReminder(cmd, "/api/agent/reminders/update", body)
}

func reminderUpdateBody(cmd *cobra.Command) (map[string]any, error) {
	body := map[string]any{}
	body["id"], _ = cmd.Flags().GetString("id")
	mutationCount := 0
	for _, name := range []string{"title", "delay-seconds", "fire-at", "cadence"} {
		if cmd.Flags().Changed(name) {
			mutationCount++
		}
	}
	if mutationCount != 1 {
		return nil, fmt.Errorf("provide exactly one of --title, --delay-seconds, --fire-at, or --cadence")
	}
	if title, _ := cmd.Flags().GetString("title"); cmd.Flags().Changed("title") {
		if strings.TrimSpace(title) == "" {
			return nil, fmt.Errorf("--title must not be empty")
		}
		body["title"] = title
	}
	delay, _ := cmd.Flags().GetInt64("delay-seconds")
	fireAt, _ := cmd.Flags().GetString("fire-at")
	if cmd.Flags().Changed("delay-seconds") {
		if delay <= 0 {
			return nil, fmt.Errorf("--delay-seconds must be a positive integer")
		}
		body["delay_seconds"] = delay
	}
	if cmd.Flags().Changed("fire-at") {
		if strings.TrimSpace(fireAt) == "" {
			return nil, fmt.Errorf("--fire-at must not be empty")
		}
		body["fire_at"] = fireAt
	}
	cadence, _ := cmd.Flags().GetString("cadence")
	if cmd.Flags().Changed("cadence") {
		if strings.TrimSpace(cadence) == "" {
			return nil, fmt.Errorf("--cadence must not be empty")
		}
		body["cadence"] = cadence
	}
	return body, nil
}

func runReminderCancel(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("id")
	return postReminder(cmd, "/api/agent/reminders/cancel", map[string]any{"id": id})
}

func runReminderLog(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("id")
	return postReminder(cmd, "/api/agent/reminders/log", map[string]any{"id": id})
}

func postReminder(cmd *cobra.Command, path string, body map[string]any) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	var response any
	if err := client.PostJSON(ctx, path, body, &response); err != nil {
		return fmt.Errorf("reminder: %w", err)
	}
	encoded, _ := json.MarshalIndent(response, "", "  ")
	fmt.Println(string(encoded))
	return nil
}

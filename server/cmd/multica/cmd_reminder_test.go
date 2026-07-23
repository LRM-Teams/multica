package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestReminderScheduleBodyValidation(t *testing.T) {
	cmd := reminderScheduleCmd
	cmd.Flags().Set("title", "check CI")
	cmd.Flags().Set("delay-seconds", "300")
	cmd.Flags().Set("fire-at", "")
	cmd.Flags().Set("repeat", "")
	cmd.Flags().Set("message-id", "00000000-0000-0000-0000-000000000001")
	body, err := reminderScheduleBody(cmd, true)
	if err != nil {
		t.Fatalf("delay body: %v", err)
	}
	if body["title"] != "check CI" || body["delay_seconds"] != int64(300) || body["message_id"] != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("unexpected body: %#v", body)
	}

	cmd = reminderScheduleCmd
	cmd.Flags().Set("title", "check CI")
	cmd.Flags().Set("delay-seconds", "0")
	cmd.Flags().Set("fire-at", "2026-07-10T04:00:00Z")
	cmd.Flags().Set("repeat", "")
	body, err = reminderScheduleBody(cmd, true)
	if err != nil {
		t.Fatalf("absolute body: %v", err)
	}
	if body["fire_at"] != "2026-07-10T04:00:00Z" {
		t.Fatalf("unexpected body: %#v", body)
	}

	cmd.Flags().Set("fire-at", "")
	cmd.Flags().Set("repeat", "weekly:mon,fri@09:00")
	body, err = reminderScheduleBody(cmd, true)
	if err != nil {
		t.Fatalf("repeat body: %v", err)
	}
	if body["repeat"] != "weekly:mon,fri@09:00" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestReminderScheduleBodyRequiresExactlyOneSchedule(t *testing.T) {
	cmd := reminderScheduleCmd
	cmd.Flags().Set("title", "check CI")
	cmd.Flags().Set("delay-seconds", "0")
	cmd.Flags().Set("fire-at", "")
	cmd.Flags().Set("repeat", "")
	cmd.Flags().Set("message-id", "00000000-0000-0000-0000-000000000001")
	if _, err := reminderScheduleBody(cmd, true); err == nil {
		t.Fatal("expected validation error when schedule is missing")
	}
	cmd.Flags().Set("delay-seconds", "60")
	cmd.Flags().Set("fire-at", "2026-07-10T04:00:00Z")
	if _, err := reminderScheduleBody(cmd, true); err == nil {
		t.Fatal("expected validation error when both schedule forms are present")
	}
}

func TestReminderScheduleBodyRequiresMessageID(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("title", "check CI", "")
	cmd.Flags().Int64("delay-seconds", 300, "")
	cmd.Flags().String("fire-at", "", "")
	cmd.Flags().String("repeat", "", "")
	cmd.Flags().String("message-id", "", "")
	if _, err := reminderScheduleBody(cmd, true); err == nil || err.Error() != "--message-id is required" {
		t.Fatalf("missing message id error = %v, want required", err)
	}
}

func TestReminderUpdateBodyRequiresExactlyOneMutation(t *testing.T) {
	newCommand := func(t *testing.T, title, delay, fireAt, cadence string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{}
		cmd.Flags().String("id", "", "")
		cmd.Flags().String("title", "", "")
		cmd.Flags().Int64("delay-seconds", 0, "")
		cmd.Flags().String("fire-at", "", "")
		cmd.Flags().String("cadence", "", "")
		if err := cmd.Flags().Set("id", "abc12345"); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{
			"title": title, "delay-seconds": delay, "fire-at": fireAt, "cadence": cadence,
		} {
			if value == "" {
				continue
			}
			if err := cmd.Flags().Set(name, value); err != nil {
				t.Fatalf("set %s: %v", name, err)
			}
		}
		return cmd
	}

	cmd := newCommand(t, "new title", "", "", "")
	body, err := reminderUpdateBody(cmd)
	if err != nil {
		t.Fatalf("single title mutation: %v", err)
	}
	if body["title"] != "new title" || len(body) != 2 {
		t.Fatalf("unexpected body: %#v", body)
	}

	for _, tc := range []struct {
		name, title, delay, fireAt, cadence string
	}{
		{name: "missing"},
		{name: "title and delay", title: "new title", delay: "300"},
		{name: "title and fire at", title: "new title", fireAt: "2026-07-10T04:00:00Z"},
		{name: "title and cadence", title: "new title", cadence: "every:2h"},
		{name: "delay and fire at", delay: "300", fireAt: "2026-07-10T04:00:00Z"},
		{name: "delay and cadence", delay: "300", cadence: "every:2h"},
		{name: "fire at and cadence", fireAt: "2026-07-10T04:00:00Z", cadence: "every:2h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newCommand(t, tc.title, tc.delay, tc.fireAt, tc.cadence)
			if _, err := reminderUpdateBody(cmd); err == nil {
				t.Fatal("expected exactly-one validation error")
			}
		})
	}
}

func TestReminderUpdateBodyRejectsInvalidSelectedValue(t *testing.T) {
	for _, tc := range []struct {
		name, flag, value string
	}{
		{name: "empty title", flag: "title", value: " "},
		{name: "zero delay", flag: "delay-seconds", value: "0"},
		{name: "empty fire at", flag: "fire-at", value: " "},
		{name: "empty cadence", flag: "cadence", value: " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().String("id", "abc12345", "")
			cmd.Flags().String("title", "", "")
			cmd.Flags().Int64("delay-seconds", 0, "")
			cmd.Flags().String("fire-at", "", "")
			cmd.Flags().String("cadence", "", "")
			if err := cmd.Flags().Set(tc.flag, tc.value); err != nil {
				t.Fatal(err)
			}
			if _, err := reminderUpdateBody(cmd); err == nil {
				t.Fatal("expected selected-value validation error")
			}
		})
	}
}

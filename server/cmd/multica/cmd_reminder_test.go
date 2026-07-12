package main

import "testing"

func TestReminderScheduleBodyValidation(t *testing.T) {
	cmd := reminderScheduleCmd
	cmd.Flags().Set("title", "check CI")
	cmd.Flags().Set("delay-seconds", "300")
	cmd.Flags().Set("fire-at", "")
	body, err := reminderScheduleBody(cmd, true)
	if err != nil {
		t.Fatalf("delay body: %v", err)
	}
	if body["title"] != "check CI" || body["delay_seconds"] != int64(300) {
		t.Fatalf("unexpected body: %#v", body)
	}

	cmd = reminderScheduleCmd
	cmd.Flags().Set("title", "check CI")
	cmd.Flags().Set("delay-seconds", "0")
	cmd.Flags().Set("fire-at", "2026-07-10T04:00:00Z")
	body, err = reminderScheduleBody(cmd, true)
	if err != nil {
		t.Fatalf("absolute body: %v", err)
	}
	if body["fire_at"] != "2026-07-10T04:00:00Z" {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestReminderScheduleBodyRequiresExactlyOneSchedule(t *testing.T) {
	cmd := reminderScheduleCmd
	cmd.Flags().Set("title", "check CI")
	cmd.Flags().Set("delay-seconds", "0")
	cmd.Flags().Set("fire-at", "")
	if _, err := reminderScheduleBody(cmd, true); err == nil {
		t.Fatal("expected validation error when schedule is missing")
	}
	cmd.Flags().Set("delay-seconds", "60")
	cmd.Flags().Set("fire-at", "2026-07-10T04:00:00Z")
	if _, err := reminderScheduleBody(cmd, true); err == nil {
		t.Fatal("expected validation error when both schedule forms are present")
	}
}

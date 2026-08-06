package handler

import (
	"testing"
	"time"
)

func TestParseReminderCadenceCanonicalGrammar(t *testing.T) {
	tests := []struct {
		rule     string
		timezone string
		want     string
	}{
		{rule: "every:15m", want: "every:15m"},
		{rule: "every:2h", want: "every:2h"},
		{rule: "every:1d", want: "every:1d"},
		{rule: "daily@09:05", timezone: "Asia/Shanghai", want: "daily@09:05"},
		{rule: "weekly:fri,mon,mon@09:00", timezone: "Europe/Berlin", want: "weekly:mon,fri@09:00"},
	}
	for _, test := range tests {
		t.Run(test.rule, func(t *testing.T) {
			got, err := parseReminderCadence(test.rule, test.timezone)
			if err != nil {
				t.Fatalf("parse cadence: %v", err)
			}
			if got.Canonical != test.want {
				t.Fatalf("canonical = %q, want %q", got.Canonical, test.want)
			}
		})
	}
}

func TestParseReminderCadenceRejectsInvalidRules(t *testing.T) {
	for _, test := range []struct {
		rule     string
		timezone string
	}{
		{},
		{rule: "every:0m"},
		{rule: "every:1w"},
		{rule: "every:91d"},
		{rule: "every:1h", timezone: "Asia/Shanghai"},
		{rule: "daily@9:00", timezone: "UTC"},
		{rule: "daily@24:00", timezone: "UTC"},
		{rule: "daily@09:00"},
		{rule: "weekly:@09:00", timezone: "UTC"},
		{rule: "weekly:monday@09:00", timezone: "UTC"},
		{rule: "weekly:mon@09:00", timezone: "Not/A/Zone"},
	} {
		if _, err := parseReminderCadence(test.rule, test.timezone); err == nil {
			t.Fatalf("parseReminderCadence(%q, %q) unexpectedly succeeded", test.rule, test.timezone)
		}
	}
}

func TestNextReminderCadenceEverySkipsMissedSlots(t *testing.T) {
	cadence, err := parseReminderCadence("every:15m", "")
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	now := slot.Add(52 * time.Minute)
	got, err := nextReminderCadenceAfterSlot(cadence, slot, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
}

func TestNextReminderCadenceDailySpringGap(t *testing.T) {
	cadence, err := parseReminderCadence("daily@02:30", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 3, 7, 8, 0, 0, 0, time.UTC)
	got, err := nextReminderCadence(cadence, after)
	if err != nil {
		t.Fatal(err)
	}
	// 02:30 does not exist on 2026-03-08. The occurrence moves to the
	// first valid wall-clock minute after the gap: 03:00 EDT.
	want := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("spring-gap next = %s, want %s", got, want)
	}
}

func TestNextReminderCadenceDailyInitialScheduleMayUseSecondFallOverlapInstant(t *testing.T) {
	cadence, err := parseReminderCadence("daily@01:30", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	got, err := nextReminderCadence(cadence, first.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	if !got.Equal(second) {
		t.Fatalf("fall-overlap next = %s, want %s", got, second)
	}
}

func TestNextReminderCadenceDailyFallOverlapFiresOncePerLocalDate(t *testing.T) {
	cadence, err := parseReminderCadence("daily@01:30", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	got, err := nextReminderCadenceAfterSlot(cadence, first, first.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 2, 6, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("daily fall-overlap next = %s, want next local date %s", got, want)
	}
}

func TestNextReminderCadenceWeeklyFallOverlapFiresOncePerLocalDate(t *testing.T) {
	cadence, err := parseReminderCadence("weekly:sun@01:30", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	got, err := nextReminderCadenceAfterSlot(cadence, first, first.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 8, 6, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("weekly fall-overlap next = %s, want next allowed local date %s", got, want)
	}
}

func TestNextReminderCadenceWeeklyLockedTimezone(t *testing.T) {
	cadence, err := parseReminderCadence("weekly:mon,fri@09:00", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC) // Wednesday
	got, err := nextReminderCadence(cadence, after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC) // Friday 09:00 CST
	if !got.Equal(want) {
		t.Fatalf("weekly next = %s, want %s", got, want)
	}
}

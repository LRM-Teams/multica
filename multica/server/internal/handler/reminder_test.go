package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseReminderFireAt(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	delay := int64(300)
	got, err := parseReminderFireAt(now, &delay, "")
	if err != nil {
		t.Fatalf("parse delay: %v", err)
	}
	if want := now.Add(5 * time.Minute); !got.Equal(want) {
		t.Fatalf("fire_at = %s, want %s", got, want)
	}

	for _, tc := range []struct {
		name   string
		delay  *int64
		fireAt string
	}{
		{name: "missing"},
		{name: "both", delay: &delay, fireAt: now.Add(time.Hour).Format(time.RFC3339)},
		{name: "too soon", delay: int64Ptr(30)},
		{name: "too late", delay: int64Ptr(int64((91 * 24 * time.Hour) / time.Second))},
		{name: "invalid absolute", fireAt: "tomorrow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseReminderFireAt(now, tc.delay, tc.fireAt); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBuildReminderPromptCarriesAnchorAndNoNoiseBoundary(t *testing.T) {
	reminder := agentReminder{
		ID:              parseUUID("11111111-1111-1111-1111-111111111111"),
		Title:           "回来看讨论是否已收敛",
		AnchorMessageID: parseUUID("22222222-2222-2222-2222-222222222222"),
	}
	prompt := buildReminderPrompt(ChannelResponse{Name: "产品讨论", Kind: "group"}, reminder,
		parseUUID("33333333-3333-3333-3333-333333333333"), "请给项目起一个名字", true)
	for _, want := range []string{
		"self-scheduled reminder is due",
		"回来看讨论是否已收敛",
		"#产品讨论",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"请给项目起一个名字",
		"If nothing changed",
		"runtime brief",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("reminder prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildReminderPromptHidesDMCanonicalChannelName(t *testing.T) {
	reminder := agentReminder{
		ID:              parseUUID("11111111-1111-1111-1111-111111111111"),
		Title:           "follow up privately",
		AnchorMessageID: parseUUID("22222222-2222-2222-2222-222222222222"),
	}
	prompt := buildReminderPrompt(ChannelResponse{Name: "dm:internal-user-a:internal-user-b", Kind: "dm"}, reminder,
		parseUUID("33333333-3333-3333-3333-333333333333"), "private anchor", true)
	if !strings.Contains(prompt, "Anchored surface: direct message") {
		t.Fatalf("DM prompt missing neutral surface:\n%s", prompt)
	}
	for _, forbidden := range []string{"dm:internal", "#dm:"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("DM prompt leaked canonical channel identity %q:\n%s", forbidden, prompt)
		}
	}
}

func TestReminderTargetKind(t *testing.T) {
	if got := reminderTargetKind(pgtype.UUID{}); got != "channel" {
		t.Fatalf("empty root target kind = %q", got)
	}
	if got := reminderTargetKind(parseUUID("33333333-3333-3333-3333-333333333333")); got != "thread" {
		t.Fatalf("thread target kind = %q", got)
	}
}

func int64Ptr(value int64) *int64 { return &value }

package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
	prompt := buildReminderPrompt(ChannelResponse{ID: "ch-1", Name: "产品讨论", Kind: "group"}, reminder,
		parseUUID("33333333-3333-3333-3333-333333333333"), "请给项目起一个名字", true)
	for _, want := range []string{
		"self-scheduled reminder is due",
		"回来看讨论是否已收敛",
		"msgid: 22222222-2222-2222-2222-222222222222",
		"Anchor excerpt: 请给项目起一个名字",
		"Target channel id: ch-1 (#产品讨论)",
		"33333333-3333-3333-3333-333333333333",
		"Reply only on that anchor surface",
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
	prompt := buildReminderPrompt(ChannelResponse{ID: "dm-ch", Name: "dm:internal-user-a:internal-user-b", Kind: "dm"}, reminder,
		parseUUID("33333333-3333-3333-3333-333333333333"), "private anchor", true)
	if !strings.Contains(prompt, "Target channel id: dm-ch (direct message)") {
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

func TestBuildReminderPromptPinsChannelID(t *testing.T) {
	reminder := agentReminder{Title: "patrol"}
	reminder.ID = parseUUID("11111111-1111-1111-1111-111111111111")
	prompt := buildReminderPrompt(ChannelResponse{ID: "ch-abc", Name: "产品", Kind: "group"}, reminder, parseUUID("22222222-2222-2222-2222-222222222222"), "hi", true)
	if !strings.Contains(prompt, "Target channel id: ch-abc (#产品)") {
		t.Fatalf("missing target channel id:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Reply only on that anchor surface") {
		t.Fatalf("missing surface pin language:\n%s", prompt)
	}
}

func TestFilterManagerChannelsTo(t *testing.T) {
	in := []ManagerChannelData{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
	}
	got := filterManagerChannelsTo(in, "b")
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("got %+v", got)
	}
	if len(filterManagerChannelsTo(in, "missing")) != 0 {
		t.Fatal("missing id should filter to empty")
	}
}

func TestEnforceReminderAnchorSurfaceMsgIDBind(t *testing.T) {
	chA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	chB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	root := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	otherRoot := "dddddddd-dddd-dddd-dddd-dddddddddddd"

	mainTask := db.AgentInboxEvent{
		Reason:    "reminder",
		ChannelID: parseUUID(chA),
		Context:   []byte(`{"type":"channel_wake","channel_id":"` + chA + `"}`),
	}
	threadTask := db.AgentInboxEvent{
		Reason:    "reminder",
		ChannelID: parseUUID(chA),
		Context:   []byte(`{"type":"channel_wake","channel_id":"` + chA + `","thread_root_message_id":"` + root + `"}`),
	}
	nonReminder := db.AgentInboxEvent{Reason: "mention", ChannelID: parseUUID(chA)}

	// Main anchor: channel main OK; other channel / thread DENY.
	if err := enforceReminderAnchorSurface(mainTask, chA, chatOutputTargetChannel, ""); err != nil {
		t.Fatalf("main→main allow: %v", err)
	}
	if err := enforceReminderAnchorSurface(mainTask, chB, chatOutputTargetChannel, ""); err == nil {
		t.Fatal("main→other channel must DENY")
	}
	if err := enforceReminderAnchorSurface(mainTask, chA, chatOutputTargetThread, root); err == nil {
		t.Fatal("main→thread must DENY")
	}

	// Thread anchor: only that thread OK.
	if err := enforceReminderAnchorSurface(threadTask, chA, chatOutputTargetThread, root); err != nil {
		t.Fatalf("thread→same thread allow: %v", err)
	}
	if err := enforceReminderAnchorSurface(threadTask, chA, chatOutputTargetChannel, ""); err == nil {
		t.Fatal("thread→main must DENY")
	}
	if err := enforceReminderAnchorSurface(threadTask, chA, chatOutputTargetThread, otherRoot); err == nil {
		t.Fatal("thread→other thread must DENY")
	}

	// Non-reminder: no gate.
	if err := enforceReminderAnchorSurface(nonReminder, chB, chatOutputTargetChannel, ""); err != nil {
		t.Fatalf("non-reminder must pass: %v", err)
	}
}

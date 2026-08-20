package handler

import (
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

func TestReminderTargetKind(t *testing.T) {
	if got := reminderTargetKind(pgtype.UUID{}); got != "channel" {
		t.Fatalf("empty root target kind = %q", got)
	}
	if got := reminderTargetKind(parseUUID("33333333-3333-3333-3333-333333333333")); got != "thread" {
		t.Fatalf("thread target kind = %q", got)
	}
}

func int64Ptr(value int64) *int64 { return &value }

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

	if err := enforceReminderAnchorSurface(mainTask, chA, chatOutputTargetChannel, ""); err != nil {
		t.Fatalf("main→main allow: %v", err)
	}
	if err := enforceReminderAnchorSurface(mainTask, chB, chatOutputTargetChannel, ""); err == nil {
		t.Fatal("main→other channel must DENY")
	}
	if err := enforceReminderAnchorSurface(mainTask, chA, chatOutputTargetThread, root); err == nil {
		t.Fatal("main→thread must DENY")
	}

	if err := enforceReminderAnchorSurface(threadTask, chA, chatOutputTargetThread, root); err != nil {
		t.Fatalf("thread→same thread allow: %v", err)
	}
	if err := enforceReminderAnchorSurface(threadTask, chA, chatOutputTargetChannel, ""); err == nil {
		t.Fatal("thread→main must DENY")
	}
	if err := enforceReminderAnchorSurface(threadTask, chA, chatOutputTargetThread, otherRoot); err == nil {
		t.Fatal("thread→other thread must DENY")
	}

	if err := enforceReminderAnchorSurface(nonReminder, chB, chatOutputTargetChannel, ""); err != nil {
		t.Fatalf("non-reminder must pass: %v", err)
	}
}

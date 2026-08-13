package handler

import "testing"

func TestClassifyNoteWritebackIssueTransitionWhitelist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prev   string
		next   string
		want   noteWritebackIssueEvent
		wantOK bool
	}{
		{name: "todo to done", prev: "todo", next: "done", want: noteWritebackIssueDone, wantOK: true},
		{name: "in_progress to done", prev: "in_progress", next: "done", want: noteWritebackIssueDone, wantOK: true},
		{name: "todo to cancelled", prev: "todo", next: "cancelled", want: noteWritebackIssueCancelled, wantOK: true},
		{name: "done to cancelled", prev: "done", next: "cancelled", want: noteWritebackIssueCancelled, wantOK: true},
		{name: "already done", prev: "done", next: "done", wantOK: false},
		{name: "already cancelled", prev: "cancelled", next: "cancelled", wantOK: false},
		{name: "todo to in_progress noise", prev: "todo", next: "in_progress", wantOK: false},
		{name: "in_progress to blocked noise", prev: "in_progress", next: "blocked", wantOK: false},
		{name: "backlog to todo noise", prev: "backlog", next: "todo", wantOK: false},
		{name: "in_review to in_progress noise", prev: "in_review", next: "in_progress", wantOK: false},
		{name: "done reopen to todo", prev: "done", next: "todo", wantOK: false},
		{name: "empty next", prev: "todo", next: "", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyNoteWritebackIssueTransition(tc.prev, tc.next)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("event = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoteWritebackIssueEventWhitelistIsClosed(t *testing.T) {
	t.Parallel()
	// Guard against accidental expansion: only terminal statuses belong here.
	for status := range noteWritebackIssueEventWhitelist {
		switch status {
		case "done", "cancelled":
		default:
			t.Fatalf("unexpected whitelist status %q", status)
		}
	}
	if len(noteWritebackIssueEventWhitelist) != 2 {
		t.Fatalf("whitelist size = %d, want 2", len(noteWritebackIssueEventWhitelist))
	}
}

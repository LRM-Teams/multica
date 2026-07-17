package handler

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func msg(role, content string) db.ChatMessage {
	return db.ChatMessage{Role: role, Content: content}
}

func contents(msgs []db.ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTrailingUserMessages pins the message-selection logic behind the daemon
// chat prompt: the agent must receive every user message since its last reply
// (the MUL-2968 debounce can land several before one run fires), not just the
// most recent one.
func TestChatResumeSafetyHelpers(t *testing.T) {
	msgs := []db.ChatMessage{
		msg("user", "old 1"),
		msg("assistant", "old 2"),
		msg("user", "new 3"),
	}
	if len(recentChatMessages(msgs, 999)) != len(msgs) {
		t.Fatal("large recent-message limits should preserve all messages")
	}
	if !chatFailureResumeUnsafe("agent_error.context_overflow") {
		t.Fatal("context overflow must not resume the native session")
	}
	if !chatFailureResumeUnsafe("grok_first_turn_no_progress") {
		t.Fatal("Grok first-turn no-progress must not resume the native session")
	}
	if !shouldIncludeChatContextSummary([]db.ChatMessage{msg("assistant", "确认继续吗？"), msg("user", "行")}) {
		t.Fatal("short confirmations after an assistant question should include recent context")
	}
	if shouldIncludeChatContextSummary([]db.ChatMessage{msg("assistant", "确认继续吗？"), msg("user", "我想看看今天上海天气")}) {
		t.Fatal("ordinary user messages should not force extra handoff context")
	}
	if shouldIncludeChatContextSummary([]db.ChatMessage{msg("assistant", "好的，有需要随时叫我。"), msg("user", "好")}) {
		t.Fatal("short acknowledgements after a non-question should not force extra handoff context")
	}
	currentTask := parseUUID("00000000-0000-0000-0000-000000000001")
	if !hasPriorChatContext([]db.ChatMessage{{Role: "user", Content: "old", TaskID: pgtype.UUID{}}}, currentTask) {
		t.Fatal("legacy or prior messages should force a fallback summary when native resume is unavailable")
	}
	if hasPriorChatContext([]db.ChatMessage{{Role: "user", Content: "current", TaskID: currentTask}}, currentTask) {
		t.Fatal("a chat containing only the current task message should not claim prior context")
	}
	if got := contents(recentChatMessages(msgs, 2)); !eq(got, []string{"old 2", "new 3"}) {
		t.Fatalf("recentChatMessages = %v", got)
	}
	line := compactChatLine("  a\n\t b   c  ")
	if line != "a b c" {
		t.Fatalf("compactChatLine = %q", line)
	}
	surface := buildConversationSurface("ws-1", "agent-1", pgtype.UUID{}, "", nil, "")
	summary := buildChatContextSummary(msgs, 123, "test reason", "ws-1", "agent-1", surface)
	for _, want := range []string{"Conversation context surface", "agent-run:ws-1:agent-1", "Native session resume was intentionally skipped", "test reason", "Recent surface messages", "Older tool outputs/log dumps are not included"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestChatContextSummaryCompactsLargeBlocks(t *testing.T) {
	largeLog := strings.Join([]string{
		"go test ./internal/handler",
		"--- FAIL: TestExample (0.01s)",
		"handler_test.go:12: expected ok",
		"stack trace line 1",
		"stack trace line 2",
		strings.Repeat("x", 900),
	}, "\n")
	msgs := []db.ChatMessage{msg("user", largeLog), msg("user", "fresh question")}
	surface := buildConversationSurface("ws-1", "agent-1", pgtype.UUID{}, "", nil, "")
	summary := buildChatContextSummary(msgs, 0, "budget", "ws-1", "agent-1", surface)
	for _, want := range []string{"go test ./internal/handler", "--- FAIL: TestExample", "omitted large log/code/json block", "fresh question"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, strings.Repeat("x", 200)) {
		t.Fatalf("summary kept oversized log tail:\n%s", summary)
	}
}

func TestShortConfirmationHandoffSummaryIncludesPreviousAgentQuestion(t *testing.T) {
	msgs := []db.ChatMessage{
		msg("user", "帮我把 feature 分支合到 dev"),
		msg("assistant", "为避免覆盖本地改动，我会用干净 worktree 合到 dev。确认继续吗？"),
		msg("user", "行"),
	}

	if !shouldIncludeChatContextSummary(msgs) {
		t.Fatal("short confirmation should force recent chat context even when native resume is available")
	}
	surface := buildConversationSurface("ws-1", "agent-1", pgtype.UUID{}, "", nil, "")
	summary := buildChatContextSummary(msgs, 0, "", "ws-1", "agent-1", surface)
	for _, want := range []string{
		"Recent surface messages:",
		"assistant: 为避免覆盖本地改动，我会用干净 worktree 合到 dev。确认继续吗？",
		"user: 行",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "Native session resume was intentionally skipped") {
		t.Fatalf("resume-available handoff should not claim native resume was skipped:\n%s", summary)
	}
}

func TestTrailingUserMessages(t *testing.T) {
	cases := []struct {
		name string
		in   []db.ChatMessage
		want []string
	}{
		{
			name: "debounced burst with no prior reply delivers all",
			in:   []db.ChatMessage{msg("user", "看上海天气"), msg("user", "还有青岛")},
			want: []string{"看上海天气", "还有青岛"},
		},
		{
			name: "only messages after the last assistant reply",
			in: []db.ChatMessage{
				msg("user", "old q"), msg("assistant", "old a"),
				msg("user", "看上海天气"), msg("user", "还有青岛"),
			},
			want: []string{"看上海天气", "还有青岛"},
		},
		{
			name: "single new message after a reply",
			in: []db.ChatMessage{
				msg("user", "看上海天气"), msg("user", "还有青岛"),
				msg("assistant", "weather…"), msg("user", "深圳呢"),
			},
			want: []string{"深圳呢"},
		},
		{
			name: "no trailing user message (last is assistant)",
			in:   []db.ChatMessage{msg("user", "hi"), msg("assistant", "done")},
			want: []string{},
		},
		{
			name: "empty history",
			in:   []db.ChatMessage{},
			want: []string{},
		},
		{
			name: "single user message",
			in:   []db.ChatMessage{msg("user", "hi")},
			want: []string{"hi"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := contents(trailingUserMessages(tc.in))
			if !eq(got, tc.want) {
				t.Fatalf("trailingUserMessages = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInboxPromptMessagesPreferCurrentEvent(t *testing.T) {
	currentEventID := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000222"), Valid: true}
	oldEventID := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000111"), Valid: true}
	msgs := []db.ChatMessage{
		{Role: "user", Content: strings.Repeat("old synthetic channel prompt", 20_000), TaskID: oldEventID},
		{Role: "user", Content: "current synthetic channel prompt", TaskID: currentEventID},
	}

	got := inboxPromptMessages(msgs, currentEventID)
	if len(got) != 1 || got[0].Content != "current synthetic channel prompt" {
		t.Fatalf("inboxPromptMessages = %#v, want only current event prompt", got)
	}
}

func TestInboxPromptMessagesRejectsUnlinkedLatestPrompt(t *testing.T) {
	msgs := []db.ChatMessage{
		{Role: "user", Content: strings.Repeat("old failed prompt", 20_000)},
		{Role: "user", Content: "unrelated latest prompt"},
	}

	got := inboxPromptMessages(msgs, pgtype.UUID{})
	if len(got) != 0 {
		t.Fatalf("inboxPromptMessages = %#v, want no guessed fallback", got)
	}
}

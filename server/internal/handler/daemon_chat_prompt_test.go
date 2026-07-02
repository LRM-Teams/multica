package handler

import (
	"strings"
	"testing"

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
	if shouldStartFreshChatSession(msgs, 0) {
		t.Fatal("short chat without high token usage should resume")
	}
	if !shouldStartFreshChatSession(make([]db.ChatMessage, chatFreshAfterMessageCount), 0) {
		t.Fatal("long chat should start fresh")
	}
	if !shouldStartFreshChatSession(msgs, chatFreshAfterTokenCount) {
		t.Fatal("high token usage should start fresh")
	}
	if !chatFailureResumeUnsafe("agent_error.context_overflow") {
		t.Fatal("context overflow must not resume the native session")
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

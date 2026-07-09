package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The welcome prompt must (1) name the joiner and channel, (2) route visible
// output through the runtime-selected chat transport, and (3) forbid
// @-mentions / follow-up — that last rule is what keeps a wall of welcomes
// from chaining into the automatic agent-reply loop.
func TestBuildChannelAmbientObservationPrompt(t *testing.T) {
	agent := db.Agent{Name: "总监助理", DisplayName: "总监助理", Description: "负责总监以上协调"}
	trigger := ChannelMessageResponse{ID: "11111111-1111-1111-1111-111111111111", AuthorName: "用户", Type: "user", Content: "全体总监以上欢迎一下新同事"}
	p := buildChannelAmbientObservationPrompt(ChannelResponse{Name: "产品讨论"}, agent, trigger)

	for _, want := range []string{
		"ONLY the current message",
		"finish without visible output",
		"do not print no_reply",
		"directly addresses your agent name",
		"全体",
		"Do not stay silent",
		"runtime brief",
		"visible message",
		"reaction",
		"Reaction target message id: 11111111-1111-1111-1111-111111111111",
		"short acknowledgement",
		"respond with a 👋 reaction",
		"explicitly asks for a sticker",
		"Do not print JSON envelopes",
		"全体总监以上欢迎一下新同事",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ambient prompt missing %q:\n%s", want, p)
		}
	}
	for _, banned := range []string{"multica send", "multica react", "multica message send", "multica message react", "multica message read", "multica message search"} {
		if strings.Contains(p, banned) {
			t.Errorf("ambient prompt should not hardcode chat CLI command %q:\n%s", banned, p)
		}
	}
	if strings.Contains(p, "Recent channel messages") {
		t.Error("ambient prompt must not include channel history")
	}
	if strings.Contains(p, "\"action\"") || strings.Contains(p, "\"parts\"") {
		t.Error("ambient prompt must not teach JSON action envelopes during transition")
	}
}

func TestBuildChannelAmbientUnreadPromptUsesRuntimeOutputContract(t *testing.T) {
	h := &Handler{}
	agent := db.Agent{Name: "总监助理", DisplayName: "总监助理", Description: "负责总监以上协调"}
	trigger := ChannelMessageResponse{ID: "11111111-1111-1111-1111-111111111111", AuthorName: "用户", Type: "user", Content: "全体总监以上欢迎一下新同事"}
	p := h.buildChannelAmbientUnreadPromptWithDB(context.Background(), channelPromptNoopDB{}, ChannelResponse{
		ID:          "22222222-2222-2222-2222-222222222222",
		WorkspaceID: "33333333-3333-3333-3333-333333333333",
		Name:        "产品讨论",
	}, agent, trigger, 1, 2)

	for _, want := range []string{
		"runtime brief",
		"visible message",
		"reaction",
		"Reaction target message id: 11111111-1111-1111-1111-111111111111",
		"Ambient cursor range: seq > 1 and seq <= 2",
		"全体总监以上欢迎一下新同事",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ambient unread prompt missing %q:\n%s", want, p)
		}
	}
	for _, banned := range []string{"multica send", "multica react", "multica message send", "multica message react", "multica message read", "multica message search"} {
		if strings.Contains(p, banned) {
			t.Errorf("ambient unread prompt should not hardcode chat CLI command %q:\n%s", banned, p)
		}
	}
}

func TestBuildChannelMentionPromptUsesCLITransportContract(t *testing.T) {
	h := &Handler{DB: channelPromptNoopDB{}}
	ch := ChannelResponse{
		ID:          "22222222-2222-2222-2222-222222222222",
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		Name:        "产品讨论",
	}
	threadRootID := "33333333-3333-3333-3333-333333333333"
	triggers := []ChannelMessageResponse{
		{AuthorName: "Frank", Type: "user", Content: "@Atlas 回复个表情包"},
		{AuthorName: "Frank", Type: "user", Content: "@Atlas 在线程里回复个表情包", ThreadRootMessageID: &threadRootID},
	}

	for _, trigger := range triggers {
		p := h.buildChannelMentionPrompt(context.Background(), ch, trigger)
		for _, want := range []string{
			"Multica group chat #产品讨论",
			"directly addressed to you",
			"visible result is required for human DMs",
			"agent-to-agent channel @mention is a weak notification",
			"finish without a visible reply",
			"Do not return no_reply",
			"greeting sticker only",
			"directly addressed to you (@-mention",
			"Collaborative discussion rule",
			"Never @ someone for thanks",
			"delivering a requested completion/blocker summary to the responsible human",
			"Do not print JSON envelopes",
			"Current message to respond to",
			trigger.Content,
		} {
			if !strings.Contains(p, want) {
				t.Errorf("mention prompt missing %q:\n%s", want, p)
			}
		}
		for _, banned := range []string{
			"internal no_reply is allowed",
			"END your reply by @-mentioning",
			"Only stop @-mentioning when you have reached a final conclusion",
		} {
			if strings.Contains(p, banned) {
				t.Errorf("direct mention prompt contains loop-prone instruction %q:\n%s", banned, p)
			}
		}
		for _, banned := range []string{"multica send", "multica react", "multica message send", "multica message react", "multica message read", "multica message search"} {
			if strings.Contains(p, banned) {
				t.Errorf("direct mention prompt should not hardcode chat CLI command %q:\n%s", banned, p)
			}
		}
		if strings.Contains(p, "\"action\"") || strings.Contains(p, "\"parts\"") {
			t.Errorf("direct mention prompt must not teach JSON action envelopes during transition:\n%s", p)
		}
	}
}

func TestBuildChannelMentionPromptIncludesCurrentReplyAndQuoteTargets(t *testing.T) {
	h := &Handler{DB: channelPromptNoopDB{}}
	ch := ChannelResponse{
		ID:          "22222222-2222-2222-2222-222222222222",
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		Name:        "multica-dev",
	}
	replyID := "33333333-3333-3333-3333-333333333333"
	quoteID := "44444444-4444-4444-4444-444444444444"
	trigger := ChannelMessageResponse{
		ID:               "55555555-5555-5555-5555-555555555555",
		AuthorName:       "用户",
		Type:             "user",
		Content:          "这条我说了什么",
		ReplyToMessageID: &replyID,
		ReplyTo: &ChannelMessageReply{
			ID:         replyID,
			Type:       "user",
			AuthorName: "用户",
			Content:    "继续",
			CreatedAt:  "2026-07-09T10:00:00Z",
		},
		QuoteMessageID: &quoteID,
		Quote: &ChannelMessageQuote{
			MessageID: quoteID,
			Status:    "active",
			Snapshot: &ChannelMessageQuoteSnapshot{
				Type:       "user",
				AuthorName: "用户",
				Content:    "继续",
				CreatedAt:  "2026-07-09T10:00:00Z",
			},
		},
	}

	p := h.buildChannelMentionPrompt(context.Background(), ch, trigger)
	for _, want := range []string{
		"Direct reply target for the current message:",
		"Direct quote target for the current message:",
		"[2026-07-09T10:00:00Z] 用户 (user): 继续",
		"treat the current message text as the user's question/request",
		"direct reply/quote target as the referenced message content",
		"Current message to respond to:",
		"用户 (user): 这条我说了什么",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("mention prompt missing %q:\n%s", want, p)
		}
	}
}

func TestFormatChannelMessageLineTruncatesHistoryContent(t *testing.T) {
	longContent := strings.Repeat("a", channelHistoryMessageMaxChars+50)
	line := formatChannelMessageLine(ChannelMessageResponse{AuthorName: "Frank", Type: "user", Content: longContent})
	if strings.Contains(line, strings.Repeat("a", channelHistoryMessageMaxChars+1)) {
		t.Fatalf("history line was not truncated: %d chars", len(line))
	}
	if !strings.Contains(line, "...[truncated]") {
		t.Fatalf("history line missing truncation marker:\n%s", line)
	}

	manyLines := strings.Join([]string{
		"01", "02", "03", "04", "05", "06", "07", "08", "09", "10",
		"11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "21",
	}, "\n")
	line = formatChannelMessageLine(ChannelMessageResponse{AuthorName: "Frank", Type: "user", Content: manyLines})
	if strings.Contains(line, "21") {
		t.Fatalf("history line kept content past line cap:\n%s", line)
	}
	if !strings.Contains(line, "...[truncated]") {
		t.Fatalf("line-capped history missing truncation marker:\n%s", line)
	}
}

func TestChannelContextMessagesExcludingTrigger(t *testing.T) {
	messages := []ChannelMessageResponse{
		{ID: "older", Content: "keep"},
		{ID: "trigger", Content: "drop"},
		{ID: "newer", Content: "keep"},
	}
	filtered := channelContextMessagesExcludingTrigger(messages, "trigger")
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered))
	}
	if filtered[0].ID != "older" || filtered[1].ID != "newer" {
		t.Fatalf("filtered messages = %#v", filtered)
	}
	if len(messages) != 3 || messages[1].ID != "trigger" {
		t.Fatalf("filter should not mutate input slice: %#v", messages)
	}
}

func TestBuildChannelWelcomePrompt(t *testing.T) {
	p := buildChannelWelcomePrompt("产品讨论", "张三")

	if !strings.Contains(p, "张三") {
		t.Error("prompt should name the joining member")
	}
	if !strings.Contains(p, "产品讨论") {
		t.Error("prompt should name the channel")
	}
	if !strings.Contains(p, "runtime brief's chat output path") {
		t.Error("prompt should instruct the agent to send a greeting sticker via the runtime brief")
	}
	if !strings.Contains(p, "Do NOT @-mention") {
		t.Error("prompt must forbid @-mentions to avoid re-triggering the agent-reply loop")
	}
	if !strings.Contains(strings.ToLower(p), "one short line") {
		t.Error("prompt must constrain the welcome to one short line")
	}
	for _, banned := range []string{"structured sticker", "stickers are unavailable", "sticker JSON", ":sticker:", "\"action\"", "\"parts\"", "\"sticker_id\""} {
		if strings.Contains(p, banned) {
			t.Errorf("welcome prompt must not expose internal sticker transport detail %q:\n%s", banned, p)
		}
	}
}

type channelPromptNoopDB struct{}

func (channelPromptNoopDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}

func (channelPromptNoopDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (channelPromptNoopDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return channelPromptNoopRow{}
}

type channelPromptNoopRow struct{}

func (channelPromptNoopRow) Scan(dest ...any) error {
	return pgx.ErrNoRows
}

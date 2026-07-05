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
// output through the CLI transport, and (3) forbid @-mentions / follow-up —
// that last rule is what keeps a wall of welcomes from chaining into the
// automatic agent-reply loop.
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
		"multica send",
		"multica react",
		"Reaction target message id: 11111111-1111-1111-1111-111111111111",
		"short acknowledgement",
		"structured sticker parts are unavailable",
		"全体总监以上欢迎一下新同事",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ambient prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "Recent channel messages") {
		t.Error("ambient prompt must not include channel history")
	}
	if strings.Contains(p, "\"action\"") || strings.Contains(p, "\"parts\"") {
		t.Error("ambient prompt must not teach JSON action envelopes during transition")
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
			"visible result by running `multica send`",
			"`multica react`",
			"Do not return no_reply",
			"structured sticker parts are unavailable",
			"Current message to respond to",
			trigger.Content,
		} {
			if !strings.Contains(p, want) {
				t.Errorf("mention prompt missing %q:\n%s", want, p)
			}
		}
		if strings.Contains(p, "internal no_reply is allowed") {
			t.Errorf("direct mention prompt must not allow silent no_reply:\n%s", p)
		}
		if strings.Contains(p, "\"action\"") || strings.Contains(p, "\"parts\"") {
			t.Errorf("direct mention prompt must not teach JSON action envelopes during transition:\n%s", p)
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
	if !strings.Contains(p, "plain text only") {
		t.Error("prompt should instruct the agent to welcome with plain text during transition")
	}
	if !strings.Contains(p, "structured stickers are unavailable") {
		t.Error("prompt should name that structured stickers are unavailable during transition")
	}
	if strings.Contains(p, "\"action\"") || strings.Contains(p, "\"parts\"") || strings.Contains(p, "\"sticker_id\"") {
		t.Error("prompt must not teach JSON structured parts during transition")
	}
	// Loop-prevention guarantees.
	if !strings.Contains(p, "Do NOT @-mention") {
		t.Error("prompt must forbid @-mentions to avoid re-triggering the agent-reply loop")
	}
	if !strings.Contains(strings.ToLower(p), "one short line") {
		t.Error("prompt must constrain the welcome to one short line")
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

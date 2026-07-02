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

// The welcome prompt must (1) name the joiner and channel, (2) ask for a
// sticker, and (3) forbid @-mentions / follow-up — that last rule is what keeps
// a wall of welcomes from chaining into the automatic agent-reply loop.
func TestBuildChannelAmbientObservationPrompt(t *testing.T) {
	agent := db.Agent{Name: "总监助理", DisplayName: "总监助理", Description: "负责总监以上协调"}
	trigger := ChannelMessageResponse{ID: "11111111-1111-1111-1111-111111111111", AuthorName: "用户", AuthorType: "user", Content: "全体总监以上欢迎一下新同事"}
	p := buildChannelAmbientObservationPrompt(ChannelResponse{Name: "产品讨论"}, agent, trigger)

	for _, want := range []string{
		"ONLY the current message",
		"stay silent",
		"全体",
		"Do not stay silent",
		"do not use a reaction-only command",
		"Reaction target message id: 11111111-1111-1111-1111-111111111111",
		"\"action\":\"message_react\"",
		"multica message react --message CURRENT_MESSAGE --emoji",
		"\"message_id\":\"CURRENT_MESSAGE\"",
		"💯",
		"🎉",
		"multica-stickers",
		"\"parts\"",
		"\"action\":\"send_channel_message\"",
		"\"sticker_id\":\"hi\"",
		"全体总监以上欢迎一下新同事",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("ambient prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "Recent channel messages") {
		t.Error("ambient prompt must not include channel history")
	}
}

func TestBuildChannelMentionPromptIncludesStickerInstruction(t *testing.T) {
	h := &Handler{DB: channelPromptNoopDB{}}
	ch := ChannelResponse{
		ID:          "22222222-2222-2222-2222-222222222222",
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		Name:        "产品讨论",
	}
	threadRootID := "33333333-3333-3333-3333-333333333333"
	triggers := []ChannelMessageResponse{
		{AuthorName: "Frank", AuthorType: "user", Content: "@Atlas 回复个表情包"},
		{AuthorName: "Frank", AuthorType: "user", Content: "@Atlas 在线程里回复个表情包", ThreadRootMessageID: &threadRootID},
	}

	for _, trigger := range triggers {
		p := h.buildChannelMentionPrompt(context.Background(), ch, trigger)
		for _, want := range []string{
			"Multica group chat #产品讨论",
			"multica-stickers",
			"\"parts\"",
			"\"sticker_id\":\"hi\"",
			"Current message to respond to",
			trigger.Content,
		} {
			if !strings.Contains(p, want) {
				t.Errorf("mention prompt missing %q:\n%s", want, p)
			}
		}
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
	if !strings.Contains(p, "\"parts\"") || !strings.Contains(p, "\"sticker_id\":\"applause\"") {
		t.Error("prompt should instruct the agent to include structured sticker parts")
	}
	if !strings.Contains(p, "\"action\":\"send_channel_message\"") {
		t.Error("prompt should instruct the agent to use the send_channel_message action")
	}
	if !strings.Contains(p, "multica-stickers") {
		t.Error("prompt should point at the multica-stickers skill")
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

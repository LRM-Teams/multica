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

// Ambient acknowledgement prompts route visible output through the
// runtime-selected chat transport and must not revive the retired forced
// all-agent welcome loop.
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
		p := h.buildChannelMentionPrompt(context.Background(), ch, trigger, channelFacilitatorState{})
		wantTarget := "#产品讨论"
		if trigger.ThreadRootMessageID != nil {
			wantTarget += ":" + (*trigger.ThreadRootMessageID)[:8]
		}
		for _, want := range []string{
			"Multica group chat #产品讨论",
			"directly addressed to you",
			"Human DMs, human @mentions, direct questions",
			"Agent-to-agent channel @mentions are weak notifications",
			"finish without visible output",
			"Never return no_reply",
			"greeting sticker only",
			"current message's source location",
			"Never create or switch to a thread based on message content or tone",
			"Substantive requests get a helpful answer using the requested supported delivery modality",
			"no acknowledgement sticker first",
			"sticker OR a short reply",
			"runtime brief's voice-delivery path",
			"Collaborative discussion rule",
			"never for thanks",
			"requested completion/blocker delivery",
			"Never print JSON envelopes",
			"Current message to respond to",
			"Message target for chat transport: " + wantTarget,
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

func TestAgentMessageTargetUsesOnlyExplicitThreadSource(t *testing.T) {
	h := &Handler{}
	ch := ChannelResponse{Name: "product", Kind: "group"}
	rootID := "11111111-1111-1111-1111-111111111111"

	if got := h.agentMessageTargetForPrompt(context.Background(), ch, ChannelMessageResponse{
		ID: rootID, Content: "@Atlas please investigate this?", ReplyToMessageID: &rootID,
	}); got != "#product" {
		t.Fatalf("top-level mention target = %q, want #product", got)
	}
	if got := h.agentMessageTargetForPrompt(context.Background(), ch, ChannelMessageResponse{
		ID: "22222222-2222-2222-2222-222222222222", ThreadRootMessageID: &rootID,
	}); got != "#product:"+rootID[:8] {
		t.Fatalf("thread source target = %q, want explicit thread target", got)
	}
}

func TestBuildChannelMentionPromptIncludesFacilitatorState(t *testing.T) {
	h := &Handler{DB: channelPromptNoopDB{}}
	ch := ChannelResponse{
		ID:          "22222222-2222-2222-2222-222222222222",
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		Name:        "产品讨论",
	}
	trigger := ChannelMessageResponse{AuthorName: "Frank", Type: "user", Content: "请主持并收敛这个讨论"}

	ownerPrompt := h.buildChannelMentionPrompt(context.Background(), ch, trigger, channelFacilitatorState{
		Active:                    true,
		FacilitatorName:           "Atlas",
		CurrentAgentIsFacilitator: true,
	})
	for _, want := range []string{
		"Facilitator mode is active for you",
		"2-4 short purposeful follow-ups",
		"conclusion, a clear owner",
	} {
		if !strings.Contains(ownerPrompt, want) {
			t.Errorf("facilitator owner prompt missing %q:\n%s", want, ownerPrompt)
		}
	}

	participantPrompt := h.buildChannelMentionPrompt(context.Background(), ch, ChannelMessageResponse{
		AuthorName: "Atlas",
		Type:       "agent",
		Content:    "请从前端体验角度比较 A 和 B，你推荐哪个？",
	}, channelFacilitatorState{
		Active:                         true,
		FacilitatorName:                "Atlas",
		CurrentTriggerFromFacilitator:  true,
		CurrentTriggerIsDirectAgentAsk: true,
	})
	for _, want := range []string{
		"Facilitator request: Atlas",
		"direct request, not a weak agent-to-agent notification",
		"Answer once",
	} {
		if !strings.Contains(participantPrompt, want) {
			t.Errorf("facilitator participant prompt missing %q:\n%s", want, participantPrompt)
		}
	}
}

func TestDetectFacilitatorIntentAndConcreteRequest(t *testing.T) {
	for _, content := range []string{
		"你来主持这次讨论并收敛方案",
		"带大家讨论 20 分钟",
		"Please facilitate this discussion",
	} {
		if !detectFacilitatorIntent(content) {
			t.Errorf("detectFacilitatorIntent(%q) = false", content)
		}
	}
	if detectFacilitatorIntent("帮我修一下登录 bug") {
		t.Error("ordinary task should not enter facilitator mode")
	}
	if !looksLikeConcreteFacilitatorRequest("请从域名可用性筛 3 个，你推荐哪个？") {
		t.Error("concrete facilitator request was not detected")
	}
	if looksLikeConcreteFacilitatorRequest("大家辛苦了") {
		t.Error("acknowledgement should not become a direct facilitator request")
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

	p := h.buildChannelMentionPrompt(context.Background(), ch, trigger, channelFacilitatorState{})
	for _, want := range []string{
		"Direct reply target for the current message:",
		"Direct quote target for the current message:",
		"[2026-07-09T10:00:00Z] 用户 (user): 继续",
		"treat the current message text as the user's question/request",
		"direct reply/quote target as the referenced message content",
		"Message target for chat transport: #multica-dev",
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

func TestBuildChannelOnboardingPrompt(t *testing.T) {
	const (
		workspaceID = "33333333-3333-3333-3333-333333333333"
		channelID   = "5a7ba4e8-85a2-4a7a-8047-a38c41ef85ed"
		agentID     = "cf7670af-d77f-469c-b395-36b390119bd7"
	)
	h := &Handler{DB: channelPromptNoopDB{}}
	p := h.buildChannelOnboardingPrompt(context.Background(), channelOnboardingRecord{
		ID:                     parseUUID("11111111-1111-1111-1111-111111111111"),
		WorkspaceID:            parseUUID(workspaceID),
		ChannelID:              parseUUID(channelID),
		AgentID:                parseUUID(agentID),
		MembershipGenerationID: parseUUID("22222222-2222-2222-2222-222222222222"),
		SourceType:             "manual",
		ChannelName:            "产品讨论",
	}, ChannelResponse{
		ID:          channelID,
		WorkspaceID: workspaceID,
		Name:        "产品讨论",
		Kind:        "group",
	}, db.Agent{
		ID:          parseUUID(agentID),
		Name:        "backend-agent",
		DisplayName: "张三",
		Description: "Backend engineer",
	})

	if !strings.Contains(p, "张三") {
		t.Error("prompt should name the onboarded agent")
	}
	if !strings.Contains(p, "产品讨论") {
		t.Error("prompt should name the channel")
	}
	if !strings.Contains(p, "finish without sending a message") {
		t.Error("prompt should allow a quiet normal completion")
	}
	if strings.Contains(p, "channel_onboarding_skipped") || strings.Contains(p, "channel-onboarding:") {
		t.Error("prompt should not expose a special receipt or client id")
	}
	if !strings.Contains(p, "exact target") {
		t.Error("prompt must bind any visible send to the joined channel")
	}
	if !strings.Contains(p, "Send at most one concise message") {
		t.Error("prompt must constrain onboarding to at most one visible message")
	}
	if !strings.Contains(p, "ordinary final/completion text") {
		t.Error("prompt must reject final prose as visible onboarding output")
	}
	if !strings.Contains(p, agentID) {
		t.Error("prompt must include the onboarded agent id")
	}
	if !strings.Contains(p, channelID) {
		t.Error("prompt must include the exact channel id")
	}
	for _, banned := range []string{"RELATIONSHIP.md", "greeting sticker", "must welcome", "Do not stay silent", "\"action\"", "\"parts\""} {
		if strings.Contains(p, banned) {
			t.Errorf("onboarding prompt must not preserve the old forced-welcome contract %q:\n%s", banned, p)
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

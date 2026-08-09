package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const agentBID = "11111111-1111-1111-1111-111111111111"

func agentMentionPart(label, id string) protocol.MessagePart {
	return protocol.MessagePart{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      id,
		Label:      label,
	}
}

func mentionContent(label, id string, body string) string {
	return "[" + label + "](mention://agent/" + id + ") " + body
}

func TestChannelContentIsPureConfirmation(t *testing.T) {
	cases := []struct {
		name    string
		content string
		parts   []protocol.MessagePart
		want    bool
	}{
		{"bare chinese ack", "收到", nil, true},
		{"ack with punctuation", "收到！", nil, true},
		{"ack with glue", "好的啦~", nil, true},
		{"复合确认", "好的收到", nil, true},
		{"已办理", "已办理", nil, true},
		{"english ok", "OK", nil, true},
		{"english ok lower", "ok", nil, true},
		{"english got it", "Got it.", nil, true},
		{"english thanks", "thanks!", nil, true},
		{"ack to agent", mentionContent("@AgentB", agentBID, "收到"), []protocol.MessagePart{agentMentionPart("@AgentB", agentBID)}, true},
		{"ack to agent with label", "收到 [@AgentB](mention://agent/" + agentBID + ")", []protocol.MessagePart{agentMentionPart("@AgentB", agentBID)}, true},

		{"ack plus new content", "收到，我来处理这个 bug", nil, false},
		{"ack plus task directive", "收到 @AgentB 请你检查一下这个接口", []protocol.MessagePart{agentMentionPart("@AgentB", agentBID)}, false},
		{"real question", "这个方案你确认一下？", nil, false},
		{"real update", "我已经完成了迁移并补了测试", nil, false},
		{"real instruction", "请把这段代码 review 一下", nil, false},
		{"ok with trailing directive", "好的，那我开始写了", nil, false},
		{"empty", "", nil, false},
		{"long informative reply", "明白，问题根因是查询少了一个索引，我加上了", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := channelContentIsPureConfirmation(tc.content, tc.parts)
			if got != tc.want {
				t.Fatalf("channelContentIsPureConfirmation(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestChannelMessageIsPureConfirmationMentionDetection(t *testing.T) {
	trigger := ChannelMessageResponse{
		Content: mentionContent("@AgentB", agentBID, "收到"),
		Parts:   []protocol.MessagePart{agentMentionPart("@AgentB", agentBID)},
	}
	pure, hasMention := channelMessageIsPureConfirmation(trigger)
	if !pure {
		t.Fatalf("expected pure confirmation, got pure=false")
	}
	if !hasMention {
		t.Fatalf("expected hasMention=true")
	}

	standalone := ChannelMessageResponse{Content: "收到", Parts: nil}
	pure, hasMention = channelMessageIsPureConfirmation(standalone)
	if !pure || hasMention {
		t.Fatalf("standalone ack: expected pure=true hasMention=false, got pure=%v hasMention=%v", pure, hasMention)
	}
}

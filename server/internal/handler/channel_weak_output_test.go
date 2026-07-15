package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestWeakAgentThreadNonActionOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "not posting rationale", output: "不发布——老胡的服务器端审核已通过，等待阿策推进。", want: true},
		{name: "short ack", output: "收到。", want: true},
		{name: "no action", output: "No further action needed from me.", want: true},
		{name: "no new request rationale", output: "这条消息是贝克汉姆在回应我已经发布的结论，没有新的可执行请求，我补充任何内容都会显得多余。", want: true},
		{name: "settled topic rationale", output: "话题已收敛（LRM-126 done、AI 等明天），阿策正确不再重复。无事可催，不刷屏。", want: true},
		{name: "channel reply not needed rationale", output: "已完成 — 无需进行频道回复。这只是一个深层的回声，发布任何回复都会导致刷屏。", want: true},
		{name: "substantive proactive answer", output: "我看了 issue110，这里建议先把等待态文案改成明确的‘还差 1 人’，否则用户会以为卡住。", want: false},
		{name: "substantive english answer", output: "I can help here: the server should keep start(false) authoritative and return not_enough_players for two-human rooms.", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWeakAgentThreadNonActionOutput(tt.output); got != tt.want {
				t.Fatalf("isWeakAgentThreadNonActionOutput(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestChannelAgentThreadReplyIsDirected(t *testing.T) {
	h := &Handler{}
	productAgent := db.Agent{
		ID:          parseUUID("11111111-1111-1111-1111-111111111111"),
		Name:        "pm_agent",
		DisplayName: "产品经理",
		Description: "负责产品经理、需求澄清和 issue 优先级判断",
	}
	backendAgent := db.Agent{
		ID:          parseUUID("22222222-2222-2222-2222-222222222222"),
		Name:        "backend_agent",
		DisplayName: "服务端",
		Description: "负责后端和服务端实现",
	}

	tests := []struct {
		name    string
		content string
		agent   db.Agent
		want    bool
	}{
		{
			name:    "status update is weak",
			content: "服务端 start(fillBots) 口径复核完成，结论通过。LRM-126 现在等待阿策推进。",
			agent:   productAgent,
			want:    false,
		},
		{
			name:    "role targeted product manager request is directed",
			content: "产品经理你看一下 issue110，确认这个交互是不是合理。",
			agent:   productAgent,
			want:    true,
		},
		{
			name:    "role targeted request does not wake unrelated role visibly",
			content: "产品经理你看一下 issue110，确认这个交互是不是合理。",
			agent:   backendAgent,
			want:    false,
		},
		{
			name:    "all agents with task request is directed",
			content: "所有 agent 都看一下 issue110，有问题直接说。",
			agent:   backendAgent,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger := channelAgentTriggerContext{Reason: "thread_reply", AuthorType: "agent", Content: tt.content, ChannelID: pgtype.UUID{}, WorkspaceID: pgtype.UUID{}}
			if got := h.channelAgentThreadReplyIsDirected(nil, trigger, tt.agent); got != tt.want {
				t.Fatalf("channelAgentThreadReplyIsDirected(%q, %q) = %v, want %v", tt.content, tt.agent.DisplayName, got, tt.want)
			}
		})
	}
}

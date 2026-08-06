package handler

import (
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchwake"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func requireActiveResearchFleetMember(member db.ResearchFleetMember, err error) error {
	return researchwake.RequireActiveMember(member.Status, err)
}

func researchWakeFailureEvent(target pgtype.UUID, err error) researchProcessEvent {
	reason, title, body, hint := classifyResearchWakeFailure(err)
	meta := map[string]any{
		"reason": reason,
		"error":  err.Error(),
	}
	if hint != "" {
		meta["recovery_hint"] = hint
	}
	fullBody := body
	if hint != "" {
		fullBody = body + " " + hint
	}
	return researchProcessEvent{
		Op:      "wake_failed",
		Title:   title,
		Body:    fullBody,
		ActorID: target,
		Meta:    meta,
	}
}

func classifyResearchWakeFailure(err error) (reason, title, body, hint string) {
	switch {
	case errors.Is(err, service.ErrChatTaskAgentArchived):
		return researchwake.ReasonAgentArchived,
			"唤醒失败",
			"目标 agent 已归档",
			"请改派给其他活跃成员。"
	case errors.Is(err, service.ErrChatTaskAgentNoRuntime):
		return researchwake.ReasonAgentNoRuntime,
			"唤醒失败",
			"目标 agent 未绑定 runtime",
			"请为该 agent 配置 runtime 后重试。"
	case errors.Is(err, service.ErrAgentModelRequired):
		return researchwake.ReasonAgentModelRequired,
			"唤醒失败",
			"目标 agent 未配置模型",
			"请为该成员设置 model 后重试；系统会在下次 wake 自动补默认模型。"
	default:
		return researchwake.FailurePresentation(err)
	}
}

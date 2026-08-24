package researchwake

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	ReasonNotMember          = "fleet_member_not_found"
	ReasonPendingReview      = "fleet_member_pending_review"
	ReasonArchived           = "fleet_member_archived"
	ReasonNotActive          = "fleet_member_not_active"
	ReasonAgentArchived      = "agent_archived"
	ReasonAgentNoRuntime     = "agent_no_runtime"
	ReasonAgentModelRequired = "agent_model_required"
	ReasonRuntimeOffline     = "runtime_offline"
	ReasonInternal           = "wake_internal_error"
)

type Error struct {
	Reason  string
	Message string
	Hint    string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Reason
}

func NewError(reason, message, hint string) *Error {
	return &Error{Reason: reason, Message: message, Hint: hint}
}

func RequireActiveMember(status string, lookupErr error) error {
	if lookupErr != nil {
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return NewError(
				ReasonNotMember,
				"目标 agent 不是调研团成员",
				"请改派给罗纳尔多或其他在册成员，或通过招聘流程添加成员。",
			)
		}
		return fmt.Errorf("lookup research fleet member: %w", lookupErr)
	}
	switch status {
	case "active":
		return nil
	case "pending_prompt_review":
		return NewError(
			ReasonPendingReview,
			"目标成员尚在 prompt 审核中，暂不能接收任务",
			"请让罗纳尔多完成 optimize 并激活该成员后重试。",
		)
	case "archived":
		return NewError(
			ReasonArchived,
			"目标成员已归档",
			"请改派给其他活跃成员，或重新招聘该角色。",
		)
	default:
		return NewError(
			ReasonNotActive,
			fmt.Sprintf("目标成员状态为 %s，不能接收任务", status),
			"请改派给活跃成员后重试。",
		)
	}
}

func FailurePresentation(err error) (reason, title, body, hint string) {
	var wakeErr *Error
	if errors.As(err, &wakeErr) {
		return wakeErr.Reason, failureTitle(wakeErr.Reason), wakeErr.Message, wakeErr.Hint
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "agent archived") || strings.Contains(lower, "chat task: agent archived"):
		return ReasonAgentArchived, "唤醒失败", "目标 agent 已归档", "请改派给其他活跃成员。"
	case strings.Contains(lower, "no runtime") || strings.Contains(lower, "agent has no runtime"):
		return ReasonAgentNoRuntime, "唤醒失败", "目标 agent 未绑定 runtime", "请为该 agent 配置 runtime 后重试。"
	case strings.Contains(lower, "agent model is required"):
		return ReasonAgentModelRequired, "唤醒失败", "目标 agent 未配置模型", "请为该成员设置 model 后重试；系统会在下次 wake 自动补默认模型。"
	case strings.Contains(lower, "runtime") || strings.Contains(lower, "daemon") || strings.Contains(lower, "offline"):
		return ReasonRuntimeOffline, "唤醒失败", "目标 agent 的 runtime/daemon 可能离线", "请确认 daemon 在线后重试，或改派给其他在线成员。"
	default:
		return ReasonInternal, "唤醒失败", fmt.Sprintf("未能唤醒目标 agent：%s", msg), "请重试，或改派给罗纳尔多。"
	}
}

func failureTitle(reason string) string {
	switch reason {
	case ReasonNotMember:
		return "目标非调研团成员"
	case ReasonPendingReview:
		return "成员待审核"
	case ReasonArchived:
		return "成员已归档"
	case ReasonNotActive:
		return "成员未激活"
	default:
		return "唤醒失败"
	}
}
